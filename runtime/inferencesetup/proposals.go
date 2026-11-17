package inferencesetup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

// SettingsChange specifies whether verification retains or replaces local execution settings.
type SettingsChange string

const (
	// PreserveSettings verifies with the current persisted execution settings.
	PreserveSettings SettingsChange = "preserve"
	// ReplaceSettings verifies proposed execution settings before committing them.
	ReplaceSettings SettingsChange = "replace"
)

// RouteProposal is one reviewed route and its explicit resource-setting policy.
type RouteProposal struct {
	Assignment
	RoleRevision       string                   `json:"roleRevision"`
	ConnectionRevision string                   `json:"connectionRevision"`
	Fingerprint        string                   `json:"fingerprint"`
	SettingsChange     SettingsChange           `json:"settingsChange"`
	Settings           modeldconn.WorkerOptions `json:"settings"`
}

// ProposalRevision returns a changed native execution for review without committing any role.
type ProposalRevision struct {
	Reason    string                     `json:"reason"`
	Proposals []RouteProposal            `json:"proposals"`
	Execution transport.SessionExecution `json:"execution"`
}

// Error reports why the reviewed group needs another review.
func (r *ProposalRevision) Error() string { return r.Reason }

func proposalContext(ctx context.Context, proposals []RouteProposal) (context.Context, error) {
	for _, proposal := range proposals {
		switch proposal.SettingsChange {
		case PreserveSettings:
			if proposal.Settings != (modeldconn.WorkerOptions{}) {
				return nil, errors.New("preserve settings cannot include replacement values")
			}
		case ReplaceSettings:
			var err error
			ctx, err = modeldconn.WithWorkerOptions(ctx, proposal.Role, proposal.Settings)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("role %q requires an explicit preserve or replace settings choice", proposal.Role)
		}
	}
	return ctx, nil
}

// Review captures role and connection revisions and the proposed execution contract without running inference.
// Previously captured revisions refuse concurrent edits instead of replacing the user's reviewed configuration.
func (s *Service) Review(ctx context.Context, proposals []RouteProposal) ([]RouteProposal, error) {
	if len(proposals) == 0 {
		return nil, errors.New("select at least one role to review")
	}
	ctx, err := proposalContext(ctx, proposals)
	if err != nil {
		return nil, err
	}
	store := runtimetypes.New(s.db.WithoutTransaction())
	connections, err := store.ListAllBackends(ctx)
	if err != nil {
		return nil, err
	}
	reviewed := append([]RouteProposal{}, proposals...)
	seen := map[string]bool{}
	for index := range reviewed {
		proposal := &reviewed[index]
		if err := proposal.Inputs.validate(proposal.Role); err != nil {
			return nil, err
		}
		if seen[proposal.Role] {
			return nil, fmt.Errorf("role %s appears more than once", proposal.Role)
		}
		seen[proposal.Role] = true
		currentRole, err := verificationRevision(ctx, store, proposal.Role)
		if err != nil {
			return nil, err
		}
		var connection *runtimetypes.Backend
		for _, candidate := range connections {
			if runtimestate.MatchesBackend(proposal.Provider, *candidate) {
				if connection != nil {
					return nil, errors.New("select an unambiguous connection before reviewing this role")
				}
				connection = candidate
			}
		}
		if connection == nil {
			return nil, errors.New("select a saved connection before reviewing this role")
		}
		currentConnection, err := connectionRevision(ctx, store, connection)
		if err != nil {
			return nil, err
		}
		if proposal.RoleRevision != "" && proposal.RoleRevision != currentRole || proposal.ConnectionRevision != "" && proposal.ConnectionRevision != currentConnection {
			return nil, ErrConfigurationChanged
		}
		proposal.RoleRevision, proposal.ConnectionRevision = currentRole, currentConnection
		proposal.Inputs = proposal.Inputs.normalized()
		if proposal.Role == RoleRecovery && proposal.SettingsChange == PreserveSettings && (connection.Type == "modeld" || connection.Type == "local") {
			options, err := modelworkers.ReadAssignedOptions(ctx, store, proposal.Role)
			if err != nil {
				return nil, err
			}
			ctx, err = modeldconn.WithWorkerOptions(ctx, proposal.Role, options)
			if err != nil {
				return nil, err
			}
		}
		proposal.Fingerprint, err = s.fingerprint(ctx, store, proposal.Assignment)
		if err != nil {
			return nil, err
		}
	}
	return reviewed, nil
}

// Apply verifies a reviewed group before atomically committing its routes, settings and evidence, recording source as their origin.
func (s *Service) Apply(ctx context.Context, proposals []RouteProposal, source AssignmentSource) ([]Verification, error) {
	if !source.Valid() {
		return nil, fmt.Errorf("assignment source %q is not one of %s", source, strings.Join(AssignmentSources(), ", "))
	}
	ctx, err := proposalContext(ctx, proposals)
	if err != nil {
		return nil, err
	}
	assignments := make([]Assignment, 0, len(proposals))
	revisions, fingerprints := map[string]string{}, map[string]string{}
	for _, proposal := range proposals {
		if proposal.RoleRevision == "" || proposal.ConnectionRevision == "" || proposal.Fingerprint == "" {
			return nil, errors.New("review the role and connection before applying this proposal")
		}
		revisions[proposal.Role], fingerprints[proposal.Role] = proposal.RoleRevision, proposal.Fingerprint
		assignments = append(assignments, proposal.Assignment)
	}
	verified, err := s.assignReviewed(ctx, assignments, revisions, fingerprints, source)
	var changed *modeldconn.ExecutionRevision
	if !errors.As(err, &changed) {
		return verified, err
	}
	revised := append([]RouteProposal{}, proposals...)
	for index := range revised {
		proposal := &revised[index]
		if proposal.Role != changed.Role {
			continue
		}
		if proposal.SettingsChange == PreserveSettings {
			proposal.Settings, err = modelworkers.ReadAssignedOptions(ctx, runtimetypes.New(s.db.WithoutTransaction()), proposal.Role)
			if err != nil {
				return nil, err
			}
		}
		resolved := changed.Execution.Resolved
		proposal.SettingsChange = ReplaceSettings
		proposal.Settings.Target = resolved.Target
		proposal.Settings.Context, proposal.Settings.Threads = resolved.NumCtx, resolved.NumThreads
		proposal.Settings.Batch, proposal.Settings.KVCacheType = resolved.NumBatch, resolved.KVCacheType
		proposal.Fingerprint = ""
	}
	revised, err = s.Review(ctx, revised)
	if err != nil {
		return nil, err
	}
	return nil, &ProposalRevision{Reason: changed.Error(), Proposals: revised, Execution: changed.Execution}
}

// ConfigureWorker verifies a role's proposed device and resource settings before saving them.
func (s *Service) ConfigureWorker(ctx context.Context, role string, options modeldconn.WorkerOptions) (*Verification, error) {
	proposed, err := modeldconn.WithWorkerOptions(ctx, role, options)
	if err != nil {
		return nil, err
	}
	store := runtimetypes.New(s.db.WithoutTransaction())
	revision, err := verificationRevision(ctx, store, role)
	if err != nil {
		return nil, err
	}
	assignment, err := s.readAssignment(ctx, store, role)
	if err != nil {
		return nil, err
	}
	if assignment.Model == "" || assignment.Provider == "" {
		return nil, errors.New("select an explicit role connection and model before verifying resource settings; inherited Recovery uses Chat settings")
	}
	verified, err := s.assignAll(proposed, []Assignment{assignment}, map[string]string{role: revision}, SourceUser)
	if err != nil {
		return nil, err
	}
	return &verified[0], nil
}
