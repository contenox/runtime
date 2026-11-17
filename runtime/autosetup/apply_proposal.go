package autosetup

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

// ApplyRecommended acquires and verifies the explicitly selected roles before committing them as one group.
func (s *Service) ApplyRecommended(ctx context.Context, plan Plan, selected []string, opts Options, roles *inferencesetup.Service, acquisition *modelacquisition.Service) (Result, error) {
	if roles == nil || acquisition == nil || opts.Report == nil || opts.Progress == nil || len(selected) == 0 {
		return Result{}, errors.New("applying recommendations requires selected roles, configuration, acquisition and progress")
	}
	if err := validatePlan(plan); err != nil {
		return Result{}, err
	}
	configuration, err := roles.Configuration(ctx)
	if err != nil {
		return Result{}, err
	}
	seen := map[string]bool{}
	proposals := []inferencesetup.RouteProposal{}
	candidates := []RoleCandidate{}
	for _, name := range selected {
		if seen[name] {
			return Result{}, errors.New("a role may only be selected once")
		}
		seen[name] = true
		var planned *RolePlan
		for index := range plan.Roles {
			if plan.Roles[index].Constraint.Role == name {
				planned = &plan.Roles[index]
				break
			}
		}
		if planned == nil || planned.Selected == nil || planned.State != "review" && planned.State != "acquire" {
			return Result{}, fmt.Errorf("role %s needs a resolved proposal before application", name)
		}
		candidate := *planned.Selected
		foundRole, foundConnection := false, false
		for _, role := range configuration.Roles {
			if role.Role == name {
				foundRole = role.Revision == candidate.Route.RoleRevision
			}
		}
		for _, connection := range configuration.Providers {
			if connection.ID == candidate.ConnectionID {
				foundConnection = connection.Revision == candidate.Route.ConnectionRevision && connection.URL == candidate.Endpoint
			}
		}
		if !foundRole || !foundConnection {
			return Result{}, inferencesetup.ErrConfigurationChanged
		}
		candidates = append(candidates, candidate)
	}
	for _, candidate := range candidates {
		if candidate.Native && candidate.Endpoint == modeldconn.LocalSentinel {
			if err := report(opts, Progress{Phase: "engine", Message: "Preparing " + candidate.Route.Settings.Backend}); err != nil {
				return Result{}, err
			}
			if err := ensureWorker(ctx, dataRoot(opts.DataRoot), candidate.Route.Settings.Backend, opts.WorkerBaseURL, opts.Progress); err != nil {
				return Result{}, err
			}
		}
		if !candidate.Installed {
			if candidate.Artifact == nil {
				return Result{}, errors.New("native acquisition has no reviewed artifact contract")
			}
			canonical, err := modelregistry.NewCurated().Resolve(ctx, candidate.Route.Model)
			if err != nil {
				return Result{}, err
			}
			if !reflect.DeepEqual(candidate.Artifact, canonical) {
				return Result{}, errors.New("model acquisition contract changed; review a new proposal")
			}
			if err := acquisition.PullTo(ctx, modelacquisition.Request{Target: "connection", ConnectionID: candidate.ConnectionID, Model: candidate.Route.Model}, func(value modelacquisition.DownloadProgress) error {
				return report(opts, Progress{Phase: "download", Message: "Downloading " + candidate.Route.Model + " to " + candidate.Endpoint, Current: value.Downloaded, Total: value.Total})
			}); err != nil {
				return Result{}, err
			}
		}
		proposal := candidate.Route
		if proposal.Fingerprint == "" {
			reviewed, err := roles.Review(ctx, []inferencesetup.RouteProposal{proposal})
			if err != nil {
				return Result{}, err
			}
			proposal = reviewed[0]
		}
		proposals = append(proposals, proposal)
	}
	if err := report(opts, Progress{Phase: "verify", Message: "Verifying the reviewed production operations"}); err != nil {
		return Result{}, err
	}
	if _, err := roles.Apply(ctx, proposals, inferencesetup.SourcePlanner); err != nil {
		return Result{}, err
	}
	return Result{Plan: plan}, nil
}

func validatePlan(plan Plan) error {
	fingerprint, err := planFingerprint(plan)
	if err != nil {
		return err
	}
	if plan.Fingerprint == "" || plan.Fingerprint != fingerprint {
		return errors.New("recommendation changed; review a new proposal")
	}
	return nil
}

// Choose replaces one selection with a displayed alternative and rechecks the group's resource and scheduling constraints.
func (s *Service) Choose(plan Plan, role string, alternative int) (Plan, error) {
	if err := validatePlan(plan); err != nil {
		return Plan{}, err
	}
	index := slices.IndexFunc(plan.Roles, func(value RolePlan) bool { return value.Constraint.Role == role })
	if index < 0 || alternative < 0 || alternative >= len(plan.Roles[index].Alternatives) {
		return Plan{}, errors.New("select a displayed role alternative")
	}
	plan.Roles = append([]RolePlan{}, plan.Roles...)
	selected := plan.Roles[index].Alternatives[alternative]
	plan.Roles[index].Selected = &selected
	used := map[string]int64{}
	for _, pool := range plan.Baseline {
		used[pool.Host+"\n"+pool.PoolID] += pool.Bytes
	}
	previous := []RolePlan{}
	for _, result := range plan.Roles {
		if result.State == "preserved" {
			previous = append(previous, result)
		}
	}
	plan.Assignments = []PlannedRole{}
	for index := range plan.Roles {
		result := &plan.Roles[index]
		if result.Selected == nil || result.State == "preserved" || result.State == "inherited" {
			previous = append(previous, *result)
			continue
		}
		fits, ceilingRepair := candidateFits(*result.Selected, used)
		if !fits || !schedulingFits(*result.Selected, previous) && result.Constraint.ExecutionMode != "serialized" {
			result.State = "unresolved"
			result.Repairs = []string{"This combination needs another placement, model, connection or explicit serialized execution."}
			if ceilingRepair != "" {
				result.Repairs = append([]string{ceilingRepair}, result.Repairs...)
			}
		} else {
			copy := *result.Selected
			result.Selected = &copy
			if !schedulingFits(copy, previous) {
				result.Selected.Scheduling = "serialized"
			}
			result.State = "review"
			if !result.Selected.Installed {
				result.State = "acquire"
			}
			for _, pool := range result.Selected.Pools {
				used[pool.Host+"\n"+pool.PoolID] += pool.Bytes
			}
			plan.Assignments = append(plan.Assignments, PlannedRole{Role: result.Constraint.Role, Provider: result.Selected.Route.Provider, Model: result.Selected.Route.Model})
		}
		previous = append(previous, *result)
	}
	var err error
	plan.Fingerprint, err = planFingerprint(plan)
	return plan, err
}
