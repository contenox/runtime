// Package autosetup selects, installs, verifies, and configures native inference.
package autosetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeldinstall"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/modeldprobe"
	"github.com/contenox/runtime/runtime/modelhardware"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo"
	native "github.com/contenox/runtime/runtime/modelrepo/modeld"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/disk"
)

// Contract describes independent inference recommendations and their application.
var Contract = servicecontract.Contract{
	ID:          "automatic-local-setup",
	Summary:     "Review and configure independent AI roles.",
	Description: "Choose local, remote or cloud routes for each role. Review connections, models, devices, settings and required downloads, then verify the selected group before saving it atomically.",
	Operations: []servicecontract.Operation{
		{
			ID: "plan", Summary: "Inspect the recommended local AI setup without changing the machine.", Description: "Reports the recommended roles, the artifacts they need and the repairs for what cannot be satisfied. Accepts an optional origin and refresh.",
			Spec: `Select a current curated, permissively licensed model that is curated as able
to run a multi-step tool loop, holding at least 32768 tokens of usable context
in this machine's memory. Capability gates the choice and context qualifies it:
a larger window on a model that cannot use it is not a capability. Selection
uses available memory, the model's native context ceiling, and the KV cache type
the worker is configured with (CONTENOX_LLAMA_KV_CACHE_TYPE; q8_0 halves what a
window costs in device memory). Cold/planner capacity does not count.
Origin restricts the model developer's region. Downloads use Hugging Face by
default (HF_ENDPOINT overrides it; HF_TOKEN optionally authenticates).

The worker and model are installed, a harmless tool call is verified, and only
then are native defaults saved. Subsequent runs reuse the configured eligible
model; --refresh selects again. Setup uses native inference.`,
			Output: servicecontract.Output{
				Stdout: "The machine's summary, the selected model with its download size, and the estimated hot context.",
				Format: "text, or the plan as JSON when --dry-run is given",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "a plan is produced", Meaning: "nothing was installed and no default changed"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "no curated model fits this machine's selection budget", Message: "names the budget, the KV cache type and the smallest model that would fit"},
			},
		},
		{
			ID: "apply", Summary: "Set up local AI", Description: "Acquire the recommended artifacts and verify real chat, completion and retrieval operations before saving all required role assignments.",
			Spec:   "Acquire the recommended artifacts and verify real chat, completion and retrieval operations before saving all required role assignments.",
			Inputs: []servicecontract.Input{{ID: "plan", Summary: "The reviewed plan whose roles are acquired, verified and saved", Type: "object", Required: true}},
			Output: servicecontract.Output{
				Stdout: "Acquisition progress, then the verified roles that were saved.",
				Stderr: "Progress narration for a long download.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "every required role is acquired and verified", Meaning: "the assignments are saved together"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "an artifact cannot be acquired", Message: "names the artifact and the failure; nothing is saved"},
				{When: "a role fails verification", Message: "names the role and the operation that failed; the current configuration is preserved"},
			},
		},
		{
			ID: "recommend", Summary: "Recommend roles independently across allowed connections.",
			Spec:   "Recommend a route for every role independently, across the connections the constraints allow, without changing any assignment.",
			Inputs: []servicecontract.Input{{ID: "recommendation", Summary: "Mode, origin and per-role connection constraints the recommendation must preserve", Type: "object", Required: true}},
			Output: servicecontract.Output{
				Stdout: "One recommendation per role with the connection it selects.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "every role has a recommendation", Meaning: "nothing was assigned"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "no allowed connection can serve a role", Message: "names the role and the constraint that excluded every connection"},
			},
		},
		{
			ID: "choose", Summary: "Select an alternative and recheck the group's resource and scheduling limits.",
			Spec: "Select an alternative route for one role and recheck the group's resource and scheduling limits, so a choice that cannot run with the others is refused before it is applied.",
			Inputs: []servicecontract.Input{
				{ID: "plan", Summary: "The reviewed plan the alternative is chosen from", Type: "object", Required: true},
				{ID: "role", Summary: "Role whose alternative is selected", Type: "string", Required: true, Values: inferencesetup.RoleIDs()},
				{ID: "alternative", Summary: "Index of the selected alternative", Type: "number"},
			},
			Output: servicecontract.Output{
				Stdout: "The selected alternative and the rechecked group.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the alternative fits the group's limits", Meaning: "the plan now carries the chosen route"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the alternative conflicts with another role's resources", Message: "names both roles and the resource they contend for"},
				{When: "the alternative index does not exist", Message: "names the range the plan offers"},
			},
		},
		{
			ID: "apply-recommended", Summary: "Acquire and verify the selected roles before committing them together.",
			Spec:   "Acquire and verify the selected roles before committing them together, so a partially available group is never saved as a configured one.",
			Inputs: []servicecontract.Input{{ID: "plan", Summary: "The reviewed plan whose selected roles are acquired and verified", Type: "object", Required: true}},
			Output: servicecontract.Output{
				Stdout: "Progress, then the roles that were verified and saved.",
				Stderr: "Progress narration for a long download.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "every selected role verifies", Meaning: "the assignments are saved together"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "a selected role fails verification", Message: "names the role and preserves the current configuration"},
			},
		},
		{
			ID: "prepare-local", Summary: "Install a native backend and prepare its local connection.",
			Spec: `Run the setup wizard to pick an LLM provider (native modeld, Ollama local or Cloud,
OpenAI, Anthropic, Gemini, Vertex AI, AWS Bedrock, or self-hosted vLLM), enter
credentials, and set defaults. This is the same wizard that runs inside IDE
terminals via ACP.

Examples:
  contenox-runtime setup`,
			Inputs: []servicecontract.Input{{ID: "nativePrerequisite", Summary: "The explicitly selected native backend, whose backend field is llama or openvino", Type: "object", Required: true}},
			Examples: []servicecontract.Example{
				{Command: "contenox-runtime setup", Explanation: "The interactive journey."},
				{Command: "contenox-runtime auto", Explanation: "The same installation driven from the machine's own plan."},
			},
			Output: servicecontract.Output{
				Stdout: "The wizard's questions, then the defaults it saved.",
				Stderr: "The refusal when the run is not interactive.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the wizard completes or the operator quits", Meaning: "a quit keeps the current configuration"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "no input arrives because the run is not interactive", Message: "says the wizard is interactive, names errSetupNoInput, and points at 'contenox-runtime config set' for a non-interactive run"},
				{When: "a store the settings name cannot be used", Message: "names the store and refuses before offering a wizard whose answers cannot be saved"},
			},
		},
		{
			ID: "devices", Summary: "List the devices and memory pools local inference can use.",
			Description: "Read, for every local connection, the devices it reports, the memory pool each draws on, the pool's size and free memory, and the ceiling its owner enforces.",
			Spec:        "List what local inference can use on this computer: for each local connection its devices grouped by the memory pool they draw on, each pool's total and free memory, and the ceiling the connection's owner enforces on it. A connection that cannot be read is listed with the reason. The pool names are the ones the ceiling operation accepts.",
			Output: servicecontract.Output{
				Stdout: "Per connection: its pools with size, free memory and ceiling, and the devices in each pool.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the connections are read", Meaning: "each listed pool can be bounded with a ceiling"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "no local connection is saved", Message: "names the step that prepares local inference"},
			},
		},
		{
			ID: "configure-ceiling", Summary: "Bound what local inference may hold on one memory pool.",
			Description: "Set the share of one memory pool's total bytes a native connection's owner lets local inference hold. Roles already placed on the pool are checked and reported, never required to be verified first.",
			Spec:        "Set the device ceiling of one memory pool on a native connection's owner, as a share of the pool's total bytes greater than 0 and at most 1. The owner enforces the new limit when it next admits a model. Lowering a ceiling never requires the connection's roles to be verified: the result names the roles whose verified placements together hold more than the new limit and the roles whose placement is not verified, so their fit is unknown, and each is repaired by choosing its model again.",
			Inputs: []servicecontract.Input{
				{ID: "id", Summary: "The native connection whose owner enforces the ceiling", Type: "string", Required: true},
				{ID: "poolId", Summary: "The memory pool the ceiling bounds, as the connection's device list names it", Type: "string", Required: true},
				{ID: "share", Summary: "The share of the pool's total bytes local inference may hold, greater than 0 and at most 1", Type: "number", Required: true},
			},
			Output: servicecontract.Output{
				Stdout: "The ceilings the connection's owner enforces after the change, and the roles that no longer fit or cannot be checked.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the owner accepts the limit", Meaning: "the owner enforces the new ceiling; roles listed as not fitting are repaired by choosing a smaller model for them"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "models loaded on the pool already hold more than the ceiling allows", Message: "names the pool and the limit; the previous ceiling stays in force"},
				{When: "the connection is not native or reports no such pool", Message: "names the connection or the pool"},
			},
		},
	},
}

// Options controls native setup selection and artifact acquisition.
type Options struct {
	Origin        string
	Backend       string
	KVCacheType   string
	DataRoot      string
	WorkerBaseURL string
	Refresh       bool
	Progress      io.Writer
	Report        func(Progress) error
}

// Progress reports one automatic setup phase and its optional byte position.
type Progress struct {
	Phase   string
	Message string
	Current int64
	Total   int64
}

// Plan is the automatic native setup selected for one machine. Ceilings are the
// device ceilings a role recommendation was planned under.
type Plan struct {
	Roles               []RolePlan                      `json:"roles,omitempty"`
	Mode                string                          `json:"mode,omitempty"`
	Baseline            []PoolEstimate                  `json:"baseline,omitempty"`
	Models              []modelregistry.ModelDescriptor `json:"models"`
	Hardware            modelhardware.Hardware          `json:"hardware"`
	Model               modelregistry.ModelDescriptor   `json:"model"`
	Backend             string                          `json:"backend"`
	Reused              bool                            `json:"reused"`
	EstimatedHotContext int                             `json:"estimatedHotContext"`
	Fingerprint         string                          `json:"fingerprint"`
	Assignments         []PlannedRole                   `json:"assignments"`
	Ceilings            []PoolCeiling                   `json:"ceilings,omitempty"`
}

// PlannedRole is one required IDE capability served by the reviewed automatic plan.
type PlannedRole struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Result is a verified native setup.
type Result struct {
	Plan       Plan `json:"plan"`
	HotContext int  `json:"hotContext"`
}

// Service owns automatic native setup independently of a presentation surface.
type Service struct {
	db      libdb.DBManager
	tracker libtracker.ActivityTracker
}

// New constructs automatic setup over the persistent runtime database.
func New(db libdb.DBManager, tracker libtracker.ActivityTracker) (*Service, error) {
	if db == nil || tracker == nil {
		return nil, errors.New("automatic setup requires a database and activity tracker")
	}
	return &Service{db: db, tracker: tracker}, nil
}

// NewPlanner constructs read-only automatic selection without persistent reuse.
func NewPlanner(tracker libtracker.ActivityTracker) (*Service, error) {
	if tracker == nil {
		return nil, errors.New("automatic setup planner requires an activity tracker")
	}
	return &Service{tracker: tracker}, nil
}

// Plan selects a compatible curated model without changing the machine.
func (s *Service) Plan(ctx context.Context, opts Options) (Plan, error) {
	origin := strings.ToLower(strings.TrimSpace(opts.Origin))
	if !slices.Contains([]string{"", "us", "eu", "china", "gus"}, origin) {
		return Plan{}, fmt.Errorf("unknown origin %q: use us, eu, china or gus", origin)
	}
	hardware := modelhardware.Detect(ctx)
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	root := dataRoot(opts.DataRoot)
	backend := planBackend(opts.Backend, hardware.PreferredBackend, modeldprobe.New(root).Probe(ctx))
	candidates, err := modelregistry.NewCurated().List(ctx)
	if err != nil {
		return Plan{}, err
	}
	embedding, err := modelregistry.SelectRole(candidates, "embedding", origin, backend, hardware.MemoryBytes, opts.KVCacheType)
	if err != nil {
		return Plan{}, err
	}
	remaining := hardware.MemoryBytes - embedding.RoleMemoryBytes("embedding", opts.KVCacheType)
	completion, err := modelregistry.SelectRole(candidates, "autocomplete", origin, backend, remaining, opts.KVCacheType)
	if err != nil {
		return Plan{}, err
	}
	remaining -= completion.RoleMemoryBytes("autocomplete", opts.KVCacheType)
	selected, reused := modelregistry.ModelDescriptor{}, false
	if !opts.Refresh {
		var err error
		selected, reused, err = s.Previous(ctx, origin, backend, remaining, opts.KVCacheType)
		if err != nil {
			return Plan{}, err
		}
	}
	if !reused {
		var err error
		selected, err = modelregistry.SelectAutoType(origin, backend, remaining, opts.KVCacheType)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", hardware.Summary, err)
		}
	}
	plan := Plan{
		Models:   []modelregistry.ModelDescriptor{selected, completion, embedding},
		Hardware: hardware, Model: selected, Backend: backend, Reused: reused,
		EstimatedHotContext: selected.AutoHotContextType(remaining, opts.KVCacheType),
		Assignments: []PlannedRole{
			{Role: "primary", Provider: "modeld", Model: selected.Name},
			{Role: "alternative", Provider: "modeld", Model: selected.Name},
			{Role: "autocomplete", Provider: "modeld", Model: completion.Name},
			{Role: "embedding", Provider: "modeld", Model: embedding.Name},
		},
	}
	unique := make([]modelregistry.ModelDescriptor, 0, len(plan.Models))
	seen := map[string]bool{}
	for _, model := range plan.Models {
		if !seen[model.Name] {
			unique = append(unique, model)
			seen[model.Name] = true
		}
	}
	plan.Models = unique
	plan.Fingerprint, err = planFingerprint(plan)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// A standalone owner can only serve its active backend. A shared owner can
// launch either backend, so selection uses the configured or detected backend.
func planBackend(configured, detected string, owner modeldprobe.Status) string {
	if owner.State == modeldprobe.StateRunning && (owner.Backend == "llama" || owner.Backend == "openvino") {
		return owner.Backend
	}
	if backend := strings.TrimSpace(configured); backend != "" {
		return backend
	}
	return detected
}

// Apply installs and verifies a plan before saving it as the native default.
func (s *Service) Apply(ctx context.Context, plan Plan, opts Options) (Result, error) {
	if s.db == nil {
		return Result{}, errors.New("automatic setup apply requires a persistent database")
	}
	if opts.Progress == nil || opts.Report == nil {
		return Result{}, errors.New("automatic setup apply requires progress output and reporting")
	}
	fingerprint, err := planFingerprint(plan)
	if err != nil {
		return Result{}, err
	}
	if plan.Fingerprint == "" || plan.Fingerprint != fingerprint {
		return Result{}, errors.New("automatic setup plan is missing or has changed; review a new plan")
	}
	if err := validateModels(plan); err != nil {
		return Result{}, err
	}
	root := dataRoot(opts.DataRoot)
	if err := report(opts, Progress{Phase: "storage", Message: "Checking local storage"}); err != nil {
		return Result{}, err
	}
	if err := checkDiskModels(root, plan.Models); err != nil {
		return Result{}, err
	}
	if err := report(opts, Progress{Phase: "engine", Message: "Preparing the local model engine"}); err != nil {
		return Result{}, err
	}
	if err := ensureWorker(ctx, root, plan.Backend, opts.WorkerBaseURL, opts.Progress); err != nil {
		return Result{}, err
	}
	for _, model := range plan.Models {
		if err := report(opts, Progress{Phase: "download", Message: "Preparing " + model.Label(), Total: model.SizeBytes + model.MMProjSizeBytes}); err != nil {
			return Result{}, err
		}
		if err := modeldconn.AcquireModel(ctx, modelregistry.NewCurated(), model.Name, modelstore.AutoPullOptions{
			DataRoot: root, ProgressOut: opts.Progress, Tracker: s.tracker,
			Progress: func(value modelstore.DownloadProgress) error {
				return report(opts, Progress{Phase: "download", Message: "Downloading " + model.Label(), Current: value.Downloaded, Total: value.Total})
			},
		}); err != nil {
			return Result{}, err
		}
	}
	if err := report(opts, Progress{Phase: "verify", Message: "Verifying local inference and tool use"}); err != nil {
		return Result{}, err
	}
	ctx = modeldconn.WithDataRoot(ctx, root)
	ctx, err = modeldconn.WithWorkerOptions(ctx, inferencesetup.RoleChat, modeldconn.WorkerOptions{Backend: plan.Backend, KVCacheType: opts.KVCacheType})
	if err != nil {
		return Result{}, err
	}
	hot, err := Verify(ctx, plan.Model, plan.EstimatedHotContext, opts.KVCacheType, opts.Progress)
	if err != nil {
		return Result{}, fmt.Errorf("native verification failed; defaults were not changed: %w", err)
	}
	if err := report(opts, Progress{Phase: "configure", Message: "Registering local AI provider"}); err != nil {
		return Result{}, err
	}
	if _, err := EnsureLocalConnection(ctx, s.db); err != nil {
		return Result{}, err
	}
	return Result{Plan: plan, HotContext: hot}, nil
}

// ApplyAll acquires the reviewed artifacts and verifies every production role before atomically persisting assignments.
func (s *Service) ApplyAll(ctx context.Context, plan Plan, opts Options, roles *inferencesetup.Service) (Result, error) {
	if roles == nil {
		return Result{}, errors.New("automatic setup requires the inference-role service")
	}
	result, err := s.Apply(ctx, plan, opts)
	if err != nil {
		return Result{}, err
	}
	selector, err := EnsureLocalConnection(ctx, s.db)
	if err != nil {
		return Result{}, err
	}
	if err := roles.RefreshProvider(ctx, selector); err != nil {
		return Result{}, err
	}
	assignments := make([]inferencesetup.Assignment, 0, len(plan.Assignments))
	for _, assignment := range plan.Assignments {
		ctx, err = modeldconn.WithWorkerOptions(ctx, assignment.Role, modeldconn.WorkerOptions{Backend: plan.Backend, KVCacheType: opts.KVCacheType})
		if err != nil {
			return Result{}, err
		}
		assignments = append(assignments, inferencesetup.Assignment{Role: assignment.Role, Provider: selector, Model: assignment.Model})
	}
	if _, err := roles.AssignAll(ctx, assignments, inferencesetup.SourcePlanner); err != nil {
		return Result{}, fmt.Errorf("role verification failed; defaults were not changed: %w", err)
	}
	return result, nil
}

// Fingerprint identifies a plan by its content. A nil collection and an empty
// one fingerprint identically, because a plan echoed back through a wire that
// cannot tell them apart must still match the plan the planner answered.
func Fingerprint(plan Plan) (string, error) {
	plan.Fingerprint = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	var canonical Plan
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return "", err
	}
	emptyNilCollections(reflect.ValueOf(&canonical).Elem())
	raw, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func planFingerprint(plan Plan) (string, error) {
	return Fingerprint(plan)
}

func emptyNilCollections(value reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			emptyNilCollections(value.Elem())
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if field := value.Field(i); field.CanSet() {
				emptyNilCollections(field)
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			if value.CanSet() {
				value.Set(reflect.MakeSlice(value.Type(), 0, 0))
			}
			return
		}
		for i := 0; i < value.Len(); i++ {
			emptyNilCollections(value.Index(i))
		}
	case reflect.Map:
		if value.IsNil() && value.CanSet() {
			value.Set(reflect.MakeMap(value.Type()))
		}
	}
}

func report(opts Options, progress Progress) error {
	if opts.Report == nil {
		return nil
	}
	return opts.Report(progress)
}

// Previous returns a configured automatic model when it remains eligible.
func (s *Service) Previous(ctx context.Context, origin, backend string, budget int64, kvCacheType string) (modelregistry.ModelDescriptor, bool, error) {
	if s.db == nil {
		return modelregistry.ModelDescriptor{}, false, nil
	}
	store := runtimetypes.New(s.db.WithoutTransaction())
	provider, err := readSetting(ctx, store, "default-provider")
	if err != nil {
		return modelregistry.ModelDescriptor{}, false, err
	}
	name, err := readSetting(ctx, store, "default-model")
	if err != nil {
		return modelregistry.ModelDescriptor{}, false, err
	}
	if provider != "modeld" {
		if !strings.HasPrefix(provider, "connection:") {
			return modelregistry.ModelDescriptor{}, false, nil
		}
		connection, err := store.GetBackend(ctx, strings.TrimPrefix(provider, "connection:"))
		if errors.Is(err, libdb.ErrNotFound) {
			return modelregistry.ModelDescriptor{}, false, nil
		}
		if err != nil {
			return modelregistry.ModelDescriptor{}, false, err
		}
		if (connection.Type != "modeld" && connection.Type != "local") || connection.BaseURL != modeldconn.LocalSentinel {
			return modelregistry.ModelDescriptor{}, false, nil
		}
	}
	model, err := modelregistry.NewCurated().Resolve(ctx, name)
	if errors.Is(err, modelregistry.ErrNotFound) {
		return modelregistry.ModelDescriptor{}, false, nil
	}
	if err != nil {
		return modelregistry.ModelDescriptor{}, false, err
	}
	if !model.AutoEligible || !model.SupportsAgenticTools() || model.BackendType() != backend || origin != "" && model.Origin != origin {
		return modelregistry.ModelDescriptor{}, false, nil
	}
	if model.AutoHotContextType(budget, kvCacheType) < modelregistry.MinAgenticHotContext {
		if modeldprobe.New(modeldconn.DataRootFromContext(ctx)).Detect().State != modeldprobe.StateRunning {
			return modelregistry.ModelDescriptor{}, false, nil
		}
		status, err := modeldconn.Status(ctx)
		if errors.Is(err, modeldprobe.ErrNotRunning) || errors.Is(err, modeldprobe.ErrStale) || errors.Is(err, modeldprobe.ErrNotInstalled) || errors.Is(err, modeldprobe.ErrUnreachable) {
			return modelregistry.ModelDescriptor{}, false, nil
		}
		if err != nil {
			return modelregistry.ModelDescriptor{}, false, err
		}
		resident := status.Active != nil && status.Active.ModelName == model.Name
		for _, worker := range status.Workers {
			resident = resident || worker.Spec.Model == model.Name && worker.State == "ready"
		}
		if !resident {
			return modelregistry.ModelDescriptor{}, false, nil
		}
	}
	return *model, true, nil
}

// SaveDefaults makes a verified native model the chat and recovery default.
func (s *Service) SaveDefaults(ctx context.Context, model modelregistry.ModelDescriptor) error {
	if s.db == nil {
		return fmt.Errorf("settings database unavailable")
	}
	tx, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := runtimetypes.BeginWrite(ctx, tx); err != nil {
		return err
	}
	store := runtimetypes.New(tx)
	provider, err := localConnection(ctx, store)
	if err != nil {
		return err
	}
	for _, setting := range []struct{ key, value string }{
		{"default-provider", provider}, {"default-model", model.Name},
		{"default-alt-provider", provider}, {"default-alt-model", model.Name},
	} {
		if err := clikv.WriteConfig(ctx, store, "", setting.key, setting.value); err != nil {
			return err
		}
	}
	return commit(ctx)
}

// SetInitialModel makes an acquired native model the first chat default only
// when the operator has not selected a provider or model.
func SetInitialModel(ctx context.Context, db libdb.DBManager, model string) error {
	tx, commit, release, err := db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := runtimetypes.BeginWrite(ctx, tx); err != nil {
		return err
	}
	store := runtimetypes.New(tx)
	for _, key := range []string{"default-provider", "default-model"} {
		value, err := readSetting(ctx, store, key)
		if err != nil || value != "" {
			return err
		}
	}
	provider, err := localConnection(ctx, store)
	if err != nil {
		return err
	}
	if err := clikv.SetString(ctx, store, "default-provider", provider); err != nil {
		return err
	}
	if err := clikv.SetString(ctx, store, "default-model", model); err != nil {
		return err
	}
	return commit(ctx)
}

// EnsureLocalConnection returns a route to this machine even when other
// Modeld nodes are connected to the same Runtime.
func EnsureLocalConnection(ctx context.Context, db libdb.DBManager) (string, error) {
	tx, commit, release, err := db.WithTransaction(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	if err := runtimetypes.BeginWrite(ctx, tx); err != nil {
		return "", err
	}
	provider, err := localConnection(ctx, runtimetypes.New(tx))
	if err != nil {
		return "", err
	}
	return provider, commit(ctx)
}

func localConnection(ctx context.Context, store runtimetypes.Store) (string, error) {
	backends, err := store.ListAllBackends(ctx)
	if err != nil {
		return "", err
	}
	name := "native-local"
	var id string
	for _, backend := range backends {
		if (backend.Type == "modeld" || backend.Type == "local") && backend.BaseURL == modeldconn.LocalSentinel {
			if id != "" {
				return "", errors.New("more than one local connection is saved; " + remedy.RemoveConnection)
			}
			id = backend.ID
		}
		if backend.Name == name {
			name = "native-local-" + uuid.NewString()
		}
	}
	if id == "" {
		id = uuid.NewString()
		if err := store.EnforceMaxRowCount(ctx, int64(len(backends))); err != nil {
			return "", err
		}
		if err := store.CreateBackend(ctx, &runtimetypes.Backend{ID: id, Name: name, Type: "modeld", BaseURL: modeldconn.LocalSentinel}); err != nil {
			return "", err
		}
	}
	return runtimestate.ConnectionSelector(id), nil
}

// Verify requires a native tool call and a usable measured context window.
func Verify(ctx context.Context, model modelregistry.ModelDescriptor, planned int, kvCacheType string, progress io.Writer) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := modeldconn.EnsureDaemon(ctx); err != nil {
		return 0, err
	}
	info, err := modeldconn.Describe(ctx, modeldconn.ModelRef{Name: model.Name, Type: model.BackendType()}, transport.Config{})
	if err != nil {
		return 0, err
	}
	if info.EffectiveContext < modelregistry.MinAgenticHotContext {
		return 0, fmt.Errorf("worker reports %d context tokens for %s; agentic work needs %d; change local resource settings and try again",
			info.EffectiveContext, model.Name, modelregistry.MinAgenticHotContext)
	}
	provider := native.NewModeldProvider(model.Name, []string{"local"}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true, ContextLength: info.EffectiveContext}, nil)
	chat, err := provider.GetChatConnection(ctx, "local")
	if err != nil {
		return 0, err
	}
	if err := inferencesetup.VerifyToolUse(ctx, chat.Chat); err != nil {
		return 0, err
	}
	session, err := modeldconn.OpenSession(ctx, modeldconn.ModelRef{Name: model.Name, Type: model.BackendType()}, transport.Config{})
	if err != nil {
		return 0, err
	}
	report := session.ExplainContext()
	if err := session.Close(); err != nil {
		return 0, err
	}
	if report.Closed || report.FatalError != "" {
		return 0, fmt.Errorf("native context verification failed: %s", report.FatalError)
	}
	hot := report.HotContextTokens
	if hot <= 0 || hot > report.NumCtx {
		hot = report.NumCtx
	}
	if hot < modelregistry.MinAgenticHotContext {
		return 0, fmt.Errorf("opened session reports %d hot context tokens for %s; agentic work needs %d and selection estimated %d with KV cache type %s",
			hot, model.Name, modelregistry.MinAgenticHotContext, planned, kvLabel(kvCacheType))
	}
	if progress != nil && planned > 0 && hot < planned/2 {
		if _, err := fmt.Fprintf(progress, "Session opened with %d hot tokens; selection estimated %d with KV cache type %s.\n", hot, planned, kvLabel(kvCacheType)); err != nil {
			return 0, err
		}
	}
	return hot, nil
}

func dataRoot(root string) string {
	if strings.TrimSpace(root) != "" {
		return root
	}
	return modeldconn.DataRoot()
}

func readSetting(ctx context.Context, store runtimetypes.Store, key string) (string, error) {
	var value string
	err := store.GetKV(ctx, clikv.Prefix+key, &value)
	if errors.Is(err, libdb.ErrNotFound) {
		return "", nil
	}
	return strings.TrimSpace(value), err
}

func ensureWorker(ctx context.Context, root, backend, baseURL string, progress io.Writer) error {
	status := modeldprobe.New(root).Detect()
	if status.Binary != "" {
		err := modeldinstall.ValidateBinary(ctx, status.Binary, backend)
		if err == nil {
			return nil
		}
		if os.Getenv("CONTENOX_MODELD_BIN") != "" {
			return fmt.Errorf("configured local model engine cannot serve %s: %v", backend, err)
		}
	}
	_, err := modeldinstall.EnsureInstalled(ctx, backend, modeldinstall.Options{DataRoot: root, BaseURL: baseURL, Progress: progress})
	return err
}

func validateModels(plan Plan) error {
	models := map[string]modelregistry.ModelDescriptor{}
	for _, model := range plan.Models {
		canonical, err := modelregistry.NewCurated().Resolve(context.Background(), model.Name)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(model, *canonical) {
			return fmt.Errorf("automatic model contract for %s has changed; review a new recommendation", model.Name)
		}
		if _, exists := models[model.Name]; exists {
			return fmt.Errorf("automatic plan contains duplicate artifact %s", model.Name)
		}
		models[model.Name] = model
	}
	seen := map[string]bool{}
	for _, assignment := range plan.Assignments {
		model, exists := models[assignment.Model]
		if !exists || assignment.Provider != "modeld" || model.BackendType() != plan.Backend {
			return fmt.Errorf("automatic plan has no compatible artifact for %s", assignment.Role)
		}
		if seen[assignment.Role] {
			return fmt.Errorf("automatic plan repeats %s", assignment.Role)
		}
		seen[assignment.Role] = true
		switch assignment.Role {
		case "primary", "alternative":
			if model.Name != plan.Model.Name {
				return errors.New("automatic chat and recovery assignments must match the selected chat artifact")
			}
			if !model.SupportsAgenticTools() {
				return errors.New("automatic chat assignment lacks agentic evidence")
			}
		case "autocomplete":
			if !model.SupportsCompletion() {
				return errors.New("automatic autocomplete assignment lacks an instructed-completion contract")
			}
		case "embedding":
			if !model.SupportsRetrieval() {
				return errors.New("automatic embedding assignment lacks a retrieval contract")
			}
		default:
			return fmt.Errorf("automatic plan contains unknown role %q", assignment.Role)
		}
	}
	for _, role := range []string{"primary", "alternative", "autocomplete", "embedding"} {
		if !seen[role] {
			return fmt.Errorf("automatic plan does not configure required role %s", role)
		}
	}
	if selected, ok := models[plan.Model.Name]; !ok || !reflect.DeepEqual(selected, plan.Model) {
		return errors.New("automatic plan chat artifact does not match its selection")
	}
	return nil
}

func checkDiskModels(root string, models []modelregistry.ModelDescriptor) error {
	var required int64
	for _, model := range models {
		if _, err := modelstore.Resolve(modelstore.Dir(root, ""), model.Name, model.BackendType(), ""); err == nil {
			continue
		} else if !errors.Is(err, modelstore.ErrModelNotFound) {
			return err
		}
		required += model.SizeBytes + model.MMProjSizeBytes
	}
	if required == 0 {
		return nil
	}
	required += 512 * 1024 * 1024
	path := root
	for {
		if _, err := os.Stat(path); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check download storage: %w", err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return fmt.Errorf("cannot find filesystem for %s", root)
		}
		path = parent
	}
	usage, err := disk.Usage(path)
	if err != nil {
		return fmt.Errorf("check download storage: %w", err)
	}
	if required <= 0 || uint64(required) > usage.Free {
		return fmt.Errorf("insufficient storage: need %s, available %s", modelstore.FormatSize(required), modelstore.FormatSize(int64(usage.Free)))
	}
	return nil
}

func kvLabel(kvCacheType string) string {
	if strings.TrimSpace(kvCacheType) == "" {
		return "f16"
	}
	return kvCacheType
}
