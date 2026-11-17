package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/internal/setupcheck"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/providerservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/servicecontract"
	"github.com/contenox/runtime/runtime/stateservice"
)

type inferenceHandler struct {
	*Transport
	roles *inferencesetup.Service
	state *runtimestate.State
}

type inferenceRole struct {
	AssignmentState string                          `json:"assignmentState"`
	ConnectionID    string                          `json:"connectionId"`
	Revision        string                          `json:"revision"`
	Inputs          inferencesetup.InputRequirement `json:"inputs"`
	Verification    *inferencesetup.Verification    `json:"verification,omitempty"`
	ID              string                          `json:"id"`
	Name            string                          `json:"name"`
	Provider        string                          `json:"provider"`
	Model           string                          `json:"model"`
	State           string                          `json:"state"`
	Detail          string                          `json:"detail,omitempty"`
	Problem         string                          `json:"problem,omitempty"`
	Inherited       bool                            `json:"inherited"`
	Enabled         bool                            `json:"enabled"`
	Source          inferencesetup.AssignmentSource `json:"source,omitempty"`
}

type inferenceSnapshot struct {
	AcquisitionContract   servicecontract.Contract             `json:"acquisitionContract"`
	RegistrationContract  servicecontract.Contract             `json:"registrationContract"`
	ConfigurationContract servicecontract.Contract             `json:"configurationContract"`
	ProviderResult        *inferencesetup.ProviderResult       `json:"providerResult,omitempty"`
	Roles                 []inferenceRole                      `json:"roles"`
	Models                []inferencesetup.AvailableModel      `json:"models"`
	Providers             []inferencesetup.ProviderConnection  `json:"providers"`
	ProviderTypes         []providerservice.ProviderCapability `json:"providerTypes"`
	Readiness             *setupcheck.Result                   `json:"readiness,omitempty"`
	AutomaticSetup        *inferenceAutomaticSetup             `json:"automaticSetup,omitempty"`
	CompletionContract    servicecontract.Contract             `json:"completionContract"`
	EditorContract        servicecontract.Contract             `json:"editorContract"`
	ReviewedProposals     []inferencesetup.RouteProposal       `json:"reviewedProposals,omitempty"`
	ProposalRevision      *inferencesetup.ProposalRevision     `json:"proposalRevision,omitempty"`
	Ceilings              []autosetup.PoolCeiling              `json:"ceilings,omitempty"`
}

type inferenceAutomaticSetup struct {
	Plan       autosetup.Plan           `json:"plan"`
	State      string                   `json:"state"`
	HotContext int                      `json:"hotContext,omitempty"`
	Contract   servicecontract.Contract `json:"contract"`
}

func (t *inferenceHandler) inferenceSnapshot(ctx context.Context) (inferenceSnapshot, error) {
	setup := t.roles
	configuration, err := setup.Configuration(ctx)
	if err != nil {
		return inferenceSnapshot{}, err
	}
	result := inferenceSnapshot{
		AcquisitionContract:   modelacquisition.Contract,
		RegistrationContract:  inferencesetup.RegistrationContract,
		ConfigurationContract: inferencesetup.ConfigurationContract,
		Roles:                 []inferenceRole{}, Models: configuration.Models, Providers: configuration.Providers,
		ProviderTypes: configuration.ProviderTypes, CompletionContract: inferencesetup.CompletionContract, EditorContract: codecompletion.Contract(),
	}
	for _, current := range configuration.Roles {
		result.Roles = append(result.Roles, inferenceRole{
			AssignmentState: current.AssignmentState, ConnectionID: current.ConnectionID, Revision: current.Revision,
			Inputs:       current.Inputs,
			Verification: current.Verification,
			ID:           current.Role, Name: current.Name, Provider: current.Provider, Model: current.Model,
			State: current.State, Detail: current.Detail, Problem: current.Problem, Inherited: current.Inherited, Enabled: current.Enabled, Source: current.Source,
		})
	}
	return result, nil
}
func (t *inferenceHandler) inferenceReadinessSnapshot(ctx context.Context) (inferenceSnapshot, error) {
	readiness, err := stateservice.New(t.state, t.deps.DB, t.deps.WorkspaceID).Refresh(ctx)
	if err != nil {
		return inferenceSnapshot{}, err
	}
	readiness = setupcheck.OverlayEffectiveDefaults(readiness, t.model(), t.provider())
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return inferenceSnapshot{}, err
	}
	snapshot.Readiness = &readiness
	return snapshot, nil
}

// Actions with declared service operations use the same required inputs as the CLI.
var inferenceInputs = func() map[string][]string {
	inputs := map[string][]string{
		"assign": {"role"}, "set-role-enabled": {"role"}, "remove-provider": {"id"}, "login": {"id"},
	}
	add := func(contract servicecontract.Contract, actions map[string]string) {
		for _, op := range contract.Operations {
			if action := actions[op.ID]; action != "" {
				for _, input := range op.Inputs {
					if input.Required {
						inputs[action] = append(inputs[action], input.ID)
					}
				}
			}
		}
	}
	add(autosetup.Contract, map[string]string{"recommend": "auto-recommend", "apply": "auto-apply", "choose": "auto-edit", "prepare-local": "prepare-native", "configure-ceiling": "configure-ceiling"})
	add(modelacquisition.Contract, map[string]string{"pull": "pull"})
	add(inferencesetup.RegistrationContract, map[string]string{"register": "register-model"})
	add(inferencesetup.CompletionContract, map[string]string{"declare": "declare-completion"})
	add(inferencesetup.ConfigurationContract, map[string]string{"apply": "apply-roles", "connect": "save-provider", "configure-resources": "configure-resources"})
	return inputs
}()

// missingDeclaredInput names the first input the declared contract requires and
// the request does not carry, so a surface learns the requirement from the
// contract instead of from a failure further down the call.
func missingDeclaredInput(action string, params json.RawMessage) string {
	required := inferenceInputs[action]
	if len(required) == 0 {
		return ""
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(params, &present); err != nil {
		return ""
	}
	for _, input := range required {
		if absentValue(present[input]) {
			return input
		}
	}
	return ""
}

func absentValue(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "[]", "{}", `""`:
		return true
	}
	return false
}

func (t *Transport) handleInference(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	if t.deps.DB == nil {
		return nil, libacp.InternalError("settings database unavailable")
	}
	p := &protocolv1.InferenceRequest{}
	if rpcErr := unmarshalProtocolRequest("inference configuration", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	action := p.GetAction()
	if missing := missingDeclaredInput(action, params); missing != "" {
		return nil, libacp.NewError(libacp.ErrInvalidParams, fmt.Sprintf("inference action %s requires the %q input", action, missing))
	}
	if t.deps.OpenInference == nil {
		return nil, libacp.InternalError("inference configuration is unavailable")
	}
	setup, state, closeSetup, err := t.deps.OpenInference(ctx)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	defer closeSetup()
	return (&inferenceHandler{Transport: t, roles: setup, state: state}).run(ctx, p)
}

func (t *inferenceHandler) run(ctx context.Context, p *protocolv1.InferenceRequest) (json.RawMessage, *libacp.Error) {
	action := p.GetAction()
	var result any
	var err error
	switch action {
	case "set-next-edit-enabled", "set-role-enabled":
		setup := t.roles
		role := p.GetRole()
		if action == "set-next-edit-enabled" {
			role = inferencesetup.RoleNextEdit
		} else if role == "" {
			err = fmt.Errorf("inference action %s requires the %q input; optional roles: %s", action, "role", strings.Join(inferencesetup.OptionalRoles(), ", "))
			break
		}
		err = setup.SetRoleEnabled(ctx, role, p.GetEnabled())
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "register-model":
		setup := t.roles
		err = setup.RegisterModel(ctx, inferenceRegistrationEngine(p.GetRegistration()))
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "status":
		result, err = t.inferenceSnapshot(ctx)
	case "review-roles":
		setup := t.roles
		var proposals []inferencesetup.RouteProposal
		proposals, err = setup.Review(ctx, inferenceRouteProposalsEngine(p.GetProposals()))
		if err == nil {
			var snapshot inferenceSnapshot
			snapshot, err = t.inferenceSnapshot(ctx)
			snapshot.ReviewedProposals = proposals
			result = snapshot
		}
	case "configure-resources", "apply-roles":
		err = t.withOperation(ctx, p.GetOperationId(), "inference-verification", "Verify AI configuration", func(progress *operationReporter) error {
			setup := t.roles
			var setupErr error
			if err := progress.running("verify", "Verifying the reviewed configuration", 0, 0); err != nil {
				return err
			}
			if action == "configure-resources" {
				_, setupErr = setup.ConfigureWorker(ctx, p.GetRole(), workerOptionsEngine(p.GetOptions()))
			} else {
				_, setupErr = setup.Apply(ctx, inferenceRouteProposalsEngine(p.GetProposals()), inferencesetup.SourceUser)
			}
			if setupErr != nil {
				return setupErr
			}
			result, setupErr = t.inferenceSnapshot(ctx)
			return setupErr
		})
	case "configure-ceiling":
		var applied autosetup.CeilingResult
		applied, err = t.configureCeiling(ctx, autosetup.PoolCeiling{ConnectionID: p.GetId(), PoolID: p.GetPoolId(), Share: p.GetShare()})
		if err == nil {
			var snapshot inferenceSnapshot
			snapshot, err = t.inferenceSnapshot(ctx)
			snapshot.Ceilings = applied.Ceilings
			result = snapshot
		}
	case "declare-completion":
		setup := t.roles
		err = setup.DeclareCompletion(ctx, p.GetProvider(), p.GetModel(), p.GetCompletionMode())
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "prepare-native":
		err = t.withOperation(ctx, p.GetOperationId(), "native-installation", "Prepare local inference", func(progress *operationReporter) error {
			setup, setupErr := autosetup.New(t.deps.DB, t.tracker())
			if setupErr != nil {
				return setupErr
			}
			roles := t.roles
			prepared, setupErr := setup.PrepareLocal(ctx, inferenceNativePrerequisiteEngine(p.GetNativePrerequisite()), autosetup.Options{DataRoot: modeldconn.DataRootFromContext(ctx), Progress: io.Discard, Report: func(value autosetup.Progress) error {
				return progress.running(value.Phase, value.Message, value.Current, value.Total)
			}}, roles)
			if setupErr != nil {
				return setupErr
			}
			snapshot, setupErr := t.inferenceSnapshot(ctx)
			snapshot.ProviderResult = &prepared
			result = snapshot
			return setupErr
		})
	case "auto-edit":
		if p.GetPlan() == nil {
			return nil, libacp.NewError(libacp.ErrInvalidParams, "select a reviewed recommendation")
		}
		var setup *autosetup.Service
		setup, err = autosetup.New(t.deps.DB, t.tracker())
		if err == nil {
			var plan autosetup.Plan
			plan, err = setup.Choose(inferencePlanEngine(p.GetPlan()), p.GetRole(), int(p.GetAlternative()))
			if err == nil {
				var snapshot inferenceSnapshot
				snapshot, err = t.inferenceSnapshot(ctx)
				snapshot.AutomaticSetup = &inferenceAutomaticSetup{Plan: plan, State: "planned", Contract: autosetup.Contract}
				result = snapshot
			}
		}
	case "auto-plan":
		var plan autosetup.Plan
		var setup *autosetup.Service
		setup, err = autosetup.New(t.deps.DB, t.tracker())
		if err == nil {
			plan, err = setup.Plan(ctx, autosetup.Options{
				Origin:      p.Origin,
				Backend:     strings.TrimSpace(os.Getenv("CONTENOX_MODELD_BACKEND")),
				KVCacheType: strings.TrimSpace(os.Getenv("CONTENOX_LLAMA_KV_CACHE_TYPE")),
				DataRoot:    modeldconn.DataRootFromContext(ctx),
				Refresh:     p.Refresh,
			})
		}
		if err == nil {
			var snapshot inferenceSnapshot
			snapshot, err = t.inferenceSnapshot(ctx)
			snapshot.AutomaticSetup = &inferenceAutomaticSetup{Plan: plan, State: "planned", Contract: autosetup.Contract}
			result = snapshot
		}
	case "auto-recommend":
		var plan autosetup.Plan
		var setup *autosetup.Service
		setup, err = autosetup.New(t.deps.DB, t.tracker())
		if err == nil {
			plan, err = setup.Recommend(ctx, t.roles, inferenceRecommendationEngine(p.GetRecommendation()))
		}
		if err == nil {
			var snapshot inferenceSnapshot
			snapshot, err = t.inferenceSnapshot(ctx)
			snapshot.AutomaticSetup = &inferenceAutomaticSetup{Plan: plan, State: "planned", Contract: autosetup.Contract}
			result = snapshot
		}
	case "auto-apply":
		if p.GetPlan() == nil {
			return nil, libacp.NewError(libacp.ErrInvalidParams, "automatic setup requires the reviewed plan")
		}
		err = t.withOperation(ctx, p.GetOperationId(), "model-setup", "Local AI setup", func(progress *operationReporter) error {
			setup, setupErr := autosetup.New(t.deps.DB, t.tracker())
			if setupErr != nil {
				return setupErr
			}
			opts := autosetup.Options{
				Origin:      p.Origin,
				Backend:     strings.TrimSpace(os.Getenv("CONTENOX_MODELD_BACKEND")),
				KVCacheType: strings.TrimSpace(os.Getenv("CONTENOX_LLAMA_KV_CACHE_TYPE")),
				DataRoot:    modeldconn.DataRootFromContext(ctx), Refresh: p.Refresh, Progress: io.Discard,
				Report: func(value autosetup.Progress) error {
					return progress.running(value.Phase, value.Message, value.Current, value.Total)
				},
			}
			plan := inferencePlanEngine(p.GetPlan())
			roles := t.roles
			var applyErr error
			var applied autosetup.Result
			if len(plan.Roles) > 0 {
				var acquisition *modelacquisition.Service
				acquisition, applyErr = t.modelAcquisition(ctx)
				if applyErr == nil {
					applied, applyErr = setup.ApplyRecommended(ctx, plan, p.GetSelectedRoles(), opts, roles, acquisition)
				}
			} else {
				applied, applyErr = setup.ApplyAll(ctx, plan, opts, roles)
			}
			if applyErr != nil {
				return applyErr
			}
			snapshot, applyErr := t.inferenceReadinessSnapshot(ctx)
			if applyErr != nil {
				return applyErr
			}
			t.cfgMu.Lock()
			for _, role := range snapshot.Roles {
				switch role.ID {
				case inferencesetup.RoleChat:
					t.defaultProvider, t.defaultModel = role.Provider, role.Model
				case inferencesetup.RoleRecovery:
					t.defaultAltProvider, t.defaultAltModel = role.Provider, role.Model
				}
			}
			t.cfgMu.Unlock()
			snapshot.AutomaticSetup = &inferenceAutomaticSetup{Plan: plan, State: "ready", HotContext: applied.HotContext, Contract: autosetup.Contract}
			result = snapshot
			return nil
		})
	case "refresh":
		setup := t.roles
		err = setup.RefreshProvider(ctx, p.GetProvider())
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "assign":
		err = t.assignInferenceRole(ctx, p.GetRole(), strings.TrimSpace(p.GetProvider()), strings.TrimSpace(p.GetModel()), p.GetInherit())
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "test":
		setup := t.roles
		_, err = setup.Verify(ctx, p.GetRole())
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "save-provider":
		setup := t.roles
		var saved inferencesetup.ProviderResult
		saved, err = setup.SaveProvider(ctx, inferencesetup.ProviderRequest{ID: p.GetId(), Name: p.GetName(), Provider: p.GetProvider(), Endpoint: p.GetUrl(), APIKey: p.GetApiKey(), APIKeyEnv: p.GetApiKeyEnv(), CredentialSource: p.GetCredentialSource(), CredentialFile: p.GetCredentialFile()})
		if err == nil {
			var snapshot inferenceSnapshot
			snapshot, err = t.inferenceSnapshot(ctx)
			snapshot.ProviderResult = &saved
			result = snapshot
		}
	case "remove-provider":
		setup := t.roles
		err = setup.RemoveProvider(ctx, p.GetId())
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "login":
		if t.conn == nil {
			err = fmt.Errorf("provider does not support device login")
			break
		}
		setup := t.roles
		err = setup.LoginProvider(ctx, p.GetId(), func(code modelauth.DeviceCode) error {
			raw, err := json.Marshal(map[string]string{"url": code.URL, "code": code.Code})
			if err != nil {
				return err
			}
			_, err = t.conn.CallExtMethod(ctx, "_contenox/provider/authorize", raw)
			return err
		})
		if err == nil {
			result, err = t.inferenceSnapshot(ctx)
		}
	case "catalog":
		var acquisition *modelacquisition.Service
		acquisition, err = t.modelAcquisition(ctx)
		if err == nil {
			result, err = acquisition.Catalog(ctx)
		}
	case "pull":
		err = t.withOperation(ctx, p.GetOperationId(), "model-download", "Download "+p.GetModel(), func(progress *operationReporter) error {
			acquisition, pullErr := t.modelAcquisition(ctx)
			if pullErr != nil {
				return pullErr
			}
			if pullErr = progress.running("download", "Downloading "+p.GetModel(), 0, 0); pullErr != nil {
				return pullErr
			}
			request := inferenceAcquisitionEngine(p.GetAcquisition())
			if request.Target == "" {
				request = modelacquisition.Request{Target: "local", Model: p.GetModel()}
			}
			pullErr = acquisition.PullTo(ctx, request, func(value modelacquisition.DownloadProgress) error {
				return progress.running("download", "Downloading "+p.GetModel(), value.Downloaded, value.Total)
			})
			if pullErr != nil {
				return pullErr
			}
			if pullErr = progress.running("refresh", "Refreshing acquired models", 0, 0); pullErr != nil {
				return pullErr
			}
			result, pullErr = t.inferenceSnapshot(ctx)
			return pullErr
		})
	default:
		return nil, libacp.NewError(libacp.ErrInvalidParams, "unknown inference configuration action")
	}
	if err != nil {
		var revision *inferencesetup.ProposalRevision
		if !errors.As(err, &revision) {
			return nil, libacp.InternalError(err.Error())
		}
		snapshot, snapshotErr := t.inferenceSnapshot(ctx)
		if snapshotErr != nil {
			return nil, libacp.InternalError(snapshotErr.Error())
		}
		snapshot.ProposalRevision = revision
		result = snapshot
	}
	switch value := result.(type) {
	case inferenceSnapshot:
		return marshalProtocol(inferenceResultWire(value))
	case []modelregistry.ModelDescriptor:
		return marshalProtocolList(modelDescriptorsWire(value))
	default:
		return nil, libacp.InternalError("inference action " + action + " answered no declared result")
	}
}

func (t *inferenceHandler) configureCeiling(ctx context.Context, ceiling autosetup.PoolCeiling) (autosetup.CeilingResult, error) {
	setup, err := autosetup.New(t.deps.DB, t.tracker())
	if err != nil {
		return autosetup.CeilingResult{}, err
	}
	roles := t.roles
	return setup.ConfigureCeiling(ctx, roles, ceiling)
}

func (t *inferenceHandler) assignInferenceRole(ctx context.Context, role, provider, model string, inherit bool) error {
	setup := t.roles
	if inherit {
		if role != inferencesetup.RoleRecovery {
			return fmt.Errorf("role %q requires an explicit verified model", role)
		}
		if err := setup.InheritRecovery(ctx); err != nil {
			return err
		}
	} else if _, err := setup.Assign(ctx, inferencesetup.Assignment{Role: role, Provider: provider, Model: model}, inferencesetup.SourceUser); err != nil {
		return err
	}
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	if role == inferencesetup.RoleChat {
		t.defaultProvider, t.defaultModel = provider, model
	}
	if role == inferencesetup.RoleRecovery {
		if inherit {
			t.defaultAltProvider, t.defaultAltModel = "", ""
		} else {
			t.defaultAltProvider, t.defaultAltModel = provider, model
		}
	}
	return nil
}

func (t *inferenceHandler) modelAcquisition(ctx context.Context) (*modelacquisition.Service, error) {
	var registry modelregistry.Registry = modelregistry.NewCurated()
	var installer modelacquisition.Installer = modelacquisition.StoreInstaller{}
	if source, ok := t.roles.ModelFactory().(interface {
		Acquisition() (modelregistry.Registry, modelacquisition.Installer)
	}); ok {
		registry, installer = source.Acquisition()
	}
	return modelacquisition.New(t.deps.DB, t.roles, t.state, registry, installer, modeldconn.DataRootFromContext(ctx), t.tracker())
}
