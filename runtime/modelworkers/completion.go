package modelworkers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const completionProtectionKey = "inference.modeld.completion-protection"

type completionRecord struct {
	Plan     modeldconn.CompletionPlan
	Provider string
	Model    string
	Options  modeldconn.WorkerOptions
}

func completionConnection(ctx context.Context, store runtimetypes.Store, selector string) (*runtimetypes.Backend, error) {
	backends, err := store.ListAllBackends(ctx)
	if err != nil {
		return nil, err
	}
	var selected *runtimetypes.Backend
	for _, backend := range backends {
		if runtimestate.MatchesBackend(selector, *backend) {
			if selected != nil {
				return nil, errors.New("more than one connection matches Autocomplete; " + remedy.PickConnection)
			}
			selected = backend
		}
	}
	if selected == nil {
		return nil, errors.New("autocomplete connection is unavailable; " + remedy.RepairConnection)
	}
	return selected, nil
}

// SaveCompletionProtection persists owner-confirmed allocation evidence in the role assignment transaction.
func SaveCompletionProtection(ctx context.Context, store runtimetypes.Store, provider, model string, executions []transport.SessionExecution) error {
	if store == nil || provider == "" || model == "" {
		return errors.New("completion protection requires persistence and an explicit route")
	}
	options, err := ReadAssignedOptions(ctx, store, "autocomplete")
	if err != nil {
		return err
	}
	connection, err := completionConnection(ctx, store, provider)
	if err != nil {
		return err
	}
	plan := modeldconn.CompletionPlan{Endpoint: connection.BaseURL, Protection: transport.CompletionProtection{State: "on-demand"}}
	if connection.Type != "modeld" && connection.Type != "local" {
		plan.Protection.State = "external"
	} else if options.Residency == "protected" {
		plan.Protection.State = "protected"
		seen := map[string]bool{}
		for _, execution := range executions {
			allocation := execution.Capacity.Allocation
			if execution.ModelName != model || execution.OwnerEpoch == "" || execution.WorkerIdentity == "" || allocation == nil || allocation.Resources.Identity == "" || len(allocation.Resources.Pools) == 0 {
				return errors.New("completion protection requires owner allocation evidence; " + remedy.UpdateLocal + "; " + remedy.VerifyAutocomplete)
			}
			if !seen[allocation.Resources.Identity] {
				seen[allocation.Resources.Identity] = true
				plan.Protection.Reservations = append(plan.Protection.Reservations, admission.Protection{Identity: allocation.Resources.Identity, Pools: allocation.Resources.Pools})
			}
		}
		if len(plan.Protection.Reservations) == 0 {
			return errors.New("verify native Autocomplete before protecting its residency")
		}
	}
	raw, err := json.Marshal(completionRecord{Plan: plan, Provider: provider, Model: model, Options: options})
	if err != nil {
		return err
	}
	return store.SetKV(ctx, completionProtectionKey, raw)
}

// CompletionSource reinstates verified residency on its selected host before native requests after restart.
func CompletionSource(db libdbexec.DBManager) func(context.Context, string) (modeldconn.CompletionPlan, error) {
	return func(ctx context.Context, endpoint string) (modeldconn.CompletionPlan, error) {
		if db == nil {
			return modeldconn.CompletionPlan{}, errors.New("completion protection requires persistence")
		}
		tx, _, release, err := db.WithTransaction(ctx)
		if err != nil {
			return modeldconn.CompletionPlan{}, err
		}
		defer release()
		store := runtimetypes.New(tx)
		var provider, model string
		for key, value := range map[string]*string{"default-autocomplete-provider": &provider, "default-autocomplete-model": &model} {
			if err := store.GetKV(ctx, clikv.Prefix+key, value); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
				return modeldconn.CompletionPlan{}, err
			}
		}
		plan := modeldconn.CompletionPlan{Protection: transport.CompletionProtection{State: "unconfigured"}}
		if provider == "" || model == "" {
			return plan, nil
		}
		connection, err := completionConnection(ctx, store, provider)
		if err != nil {
			return plan, err
		}
		if connection.Type != "modeld" && connection.Type != "local" || connection.BaseURL != endpoint {
			plan.Protection.State = "external"
			return plan, nil
		}
		options, err := ReadAssignedOptions(ctx, store, "autocomplete")
		if err != nil {
			return plan, err
		}
		if options.Residency != "protected" {
			plan.Protection.State = "on-demand"
			return plan, nil
		}
		var saved completionRecord
		if err := store.GetKV(ctx, completionProtectionKey, &saved); err != nil {
			if errors.Is(err, libdbexec.ErrNotFound) && modeldconn.CapturingExecution(ctx) {
				return modeldconn.CompletionPlan{Protection: transport.CompletionProtection{State: "retain"}}, nil
			}
			return plan, errors.Join(errors.New("completion residency has no verified allocation; "+remedy.VerifyAutocomplete), err)
		}
		if saved.Provider != provider || saved.Model != model || saved.Options != options || saved.Plan.Endpoint != connection.BaseURL {
			if modeldconn.CapturingExecution(ctx) {
				return modeldconn.CompletionPlan{Protection: transport.CompletionProtection{State: "retain"}}, nil
			}
			return plan, errors.New("completion residency differs from the current route; " + remedy.VerifyAutocomplete)
		}
		return saved.Plan, nil
	}
}
