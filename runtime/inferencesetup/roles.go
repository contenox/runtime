// Package inferencesetup owns inference-role configuration and verification.
package inferencesetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcapability"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
)

const (
	RoleChat         = "primary"
	RoleRecovery     = "alternative"
	RoleAutocomplete = "autocomplete"
	RoleNextEdit     = "next-edit"
	RoleEmbedding    = "embedding"
)

// RoleIDs is every inference role an assignment may name.
func RoleIDs() []string {
	return []string{RoleChat, RoleRecovery, RoleAutocomplete, RoleNextEdit, RoleEmbedding}
}

var ErrSetupIncomplete = errors.New("inference setup is incomplete")

// ErrRoleDisabled marks a request that needs an optional role the operator turned off.
var ErrRoleDisabled = errors.New("inference role is turned off")

type optionalRole struct {
	id               string
	enabledByDefault bool
}

var optionalRoles = []optionalRole{
	{id: RoleNextEdit, enabledByDefault: false},
	{id: RoleEmbedding, enabledByDefault: false},
}

// OptionalRoles lists the roles an operator may turn off. Every other role is required.
func OptionalRoles() []string {
	ids := make([]string, 0, len(optionalRoles))
	for _, role := range optionalRoles {
		ids = append(ids, role.id)
	}
	return ids
}

func optionalRoleSpec(role string) (optionalRole, bool) {
	for _, spec := range optionalRoles {
		if spec.id == role {
			return spec, true
		}
	}
	return optionalRole{}, false
}

func enabledKey(role string) string { return "inference.enabled." + role }

func readRoleEnabled(ctx context.Context, store runtimetypes.Store, role string) (bool, error) {
	spec, optional := optionalRoleSpec(role)
	if !optional {
		return true, nil
	}
	value, err := readSetting(ctx, store, enabledKey(role))
	if err != nil {
		return false, err
	}
	switch value {
	case "enabled":
		return true, nil
	case "disabled":
		return false, nil
	}
	return spec.enabledByDefault, nil
}

// ErrConfigurationChanged requires a fresh review after a concurrent configuration edit.
var ErrConfigurationChanged = errors.New("inference configuration changed during verification; review the current configuration and retry")

// CompletionContract describes explicit operator declaration without claiming native verification.
var CompletionContract = servicecontract.Contract{
	ID: "instructed-code-completion", Summary: "Declare an instructed code-completion operation",
	Description: "Declare whether a provider model uses the chat completion chain or one bounded prompt operation. Assigning the model still requires successful execution of the selected IDE completion operation.",
	Operations: []servicecontract.Operation{{
		ID: "declare", Summary: "Declare instructed code-completion support",
		Spec: "Declare which operation a provider model serves for code completion: the chat\ncompletion chain, or one bounded prompt operation using the model's own\nfill-in-the-middle tokens. The declaration is recorded against the model, and\nassigning the model still requires the selected operation to run successfully.",
		Inputs: []servicecontract.Input{
			{ID: "provider", Summary: "Connection selector or provider type the declaration applies to", Type: "string", Required: true},
			{ID: "model", Summary: "Model the declaration applies to", Type: "string", Required: true},
			{ID: "completionMode", Summary: "Declared completion mode", Type: "string", Required: true, Values: []string{modelregistry.CompletionInstruct, modelregistry.CompletionPrompt}},
		},
		Output: servicecontract.Output{
			Stdout: "The model identity and the completion mode now declared for it.",
			Format: "text",
		},
		Outcomes: []servicecontract.Outcome{
			{Code: 0, When: "the mode is declared", Meaning: "the completion route resolves through the declared operation"},
		},
		Refusals: []servicecontract.Refusal{
			{When: "the provider or model is not registered", Message: "names the connection and the command that lists its models"},
			{When: "the mode is not one this runtime serves", Message: "names the unsupported mode and the modes it accepts"},
		},
	}},
}

// ModelFactory returns a model repository reflecting the latest persisted provider configuration.
type ModelFactory interface {
	Models(context.Context) (llmrepo.ModelRepo, func(), error)
}

// CompletionFactory verifies completion through the same operation used by the IDE.
type CompletionFactory interface {
	Complete(context.Context, Assignment, string, string) (string, error)
	CompletionFingerprint(context.Context) (string, error)
	ExecutionFingerprint(context.Context, string, string) (string, error)
}

// NextEditFactory executes and fingerprints a model's declared independent edit operation.
type NextEditFactory interface {
	NextEdit(context.Context, Assignment, modelcontract.NextEditInput) ([]modelcontract.TextEdit, error)
	NextEditExecutionFingerprint(context.Context, string) (string, error)
}

// Assignment identifies one explicitly selected inference route.
type Assignment struct {
	Inputs   InputRequirement `json:"inputs"`
	Role     string           `json:"role"`
	Provider string           `json:"provider"`
	Model    string           `json:"model"`
}

// ProviderRequest is one native provider configuration submitted by the IDE.
type ProviderRequest struct {
	CredentialSource string
	CredentialFile   string
	ID               string
	Name             string
	Provider         string
	Endpoint         string
	APIKey           string
	APIKeyEnv        string
}

// Verification records the route and result proven by a real capability request.
type Verification struct {
	Inputs                InputRequirement             `json:"inputs"`
	NativeExecutions      []transport.SessionExecution `json:"nativeExecutions,omitempty"`
	ConfigurationRevision string                       `json:"configurationRevision,omitempty"`
	RoleRevision          string                       `json:"roleRevision"`
	Role                  string                       `json:"role"`
	Provider              string                       `json:"provider"`
	Model                 string                       `json:"model"`
	Fingerprint           string                       `json:"fingerprint"`
	Dimension             int                          `json:"dimension,omitempty"`
	VerifiedAt            time.Time                    `json:"verifiedAt"`
	Sample                string                       `json:"sample,omitempty"`
}

// AssignmentSource names which path saved a role assignment.
type AssignmentSource string

// Assignment sources. Every path that saves a role assignment records exactly one.
const (
	SourceUser    AssignmentSource = "user"
	SourcePlanner AssignmentSource = "planner"
)

// AssignmentSources lists every assignment source a role may record.
func AssignmentSources() []string {
	return []string{string(SourceUser), string(SourcePlanner)}
}

// Valid reports whether the source is one of the declared assignment sources.
func (s AssignmentSource) Valid() bool {
	return slices.Contains(AssignmentSources(), string(s))
}

func sourceKey(role string) string { return "inference.source." + role }

// RoleStatus reports the effective assignment and whether its current configuration was verified.
type RoleStatus struct {
	Assignment
	Enabled         bool             `json:"enabled"`
	AssignmentState string           `json:"assignmentState"`
	ConnectionID    string           `json:"connectionId"`
	Revision        string           `json:"revision"`
	Name            string           `json:"name"`
	State           string           `json:"state"`
	Detail          string           `json:"detail,omitempty"`
	Problem         string           `json:"problem,omitempty"`
	Inherited       bool             `json:"inherited"`
	Source          AssignmentSource `json:"source,omitempty"`
	Verification    *Verification    `json:"verification,omitempty"`
}

// Service owns inference-role assignment and verification.
type Service struct {
	bus    libbus.Messenger
	db     libdbexec.DBManager
	state  *runtimestate.State
	models ModelFactory
}

// New constructs the inference setup service from required runtime dependencies.
func New(db libdbexec.DBManager, state *runtimestate.State, bus libbus.Messenger, models ModelFactory) (*Service, error) {
	if db == nil {
		return nil, errors.New("inference setup database is required")
	}
	if state == nil {
		return nil, errors.New("inference setup runtime state is required")
	}
	if bus == nil {
		return nil, errors.New("inference setup event bus is required")
	}
	if models == nil {
		return nil, errors.New("inference setup model factory is required")
	}
	return &Service{db: db, state: state, bus: bus, models: models}, nil
}

// ModelFactory returns the factory the service builds its model repositories from, so a host that composes extra collaborators into it can reach them.
func (s *Service) ModelFactory() ModelFactory {
	return s.models
}

var roleSpecs = []struct{ id, name string }{
	{RoleChat, "Chat"}, {RoleAutocomplete, "Autocomplete"}, {RoleNextEdit, "Next Edit"}, {RoleRecovery, "Recovery"}, {RoleEmbedding, "Workspace Search"},
}

// Roles returns the effective role assignments and their verification state.
func (s *Service) Roles(ctx context.Context) ([]RoleStatus, error) {
	store := runtimetypes.New(s.db.WithoutTransaction())
	chat, err := s.readAssignment(ctx, store, RoleChat)
	if err != nil {
		return nil, err
	}
	roles := make([]RoleStatus, 0, 5)
	for _, spec := range roleSpecs {
		assignment, readErr := s.readAssignment(ctx, store, spec.id)
		if readErr != nil {
			return nil, readErr
		}
		inherited := false
		assignmentState, err := readSetting(ctx, store, "inference.assignment."+spec.id)
		if err != nil {
			return nil, err
		}
		if spec.id == RoleRecovery && (assignmentState == "inherited" || assignmentState == "" && assignment.Model == "") {
			assignment.Provider, assignment.Model, inherited = chat.Provider, chat.Model, true
			assignment.Inputs = chat.Inputs
		}
		source, err := readSetting(ctx, store, sourceKey(spec.id))
		if err != nil {
			return nil, err
		}
		if source != "" && !AssignmentSource(source).Valid() {
			return nil, fmt.Errorf("role %q records unknown assignment source %q", spec.id, source)
		}
		status := RoleStatus{Assignment: assignment, Name: spec.name, State: "unconfigured", Inherited: inherited, Source: AssignmentSource(source)}
		status.Enabled, err = readRoleEnabled(ctx, store, spec.id)
		if err != nil {
			return nil, err
		}
		status.AssignmentState = "unconfigured"
		if assignment.Provider != "" && assignment.Model != "" {
			status.AssignmentState = "assigned"
		}
		if inherited {
			status.AssignmentState = "inherited"
		}
		status.Revision, err = verificationRevision(ctx, store, spec.id)
		if err != nil {
			return nil, err
		}
		connections, err := store.ListAllBackends(ctx)
		if err != nil {
			return nil, err
		}
		for _, connection := range connections {
			if runtimestate.MatchesBackend(assignment.Provider, *connection) {
				if status.ConnectionID != "" {
					status.ConnectionID = ""
					break
				}
				status.ConnectionID = connection.ID
			}
		}
		if err := assignment.Inputs.validate(spec.id); err != nil {
			status.State, status.Detail, status.Problem = "needs-attention", err.Error(), "model"
			roles = append(roles, status)
			continue
		}
		if assignment.Provider != "" && assignment.Model != "" {
			status.State = "needs-verification"
			verificationRole := spec.id
			if inherited {
				verificationRole = RoleChat
			}
			verification, verificationErr := s.readVerification(ctx, store, verificationRole)
			if verificationErr != nil {
				return nil, verificationErr
			}
			fingerprintAssignment := assignment
			fingerprintAssignment.Role = verificationRole
			fingerprint, fingerprintErr := s.configurationFingerprint(ctx, fingerprintAssignment)
			if fingerprintErr != nil {
				status.State = "needs-attention"
				status.Detail = fingerprintErr.Error()
				status.Problem = "provider"
			} else if verification != nil && verification.Fingerprint == fingerprint {
				currentRevision, err := roleRevision(ctx, store, verificationRole)
				if err != nil {
					return nil, err
				}
				if verification.RoleRevision == currentRevision {
					status.State = "available"
					status.Verification = verification
					if verification.ConfigurationRevision != "" {
						current, err := verificationRevision(ctx, store, verificationRole)
						if err != nil {
							return nil, err
						}
						if current != verification.ConfigurationRevision {
							status.State, status.Verification = "needs-verification", nil
						}
					}
				}
			}
			if capabilityErr := s.requireCapability(ctx, assignment); capabilityErr != nil {
				status.State = "needs-attention"
				status.Detail = capabilityErr.Error()
				status.Problem = "model"
				for _, backend := range s.state.Get(ctx) {
					if runtimestate.MatchesBackend(assignment.Provider, backend.Backend) && backend.Error != "" {
						status.Problem = "provider"
						status.Detail = backend.Error
						break
					}
				}
			}
		}
		roles = append(roles, status)
	}
	return roles, nil
}

// Ready requires every effective inference role that is turned on to have a verified route.
func (s *Service) Ready(ctx context.Context) (bool, error) {
	roles, err := s.Roles(ctx)
	if err != nil {
		return false, err
	}
	ready := map[string]bool{}
	for _, role := range roles {
		ready[role.Role] = role.State == "available"
	}
	for _, role := range roles {
		if _, optional := optionalRoleSpec(role.Role); optional && !role.Enabled {
			ready[role.Role] = true
		}
	}
	return ready[RoleChat] && ready[RoleRecovery] && ready[RoleAutocomplete] && ready[RoleNextEdit] && ready[RoleEmbedding], nil
}

// VerifiedRole returns a role only when its current provider, model, and configuration fingerprint match successful verification evidence.
func (s *Service) VerifiedRole(ctx context.Context, role string) (RoleStatus, error) {
	roles, err := s.Roles(ctx)
	if err != nil {
		return RoleStatus{}, err
	}
	for _, status := range roles {
		if status.Role != role {
			continue
		}
		if _, optional := optionalRoleSpec(role); optional && !status.Enabled {
			return RoleStatus{}, fmt.Errorf("%w: %w: %s is turned off; turn it on with `contenox-runtime role on %s` or `/role %s on` in the chat", ErrSetupIncomplete, ErrRoleDisabled, status.Name, role, role)
		}
		if status.State != "available" || status.Verification == nil {
			detail := strings.TrimSpace(status.Detail)
			if detail == "" {
				detail = status.State
			}
			return RoleStatus{}, fmt.Errorf("%w: %s role is %s", ErrSetupIncomplete, status.Name, detail)
		}
		return status, nil
	}
	return RoleStatus{}, fmt.Errorf("%w: unknown role %q", ErrSetupIncomplete, role)
}

// Embed executes workspace-search embedding through the currently verified embedding role.
func (s *Service) Embed(ctx context.Context, value string) ([]float32, error) {
	status, err := s.VerifiedRole(ctx, RoleEmbedding)
	if err != nil {
		return nil, err
	}
	models, closeModels, err := s.models.Models(ctx)
	if err != nil {
		return nil, err
	}
	defer closeModels()
	vector, _, err := models.Embed(ctx, llmrepo.EmbedRequest{ModelName: status.Model, ProviderType: status.Provider}, value)
	if err != nil {
		return nil, err
	}
	if err := ValidateEmbedding(vector); err != nil {
		return nil, err
	}
	if len(vector) != status.Verification.Dimension {
		return nil, fmt.Errorf("embedding dimension changed from verified %d to %d", status.Verification.Dimension, len(vector))
	}
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = float32(value)
	}
	return result, nil
}

// Assign verifies and atomically persists one explicit role assignment, recording source as its origin.
func (s *Service) Assign(ctx context.Context, assignment Assignment, source AssignmentSource) (*Verification, error) {
	verifications, err := s.AssignAll(ctx, []Assignment{assignment}, source)
	if err != nil {
		return nil, err
	}
	return &verifications[0], nil
}

// AssignAll verifies every route before atomically persisting the complete assignment set, recording source as its origin.
func (s *Service) AssignAll(ctx context.Context, assignments []Assignment, source AssignmentSource) ([]Verification, error) {
	if !source.Valid() {
		return nil, fmt.Errorf("assignment source %q is not one of %s", source, strings.Join(AssignmentSources(), ", "))
	}
	return s.assignAll(ctx, assignments, map[string]string{}, source)
}

// assignAll with an empty source re-verifies in place and leaves the recorded source untouched.
func (s *Service) assignAll(ctx context.Context, assignments []Assignment, expectedRevisions map[string]string, source AssignmentSource) ([]Verification, error) {
	return s.assignReviewed(ctx, assignments, expectedRevisions, map[string]string{}, source)
}

func (s *Service) assignReviewed(ctx context.Context, assignments []Assignment, expectedRevisions, expectedFingerprints map[string]string, source AssignmentSource) ([]Verification, error) {
	if len(assignments) == 0 {
		return nil, errors.New("at least one inference assignment is required")
	}
	verifications := make([]Verification, 0, len(assignments))
	assignments = append([]Assignment{}, assignments...)
	previous := make([]Assignment, len(assignments))
	previousRevisions := make([]string, len(assignments))
	fingerprints := make([]string, len(assignments))
	currentStore := runtimetypes.New(s.db.WithoutTransaction())
	seen := map[string]bool{}
	for index := range assignments {
		assignment := &assignments[index]
		assignment.Role = strings.TrimSpace(assignment.Role)
		assignment.Provider = strings.TrimSpace(assignment.Provider)
		assignment.Model = strings.TrimSpace(assignment.Model)
		assignment.Inputs = assignment.Inputs.normalized()
		if err := assignment.Inputs.validate(assignment.Role); err != nil {
			return nil, err
		}
		if assignment.Role != RoleChat && assignment.Role != RoleRecovery && assignment.Role != RoleAutocomplete && assignment.Role != RoleNextEdit && assignment.Role != RoleEmbedding {
			return nil, fmt.Errorf("role %q cannot be assigned explicitly", assignment.Role)
		}
		if seen[assignment.Role] {
			return nil, fmt.Errorf("role %q is assigned more than once", assignment.Role)
		}
		seen[assignment.Role] = true
		if assignment.Provider == "" || assignment.Model == "" {
			return nil, fmt.Errorf("provider and model are required for role %q", assignment.Role)
		}
		if assignment.Role == RoleRecovery {
			if _, proposed := modeldconn.ProposedWorkerOptions(ctx, assignment.Role); !proposed {
				for _, connection := range s.state.Get(ctx) {
					if runtimestate.MatchesBackend(assignment.Provider, connection.Backend) && (connection.Backend.Type == "modeld" || connection.Backend.Type == "local") {
						options, err := modelworkers.ReadAssignedOptions(ctx, currentStore, assignment.Role)
						if err != nil {
							return nil, err
						}
						ctx, err = modeldconn.WithWorkerOptions(ctx, assignment.Role, options)
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
		var err error
		previous[index], err = s.readAssignment(ctx, currentStore, assignment.Role)
		if err != nil {
			return nil, err
		}
		previousRevisions[index], err = verificationRevision(ctx, currentStore, assignment.Role)
		if err != nil {
			return nil, err
		}
		if expected, ok := expectedRevisions[assignment.Role]; ok && expected != previousRevisions[index] {
			return nil, ErrConfigurationChanged
		}
		if err := s.requireCapability(ctx, *assignment); err != nil {
			return nil, err
		}
		fingerprints[index], err = s.fingerprint(ctx, currentStore, *assignment)
		if err != nil {
			return nil, err
		}
		if expected, reviewed := expectedFingerprints[assignment.Role]; reviewed && expected != fingerprints[index] {
			return nil, ErrConfigurationChanged
		}
	}
	for index, assignment := range assignments {
		verification, err := s.verify(ctx, assignment)
		if err != nil {
			return nil, err
		}
		if verification.Fingerprint != fingerprints[index] {
			return nil, ErrConfigurationChanged
		}
		verifications = append(verifications, *verification)
	}
	tx, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := runtimetypes.BeginWrite(ctx, tx); err != nil {
		return nil, err
	}
	store := runtimetypes.New(tx)
	if err := s.validateScheduling(ctx, store, assignments, verifications); err != nil {
		return nil, err
	}
	for index, assignment := range assignments {
		current, err := s.readAssignment(ctx, store, assignment.Role)
		if err != nil {
			return nil, err
		}
		if current != previous[index] {
			return nil, ErrConfigurationChanged
		}
		currentRevision, err := verificationRevision(ctx, store, assignment.Role)
		if err != nil {
			return nil, err
		}
		if currentRevision != previousRevisions[index] {
			return nil, ErrConfigurationChanged
		}
		fingerprint, err := s.fingerprint(ctx, store, assignment)
		if err != nil {
			return nil, err
		}
		if fingerprint != verifications[index].Fingerprint {
			return nil, ErrConfigurationChanged
		}
		if options, proposed := modeldconn.ProposedWorkerOptions(ctx, assignment.Role); proposed {
			raw, err := json.Marshal(options)
			if err != nil {
				return nil, err
			}
			if err := clikv.SetString(ctx, store, "inference.modeld."+assignment.Role, string(raw)); err != nil {
				return nil, err
			}
		}
		keys := roleKeys(assignment.Role)
		if err := clikv.SetString(ctx, store, keys[0], assignment.Provider); err != nil {
			return nil, err
		}
		if err := clikv.SetString(ctx, store, keys[1], assignment.Model); err != nil {
			return nil, err
		}
		if err := clikv.SetString(ctx, store, "inference.inputs."+assignment.Role, string(assignment.Inputs)); err != nil {
			return nil, err
		}
		if err := clikv.SetString(ctx, store, "inference.assignment."+assignment.Role, "assigned"); err != nil {
			return nil, err
		}
		if source != "" {
			if err := clikv.SetString(ctx, store, sourceKey(assignment.Role), string(source)); err != nil {
				return nil, err
			}
		}
		verifications[index].RoleRevision, err = roleRevision(ctx, store, assignment.Role)
		if err != nil {
			return nil, err
		}
		verifications[index].ConfigurationRevision, err = verificationRevision(ctx, store, assignment.Role)
		if err != nil {
			return nil, err
		}
		verificationJSON, err := json.Marshal(verifications[index])
		if err != nil {
			return nil, err
		}
		if err := store.SetKV(ctx, verificationKey(assignment.Role), verificationJSON); err != nil {
			return nil, err
		}
		if assignment.Role == RoleAutocomplete {
			if err := modelworkers.SaveCompletionProtection(ctx, store, assignment.Provider, assignment.Model, verifications[index].NativeExecutions); err != nil {
				return nil, err
			}
		}
	}
	if err := commit(ctx); err != nil {
		return nil, err
	}
	for _, assignment := range assignments {
		if assignment.Role == RoleEmbedding {
			if err := s.embeddingChanged(ctx); err != nil {
				return nil, err
			}
			break
		}
	}
	return verifications, nil
}

// InheritRecovery clears an explicit recovery route so recovery follows verified Chat.
func (s *Service) InheritRecovery(ctx context.Context) error {
	tx, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	store := runtimetypes.New(tx)
	keys := roleKeys(RoleRecovery)
	if err := clikv.SetString(ctx, store, "inference.assignment."+RoleRecovery, "inherited"); err != nil {
		return err
	}
	if err := clikv.SetString(ctx, store, sourceKey(RoleRecovery), string(SourceUser)); err != nil {
		return err
	}
	if err := clikv.SetString(ctx, store, keys[0], ""); err != nil {
		return err
	}
	if err := clikv.SetString(ctx, store, keys[1], ""); err != nil {
		return err
	}
	if err := store.DeleteKV(ctx, verificationKey(RoleRecovery)); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
		return err
	}
	return commit(ctx)
}

// Verify repeats the real capability request for the current assignment and refreshes its evidence.
func (s *Service) Verify(ctx context.Context, role string) (*Verification, error) {
	role = strings.TrimSpace(role)
	store := runtimetypes.New(s.db.WithoutTransaction())
	revision, err := verificationRevision(ctx, store, role)
	if err != nil {
		return nil, err
	}
	assignment, err := s.readAssignment(ctx, store, role)
	if err != nil {
		return nil, err
	}
	if role == RoleRecovery && assignment.Model == "" {
		return s.Verify(ctx, RoleChat)
	}
	if assignment.Provider == "" || assignment.Model == "" {
		return nil, ErrSetupIncomplete
	}
	verifications, err := s.assignAll(ctx, []Assignment{assignment}, map[string]string{role: revision}, "")
	if err != nil {
		return nil, err
	}
	return &verifications[0], nil
}

// SetNextEditEnabled persists whether automatic current-file edit previews may run.
func (s *Service) SetNextEditEnabled(ctx context.Context, enabled bool) error {
	return s.SetRoleEnabled(ctx, RoleNextEdit, enabled)
}

// SetRoleEnabled persists whether an optional role is turned on. A role that is off
// unless chosen must hold verified evidence before it can be turned on.
func (s *Service) SetRoleEnabled(ctx context.Context, role string, enabled bool) error {
	spec, optional := optionalRoleSpec(role)
	if !optional {
		return fmt.Errorf("inference role %q is required and cannot be turned off; optional roles: %s", role, strings.Join(OptionalRoles(), ", "))
	}
	tx, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	store := runtimetypes.New(tx)
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	if err := clikv.SetString(ctx, store, enabledKey(role), state); err != nil {
		return err
	}
	if enabled && !spec.enabledByDefault {
		assignment, err := s.readAssignment(ctx, store, role)
		if err != nil {
			return err
		}
		if assignment.Provider == "" || assignment.Model == "" {
			return fmt.Errorf("%w: choose and verify a model for %s first", ErrSetupIncomplete, roleName(role))
		}
		verification, err := s.readVerification(ctx, store, role)
		if err != nil {
			return err
		}
		fingerprint, err := s.fingerprint(ctx, store, assignment)
		if err != nil {
			return err
		}
		revision, err := roleRevision(ctx, store, role)
		if err != nil {
			return err
		}
		configurationRevision, err := verificationRevision(ctx, store, role)
		if err != nil {
			return err
		}
		if verification == nil || verification.Fingerprint != fingerprint || verification.RoleRevision != revision || verification.ConfigurationRevision != configurationRevision {
			return fmt.Errorf("%w: assign and verify a model for %s before turning it on", ErrSetupIncomplete, roleName(role))
		}
		if err := s.validateScheduling(ctx, store, []Assignment{assignment}, []Verification{*verification}); err != nil {
			return err
		}
	}
	if err := commit(ctx); err != nil {
		return err
	}
	if role == RoleEmbedding {
		return s.embeddingChanged(ctx)
	}
	return nil
}

// InvalidateProvider removes verification evidence for every role using a provider.
func (s *Service) InvalidateProvider(ctx context.Context, provider string) error {
	return s.invalidateRoles(ctx, provider, "", []string{RoleChat, RoleRecovery, RoleAutocomplete, RoleNextEdit, RoleEmbedding})
}

// invalidateRoles removes verification evidence for each listed role using a
// provider and, when model is not empty, assigned that model.
func (s *Service) invalidateRoles(ctx context.Context, provider, model string, roles []string) error {
	provider = strings.TrimSpace(provider)
	store := runtimetypes.New(s.db.WithoutTransaction())
	backends, err := store.ListBackends(ctx, nil, runtimetypes.MAXLIMIT)
	if err != nil {
		return err
	}
	for _, role := range roles {
		assignment, err := s.readAssignment(ctx, store, role)
		if err != nil {
			return err
		}
		if model != "" && assignment.Model != model {
			continue
		}
		matches := assignment.Provider == provider
		for _, backend := range backends {
			matches = matches || runtimestate.MatchesBackend(provider, *backend) && runtimestate.MatchesBackend(assignment.Provider, *backend)
		}
		if matches {
			if err := store.DeleteKV(ctx, verificationKey(role)); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

// ProviderResult identifies the saved connection and reports an unsuccessful discovery refresh.
type ProviderResult struct {
	ID             string `json:"id"`
	DiscoveryError string `json:"discoveryError,omitempty"`
}

// ConfigureProvider saves a connection and requires its discovery refresh to finish.
func (s *Service) ConfigureProvider(ctx context.Context, request ProviderRequest) error {
	result, err := s.ConnectProvider(ctx, request)
	if err != nil {
		return err
	}
	if result.DiscoveryError != "" {
		return fmt.Errorf("provider saved; discovery failed: %s", result.DiscoveryError)
	}
	return nil
}

// ConnectProvider saves a connection and reports discovery separately from persistence.
func (s *Service) ConnectProvider(ctx context.Context, request ProviderRequest) (ProviderResult, error) {
	saved, err := s.SaveProvider(ctx, request)
	if err != nil {
		return saved, err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.state.RefreshProviderModels(refreshCtx, saved.ID); err != nil {
		saved.DiscoveryError = err.Error()
		return saved, nil
	}
	if state, exists := s.state.Get(ctx)[saved.ID]; exists {
		if state.Error != "" {
			saved.DiscoveryError = state.Error
		}
		if state.DiscoveryError != "" {
			saved.DiscoveryError = state.DiscoveryError
		}
	}
	return saved, nil
}

// SaveProvider atomically saves connection configuration and credentials without waiting for discovery.
func (s *Service) SaveProvider(ctx context.Context, request ProviderRequest) (ProviderResult, error) {
	var err error
	request, err = prepareCredentials(request)
	if err != nil {
		return ProviderResult{}, err
	}
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Provider = strings.TrimSpace(request.Provider)
	request.Endpoint = strings.TrimSpace(request.Endpoint)
	request.APIKey = strings.TrimSpace(request.APIKey)
	request.APIKeyEnv = strings.TrimSpace(request.APIKeyEnv)
	if request.Name == "" || request.Provider == "" {
		return ProviderResult{}, errors.New("provider name and type are required")
	}
	if request.APIKey != "" && request.APIKeyEnv != "" {
		return ProviderResult{}, errors.New("provide an API key or an API-key environment variable, not both")
	}
	change := backendservice.PreserveCredentials
	if request.ID == "" || request.APIKey != "" || request.APIKeyEnv != "" || request.CredentialSource != "" {
		change = backendservice.ReplaceCredentials
	}
	backend, err := backendservice.New(s.db).SaveConnection(ctx, backendservice.SaveConnectionRequest{
		ID: request.ID, Name: request.Name, Type: request.Provider, Endpoint: request.Endpoint,
		CredentialChange: change, APIKey: request.APIKey, APIKeyEnv: request.APIKeyEnv,
		CredentialSource: request.CredentialSource,
	})
	if err != nil {
		return ProviderResult{}, err
	}
	return ProviderResult{ID: backend.ID}, nil
}

// RemoveProvider deletes a connection and invalidates roles whose routes included it.
func (s *Service) RemoveProvider(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("provider id is required")
	}
	backends := backendservice.New(s.db)
	backend, err := backends.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.InvalidateProvider(ctx, runtimestate.ConnectionSelector(backend.ID)); err != nil {
		return err
	}
	if err := backends.Delete(ctx, id); err != nil {
		return err
	}
	if err := s.state.RunProviderCycle(ctx, runtimestate.ConnectionSelector(backend.ID)); err != nil {
		return fmt.Errorf("provider removed; runtime refresh failed: %w", err)
	}
	return nil
}

// RefreshProvider updates discovered capabilities without changing configuration.
func (s *Service) RefreshProvider(ctx context.Context, provider string) error {
	return s.state.RefreshProviderModels(ctx, strings.TrimSpace(provider))
}

// DeclareCompletion persists an explicit operator contract and refreshes discovery without verifying or assigning the route.
func (s *Service) DeclareCompletion(ctx context.Context, provider, model, mode string) error {
	if mode != modelregistry.CompletionInstruct && mode != modelregistry.CompletionPrompt && mode != "" {
		return fmt.Errorf("unsupported completion mode %q", mode)
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return errors.New("completion declaration requires a provider and model")
	}
	available := false
	for _, backend := range s.state.Get(ctx) {
		if !runtimestate.MatchesBackend(provider, backend.Backend) || backend.Error != "" {
			continue
		}
		for _, candidate := range backend.PulledModels {
			capable := candidate.CanChat
			if mode == modelregistry.CompletionPrompt {
				capable = candidate.CanPrompt
			}
			if (candidate.Model == model || candidate.Model == "" && candidate.Name == model) && capable {
				available = true
			}
		}
	}
	if !available {
		return errors.New("completion declaration requires a discovered model with the selected chat or prompt capability")
	}
	if _, err := modelcapability.New(runtimetypes.New(s.db.WithoutTransaction())).SetCompletion(ctx, provider, model, mode); err != nil {
		return err
	}
	if err := s.invalidateRoles(ctx, provider, model, []string{RoleAutocomplete, RoleNextEdit}); err != nil {
		return err
	}
	return s.RefreshProvider(ctx, provider)
}

// LoginProvider completes a provider-owned device login and refreshes its catalog.
func (s *Service) LoginProvider(ctx context.Context, id string, authorize func(modelauth.DeviceCode) error) error {
	if authorize == nil {
		return errors.New("device authorization presenter is required")
	}
	backend, err := backendservice.New(s.db).Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if backend.Type != modelauth.ProviderType {
		return errors.New("provider does not support device login")
	}
	if err := modelauth.New(s.db).Login(ctx, backend.ID, authorize); err != nil {
		return err
	}
	if err := s.InvalidateProvider(ctx, runtimestate.ConnectionSelector(backend.ID)); err != nil {
		return err
	}
	return s.state.RunProviderCycle(ctx, backend.ID)
}

func (s *Service) verify(ctx context.Context, assignment Assignment) (*Verification, error) {
	fingerprint, err := s.configurationFingerprint(ctx, assignment)
	if err != nil {
		return nil, err
	}
	ctx, native := modeldconn.CaptureExecution(ctx)
	models, closeModels, err := s.models.Models(ctx)
	if err != nil {
		return nil, err
	}
	defer closeModels()
	verification := &Verification{
		Inputs: assignment.Inputs.normalized(),
		Role:   assignment.Role, Provider: assignment.Provider, Model: assignment.Model,
		VerifiedAt: time.Now().UTC(),
	}
	verification.Fingerprint = fingerprint
	switch assignment.Role {
	case RoleEmbedding:
		dimension, err := VerifyRetrieval(ctx, models, assignment)
		if err != nil {
			return nil, fmt.Errorf("workspace search verification failed: %w", err)
		}
		verification.Dimension = dimension
	case RoleAutocomplete:
		completion, ok := s.models.(CompletionFactory)
		if !ok {
			return nil, errors.New("autocomplete verification requires the production completion operation")
		}
		const probePrefix, probeSuffix = "func answer() int { return ", " }"
		text, promptErr := completion.Complete(ctx, assignment, probePrefix, probeSuffix)
		if promptErr != nil {
			return nil, fmt.Errorf("autocomplete verification failed: %w", promptErr)
		}
		if err := validateInsertion(probePrefix, probeSuffix, text); err != nil {
			return nil, fmt.Errorf("autocomplete verification failed: %w", err)
		}
		verification.Sample = sample(text)
	case RoleNextEdit:
		nextEdit, ok := s.models.(NextEditFactory)
		if !ok {
			return nil, errors.New("next edit verification requires the production operation")
		}
		before := "package verification\n\nfunc sum(left, right int) int {\n    return left\n}\n"
		prefix := "package verification\n\nfunc sum(left, right int) int {\n    return left + "
		current := prefix + "\n}\n"
		input := modelcontract.NextEditInput{Path: "answer.go", Before: before, Current: current, Cursor: len(prefix), Start: len(prefix), End: len(prefix)}
		edits, err := nextEdit.NextEdit(ctx, assignment, input)
		if err != nil {
			return nil, fmt.Errorf("next edit verification failed: %w", err)
		}
		if len(edits) == 0 {
			return nil, errors.New("next edit verification failed: model returned no edits")
		}
		if err := modelcontract.ValidateEdits(input, edits); err != nil {
			return nil, fmt.Errorf("next edit verification failed: %w", err)
		}
		updated := current
		for index := len(edits) - 1; index >= 0; index-- {
			edit := edits[index]
			updated = updated[:edit.Start] + edit.NewText + updated[edit.End:]
		}
		if !strings.Contains(updated, "return left + right") {
			return nil, errors.New("next edit verification failed: expected an edit that completes the changed expression")
		}
		if _, err := parser.ParseFile(token.NewFileSet(), input.Path, updated, parser.AllErrors); err != nil {
			return nil, fmt.Errorf("next edit verification failed: edited file is invalid Go code: %w", err)
		}
	case RoleChat, RoleRecovery:
		chat := func(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
			result, _, err := models.Chat(ctx, llmrepo.Request{WorkerRole: assignment.Role, ModelNames: []string{assignment.Model}, ProviderTypes: []string{assignment.Provider}}, messages, args...)
			return result, err
		}
		if err := VerifyToolUse(ctx, chat); err != nil {
			return nil, fmt.Errorf("chat verification failed: %w", err)
		}
		if err := verifyInputs(ctx, assignment.Inputs, chat); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown inference role %q", assignment.Role)
	}
	verification.NativeExecutions = native.Reports()
	for _, connection := range s.state.Get(ctx) {
		if runtimestate.MatchesBackend(assignment.Provider, connection.Backend) && (connection.Backend.Type == "modeld" || connection.Backend.Type == "local") && len(verification.NativeExecutions) == 0 {
			return nil, errors.New("native verification returned no execution evidence; update and restart local inference")
		}
	}
	return verification, nil
}

// requireCapability refuses an assignment whose connection does not serve the model or whose model cannot serve the role, before any fingerprint or verification request is made.
func (s *Service) requireCapability(ctx context.Context, assignment Assignment) error {
	matched, native := false, false
	unavailable := ""
	for _, backend := range s.state.Get(ctx) {
		if !runtimestate.MatchesBackend(assignment.Provider, backend.Backend) {
			continue
		}
		matched = true
		native = native || backend.Backend.Type == "modeld" || backend.Backend.Type == "local"
		if backend.Error != "" {
			unavailable = backend.Error
			continue
		}
		for _, candidate := range backend.PulledModels {
			name := candidate.Model
			if name == "" {
				name = candidate.Name
			}
			if name != assignment.Model {
				continue
			}
			switch assignment.Role {
			case RoleEmbedding:
				if !candidate.CanEmbed {
					return errors.New("selected model does not advertise embedding capability")
				}
			case RoleAutocomplete:
				if _, err := s.CompletionMode(ctx, assignment); err != nil {
					return err
				}
			case RoleNextEdit:
				if !candidate.CanNextEdit {
					return errors.New("selected model does not advertise independent next-edit capability")
				}
				if _, err := modelcontract.ResolveNextEdit(candidate.NextEditFormat); err != nil {
					return err
				}
			case RoleChat, RoleRecovery:
				if !candidate.CanChat {
					return errors.New("selected model does not advertise chat capability")
				}
				if assignment.Inputs.images() && !candidate.CanVision {
					return errors.New("selected connection and model do not advertise image input")
				}
				if assignment.Inputs.audio() && !candidate.CanAudio {
					return errors.New("selected connection and model do not support the audio-input contract")
				}
			}
			return nil
		}
	}
	switch {
	case !matched:
		return &ConnectionUnavailableError{Connection: assignment.Provider, Reason: "no connection of that name has been discovered"}
	case unavailable != "":
		return &ConnectionUnavailableError{Connection: assignment.Provider, Reason: unavailable}
	}
	curated := false
	if native {
		descriptor, err := modelregistry.NewCurated().Resolve(ctx, assignment.Model)
		if err != nil && !errors.Is(err, modelregistry.ErrNotFound) {
			return err
		}
		curated = err == nil && descriptor != nil && descriptor.Name == assignment.Model
	}
	return &ModelNotDiscoveredError{Connection: assignment.Provider, Model: assignment.Model, Curated: curated}
}

func (s *Service) readAssignment(ctx context.Context, store runtimetypes.Store, role string) (Assignment, error) {
	keys := roleKeys(role)
	provider, err := readSetting(ctx, store, keys[0])
	if err != nil {
		return Assignment{}, err
	}
	model, err := readSetting(ctx, store, keys[1])
	if err != nil {
		return Assignment{}, err
	}
	inputs, err := readSetting(ctx, store, "inference.inputs."+role)
	if err != nil {
		return Assignment{}, err
	}
	requirement := InputRequirement(inputs).normalized()
	return Assignment{Role: role, Provider: provider, Model: model, Inputs: requirement}, nil
}

func (s *Service) readVerification(ctx context.Context, store runtimetypes.Store, role string) (*Verification, error) {
	var verification Verification
	if err := store.GetKV(ctx, verificationKey(role), &verification); err != nil {
		if errors.Is(err, libdbexec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &verification, nil
}

func roleKeys(role string) [2]string {
	switch role {
	case RoleChat:
		return [2]string{"default-provider", "default-model"}
	case RoleRecovery:
		return [2]string{"default-alt-provider", "default-alt-model"}
	case RoleAutocomplete:
		return [2]string{"default-autocomplete-provider", "default-autocomplete-model"}
	case RoleNextEdit:
		return [2]string{"default-next-edit-provider", "default-next-edit-model"}
	case RoleEmbedding:
		return [2]string{"default-embedding-provider", "default-embedding-model"}
	default:
		return [2]string{"invalid-provider", "invalid-model"}
	}
}

// validateInsertion rejects provider output that cannot be an editor insertion.
// The probe stays language-neutral deliberately: this service cannot see the
// workspace language, and parsing one language rejected correct insertions for
// every other one. What remains is verifiable without knowing the language —
// envelope leakage, control-token leakage and an echo of the surrounding source.
func validateInsertion(prefix, suffix, text string) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return errors.New("provider returned an empty completion")
	}
	if strings.Contains(text, "```") {
		return errors.New("provider returned a markdown fence instead of an insertion")
	}
	if strings.Contains(text, "<|") && strings.Contains(text, "|>") || strings.Contains(text, "<fim_") || strings.Contains(text, "</s>") {
		return errors.New("provider leaked a fill-in-the-middle control token into the insertion")
	}
	if trimmed == strings.TrimSpace(prefix) || suffix != "" && trimmed == strings.TrimSpace(suffix) {
		return errors.New("provider echoed the surrounding source instead of completing it")
	}
	return nil
}

// sample retains the observed insertion for review without echoing unbounded provider output.
func sample(text string) string {
	const limit = 200
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + " ..."
}

func roleName(role string) string {
	for _, spec := range roleSpecs {
		if spec.id == role {
			return spec.name
		}
	}
	return role
}

func readSetting(ctx context.Context, store runtimetypes.Store, key string) (string, error) {
	if strings.HasPrefix(key, "invalid-") {
		return "", errors.New("unknown inference role")
	}
	var value string
	err := store.GetKV(ctx, clikv.Prefix+key, &value)
	if errors.Is(err, libdbexec.ErrNotFound) {
		return "", nil
	}
	return strings.TrimSpace(value), err
}

func verificationKey(role string) string {
	return "inference.setup.verification." + role
}

func (s *Service) configurationFingerprint(ctx context.Context, assignment Assignment) (string, error) {
	return s.fingerprint(ctx, runtimetypes.New(s.db.WithoutTransaction()), assignment)
}

// ConfigurationFingerprint returns the fingerprint of the production route and execution contract for an assignment.
func (s *Service) ConfigurationFingerprint(ctx context.Context, assignment Assignment) (string, error) {
	return s.configurationFingerprint(ctx, assignment)
}

// fingerprint covers what a role's requests use: a model's completion contract
// counts for autocomplete and next edit alone, so declaring one never
// unverifies a chat, recovery or embedding role on the same model.
func (s *Service) fingerprint(ctx context.Context, store runtimetypes.Store, assignment Assignment) (string, error) {
	completionRole := assignment.Role == RoleAutocomplete || assignment.Role == RoleNextEdit
	parts := []string{assignment.Provider, assignment.Model, assignment.Role, "production-role-verification-v2"}
	if assignment.Role == RoleChat || assignment.Role == RoleRecovery {
		parts = append(parts, "tool-result-verification-v1")
		parts = append(parts, "input-requirement="+string(assignment.Inputs.normalized()))
	}
	connections, err := store.ListAllBackends(ctx)
	if err != nil {
		return "", err
	}
	var selected *runtimetypes.Backend
	for _, backend := range connections {
		if runtimestate.MatchesBackend(assignment.Provider, *backend) {
			if selected != nil {
				return "", errors.New("inference route matches multiple connections; name one connection")
			}
			selected = backend
		}
	}
	if selected == nil {
		return "", errors.New("inference connection is unavailable")
	}
	connectionVersion, err := connectionRevision(ctx, store, selected)
	if err != nil {
		return "", err
	}
	facts, err := store.GetLLMProviderModelFacts(ctx, selected.ID)
	if err != nil {
		return "", err
	}
	completionMode, fimFormat, nextEditFormat := "", "", ""
	if fact := facts[assignment.Model]; !fact.IsZero() {
		verified := fact
		if !completionRole {
			verified.CompletionMode, verified.FIMFormat, verified.NextEditFormat = "", "", ""
		}
		if !verified.IsZero() {
			contract, err := json.Marshal(verified)
			if err != nil {
				return "", err
			}
			parts = append(parts, "declared-contract="+string(contract))
		}
		completionMode, fimFormat = fact.CompletionMode, fact.FIMFormat
		nextEditFormat = fact.NextEditFormat
	}
	local, foundModel := false, false
	for _, backend := range s.state.Get(ctx) {
		if !runtimestate.MatchesBackend(assignment.Provider, backend.Backend) {
			continue
		}
		if backend.Backend.ID != selected.ID || backend.Backend.BaseURL != selected.BaseURL || backend.Backend.Type != selected.Type {
			return "", ErrConfigurationChanged
		}
		local = local || backend.Backend.Type == "modeld" || backend.Backend.Type == "local"
		for _, candidate := range backend.PulledModels {
			name := candidate.Model
			if name == "" {
				name = candidate.Name
			}
			if name == assignment.Model {
				foundModel = true
				if candidate.NextEditFormat != "" {
					nextEditFormat = candidate.NextEditFormat
				}
				if candidate.CompletionMode != "" {
					completionMode, fimFormat = candidate.CompletionMode, candidate.FIMFormat
				}
				mode, format, autocomplete := candidate.CompletionMode, candidate.FIMFormat, candidate.CanAutocomplete
				if !completionRole {
					mode, format, autocomplete = "", "", false
				}
				parts = append(parts, backend.Backend.ID+"\n"+backend.Backend.BaseURL+"\n"+candidate.Digest+"\n"+mode+"\n"+format+fmt.Sprintf("\nchat=%t\nprompt=%t\nautocomplete=%t", candidate.CanChat, candidate.CanPrompt, autocomplete))
				if candidate.NativeBackend != "" {
					parts = append(parts, "native-backend="+candidate.NativeBackend)
				}
			}
		}
	}
	if !foundModel {
		return "", errors.New("cannot fingerprint an unavailable inference route")
	}
	parts = append(parts, "connection-revision="+connectionVersion)
	if assignment.Role == RoleAutocomplete {
		completion, ok := s.models.(CompletionFactory)
		if !ok {
			return "", errors.New("autocomplete requires the configured completion operation")
		}
		switch completionMode {
		case modelregistry.CompletionFIM:
			fingerprint, err := completion.ExecutionFingerprint(ctx, completionMode, fimFormat)
			if err != nil {
				return "", err
			}
			if fingerprint == "" {
				return "", errors.New("native completion contract has no fingerprint")
			}
			parts = append(parts, "completion-execution="+fingerprint)
		case modelregistry.CompletionInstruct:
			chain, err := completion.CompletionFingerprint(ctx)
			if err != nil {
				return "", err
			}
			if chain == "" {
				return "", errors.New("autocomplete completion contract has no fingerprint")
			}
			parts = append(parts, "completion-chain="+chain)
			think, err := readSetting(ctx, store, "default-autocomplete-think")
			if err != nil {
				return "", err
			}
			parts = append(parts, "think="+think)
		case modelregistry.CompletionPrompt:
			fingerprint, err := completion.ExecutionFingerprint(ctx, completionMode, "")
			if err != nil {
				return "", err
			}
			if fingerprint == "" {
				return "", errors.New("prompt completion contract has no fingerprint")
			}
			parts = append(parts, "completion-execution="+fingerprint)
		default:
			return "", errors.New("autocomplete model has no completion execution contract")
		}
	}
	if assignment.Role == RoleNextEdit {
		factory, ok := s.models.(NextEditFactory)
		if !ok {
			return "", errors.New("next edit requires its production operation")
		}
		fingerprint, err := factory.NextEditExecutionFingerprint(ctx, nextEditFormat)
		if err != nil {
			return "", err
		}
		if fingerprint == "" {
			return "", errors.New("next edit contract has no fingerprint")
		}
		parts = append(parts, "next-edit-execution="+fingerprint)
	}
	options, proposed := modeldconn.ProposedWorkerOptions(ctx, assignment.Role)
	if proposed && !local {
		return "", errors.New("device and resource settings require a modeld connection")
	}
	if local {
		parts = append(parts, "native-execution-evidence-v1")
		if assignment.Role == RoleEmbedding {
			descriptor, err := modelregistry.NewCurated().Resolve(ctx, assignment.Model)
			if err != nil && !errors.Is(err, modelregistry.ErrNotFound) {
				return "", err
			}
			if err == nil {
				contract, err := json.Marshal(descriptor.Embedding)
				if err != nil {
					return "", err
				}
				parts = append(parts, "native-retrieval-contract="+string(contract))
			}
		}
		settings, err := readSetting(ctx, store, "inference.modeld."+assignment.Role)
		if err != nil {
			return "", err
		}
		if proposed {
			raw, err := json.Marshal(options)
			if err != nil {
				return "", err
			}
			settings = string(raw)
		}
		if settings != "" {
			parts = append(parts, "local-settings="+settings)
		}
	}
	sort.Strings(parts[4:])
	raw, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateEmbedding rejects vectors that cannot represent a searchable document or query.
func ValidateEmbedding(vector []float64) error {
	if len(vector) == 0 {
		return errors.New("provider returned an empty vector")
	}
	var norm float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("provider returned a non-finite embedding")
		}
		norm += value * value
	}
	if norm == 0 || math.IsInf(norm, 0) {
		return errors.New("provider returned an invalid embedding norm")
	}
	return nil
}

// VerifyRetrieval exercises document and query encoding and requires the matching passage to outrank unrelated text.
func VerifyRetrieval(ctx context.Context, models llmrepo.ModelRepo, assignment Assignment) (int, error) {
	if models == nil || assignment.Provider == "" || assignment.Model == "" {
		return 0, errors.New("retrieval verification requires a complete model route")
	}
	request := llmrepo.EmbedRequest{ModelName: assignment.Model, ProviderType: assignment.Provider}
	inputs := []struct {
		purpose modelrepo.EmbeddingPurpose
		text    string
	}{
		{modelrepo.EmbeddingDocument, "Contenox saves provider and model configuration in a local SQLite database so AI settings survive an application restart."},
		{modelrepo.EmbeddingDocument, "Banana bread is baked with ripe bananas, flour, butter and sugar."},
		{modelrepo.EmbeddingQuery, "Where does Contenox persist AI provider and model settings across restarts?"},
	}
	vectors := make([][]float64, 0, len(inputs))
	for _, input := range inputs {
		vector, _, err := models.Embed(modelrepo.WithEmbeddingPurpose(ctx, input.purpose), request, input.text)
		if err != nil {
			return 0, err
		}
		if err := ValidateEmbedding(vector); err != nil {
			return 0, err
		}
		if len(vectors) > 0 && len(vector) != len(vectors[0]) {
			return 0, errors.New("document and query embedding dimensions differ")
		}
		vectors = append(vectors, vector)
	}
	if cosineSimilarity(vectors[2], vectors[0]) <= cosineSimilarity(vectors[2], vectors[1]) {
		return 0, errors.New("embedding route failed retrieval: matching configuration passage did not outrank unrelated text")
	}
	return len(vectors[0]), nil
}

func cosineSimilarity(a, b []float64) float64 {
	var dot, aa, bb float64
	for index, value := range a {
		dot += value * b[index]
		aa += value * value
		bb += b[index] * b[index]
	}
	return dot / math.Sqrt(aa) / math.Sqrt(bb)
}
