package acpsvc

import (
	"time"

	"github.com/contenox/runtime/internal/modeld/capacity"
	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/internal/setupcheck"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelhardware"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/providerservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

// inferenceResultWire maps one inference answer onto its declared message.
func inferenceResultWire(snapshot inferenceSnapshot) *protocolv1.InferenceResult {
	return &protocolv1.InferenceResult{
		AcquisitionContract:   serviceContractWire(snapshot.AcquisitionContract),
		RegistrationContract:  serviceContractWire(snapshot.RegistrationContract),
		ConfigurationContract: serviceContractWire(snapshot.ConfigurationContract),
		CompletionContract:    serviceContractWire(snapshot.CompletionContract),
		EditorContract:        serviceContractWire(snapshot.EditorContract),
		ProviderResult:        inferenceProviderResultWire(snapshot.ProviderResult),
		AutomaticSetup:        inferenceAutomaticSetupWire(snapshot.AutomaticSetup),
		Readiness:             inferenceReadinessWire(snapshot.Readiness),
		ProposalRevision:      inferenceProposalRevisionWire(snapshot.ProposalRevision),
		Roles:                 inferenceRolesWire(snapshot.Roles),
		Models:                inferenceModelsWire(snapshot.Models),
		Providers:             inferenceProvidersWire(snapshot.Providers),
		ProviderTypes:         inferenceProviderTypesWire(snapshot.ProviderTypes),
		ReviewedProposals:     inferenceRouteProposalsWire(snapshot.ReviewedProposals),
		Ceilings:              inferencePoolCeilingsWire(snapshot.Ceilings),
	}
}

// inferenceRolesWire maps the effective role assignments onto their declared
// messages, always as a list so an unassigned role renders an empty one.
func inferenceRolesWire(roles []inferenceRole) []*protocolv1.InferenceRole {
	out := make([]*protocolv1.InferenceRole, 0, len(roles))
	for _, role := range roles {
		out = append(out, inferenceRoleWire(role))
	}
	return out
}

func inferenceRoleWire(role inferenceRole) *protocolv1.InferenceRole {
	return &protocolv1.InferenceRole{
		Verification:    inferenceVerificationWire(role.Verification),
		AssignmentState: role.AssignmentState, ConnectionId: role.ConnectionID, Revision: role.Revision,
		Inputs: string(role.Inputs), Id: role.ID, Name: role.Name, Provider: role.Provider, Model: role.Model,
		State: role.State, Detail: role.Detail, Problem: role.Problem, Inherited: role.Inherited, Enabled: role.Enabled, Source: string(role.Source),
	}
}

func inferenceVerificationWire(verification *inferencesetup.Verification) *protocolv1.InferenceVerification {
	if verification == nil {
		return nil
	}
	return &protocolv1.InferenceVerification{
		Inputs:                string(verification.Inputs),
		NativeExecutions:      inferenceSessionExecutionsWire(verification.NativeExecutions),
		ConfigurationRevision: verification.ConfigurationRevision, RoleRevision: verification.RoleRevision,
		Role: verification.Role, Provider: verification.Provider, Model: verification.Model,
		Fingerprint: verification.Fingerprint, Dimension: int32(verification.Dimension),
		VerifiedAt: verification.VerifiedAt.Format(time.RFC3339Nano), Sample: verification.Sample,
	}
}

func inferenceSessionExecutionsWire(executions []transport.SessionExecution) []*protocolv1.InferenceSessionExecution {
	out := make([]*protocolv1.InferenceSessionExecution, 0, len(executions))
	for _, execution := range executions {
		out = append(out, inferenceSessionExecutionWire(execution))
	}
	return out
}

func inferenceSessionExecutionWire(execution transport.SessionExecution) *protocolv1.InferenceSessionExecution {
	return &protocolv1.InferenceSessionExecution{
		Requested: workerLaunchConfigWire(execution.Requested), Resolved: workerLaunchConfigWire(execution.Resolved),
		Capacity: workerModelInfoWire(&execution.Capacity), OwnerEpoch: execution.OwnerEpoch,
		WorkerIdentity: execution.WorkerIdentity, ModelName: execution.ModelName, Digest: execution.Digest,
		Backend: execution.Backend, DeviceSelector: execution.DeviceSelector,
	}
}

func inferenceProviderResultWire(result *inferencesetup.ProviderResult) *protocolv1.InferenceProviderResult {
	if result == nil {
		return nil
	}
	return &protocolv1.InferenceProviderResult{Id: result.ID, DiscoveryError: result.DiscoveryError}
}

func inferenceModelsWire(models []inferencesetup.AvailableModel) []*protocolv1.InferenceAvailableModel {
	out := make([]*protocolv1.InferenceAvailableModel, 0, len(models))
	for _, model := range models {
		out = append(out, &protocolv1.InferenceAvailableModel{
			DeclaredCapabilities: model.DeclaredCapabilities, AdvertisedCapabilities: model.AdvertisedCapabilities,
			AdapterCapabilities: model.AdapterCapabilities, Digest: model.Digest, NativeBackend: model.NativeBackend,
			Provider: model.Provider, Name: model.Name, FimFormat: model.FIMFormat,
			NextEditFormat: model.NextEditFormat, CompletionMode: model.CompletionMode,
			ContextTokens: int32(model.ContextTokens), Registered: model.Registered, Vision: model.Vision,
			Audio: model.Audio, Chat: model.Chat, Embed: model.Embed, Prompt: model.Prompt,
			Autocomplete: model.Autocomplete, NextEdit: model.NextEdit,
		})
	}
	return out
}

func inferenceProvidersWire(providers []inferencesetup.ProviderConnection) []*protocolv1.InferenceProviderConnection {
	out := make([]*protocolv1.InferenceProviderConnection, 0, len(providers))
	for _, provider := range providers {
		out = append(out, &protocolv1.InferenceProviderConnection{
			RegistrationCapabilities: provider.RegistrationCapabilities, CredentialSource: provider.CredentialSource,
			AuthorizationState: provider.AuthorizationState, AuthorizationError: provider.AuthorizationError,
			DiscoveryState: provider.DiscoveryState, Revision: provider.Revision, Selector: provider.Selector,
			Id: provider.ID, Name: provider.Name, Type: provider.Type, Url: provider.URL,
			State: provider.State, Error: provider.Error,
		})
	}
	return out
}

func inferenceProviderTypesWire(types []providerservice.ProviderCapability) []*protocolv1.InferenceProviderCapability {
	out := make([]*protocolv1.InferenceProviderCapability, 0, len(types))
	for _, capability := range types {
		wire := &protocolv1.InferenceProviderCapability{
			Provider: capability.Provider, Label: capability.Provider, DefaultBaseUrl: capability.DefaultBaseURL,
			RecommendedApiKeyEnv: capability.RecommendedAPIKeyEnv,
			RequiresBaseUrl:      capability.RequiresBaseURL, RequiresSecretConfig: capability.RequiresSecretConfig,
		}
		if capability.Provider == providerservice.ProviderTypeModeld || capability.Provider == providerservice.ProviderTypeLocal {
			wire.Label = "Local models · " + capability.Provider
			wire.Description = "Connect to a local or remote Modeld owner."
			wire.AuthKind, wire.DefaultCredentialSource = "none", "none"
			wire.CredentialSources = []*protocolv1.InferenceCredentialSource{{Id: "none", Label: "No credentials required"}}
			wire.AutocompleteFormats = []string{"qwen2.5-coder", "starcoder"}
		}
		out = append(out, wire)
	}
	return out
}

func inferenceReadinessWire(readiness *setupcheck.Result) *protocolv1.InferenceReadiness {
	if readiness == nil {
		return nil
	}
	issues := make([]*protocolv1.InferenceReadinessIssue, 0, len(readiness.Issues))
	for _, issue := range readiness.Issues {
		issues = append(issues, &protocolv1.InferenceReadinessIssue{
			Code: issue.Code, Severity: issue.Severity,
			Category: issue.Category, Message: issue.Message, FixPath: issue.FixPath, CliCommand: issue.CLICommand,
		})
	}
	checks := make([]*protocolv1.InferenceBackendCheck, 0, len(readiness.BackendChecks))
	for _, check := range readiness.BackendChecks {
		checks = append(checks, &protocolv1.InferenceBackendCheck{
			ChatModels: check.ChatModels, Id: check.ID, Name: check.Name, Type: check.Type, BaseUrl: check.BaseURL,
			Status: check.Status, Error: check.Error, Hint: check.Hint,
			ModelCount: int32(check.ModelCount), ChatModelCount: int32(check.ChatModelCount),
			Reachable: check.Reachable, DefaultProvider: check.DefaultProvider,
		})
	}
	return &protocolv1.InferenceReadiness{
		Issues: issues, BackendChecks: checks, ResolvedFrom: readiness.ResolvedFrom,
		DefaultModel: readiness.DefaultModel, DefaultProvider: readiness.DefaultProvider,
		DefaultChain: readiness.DefaultChain, HitlPolicyName: readiness.HITLPolicyName,
		DefaultMaxOutputTokens: int32(readiness.DefaultMaxOutputTokens),
		BackendCount:           int32(readiness.BackendCount),
		ReachableBackendCount:  int32(readiness.ReachableBackendCount),
	}
}

func inferenceAutomaticSetupWire(setup *inferenceAutomaticSetup) *protocolv1.InferenceAutomaticSetup {
	if setup == nil {
		return nil
	}
	return &protocolv1.InferenceAutomaticSetup{
		Plan: inferencePlanWire(setup.Plan), Contract: serviceContractWire(setup.Contract),
		State: setup.State, HotContext: int32(setup.HotContext),
	}
}

func inferencePlanWire(plan autosetup.Plan) *protocolv1.InferencePlan {
	roles := make([]*protocolv1.InferenceRolePlan, 0, len(plan.Roles))
	for _, role := range plan.Roles {
		roles = append(roles, inferenceRolePlanWire(role))
	}
	assignments := make([]*protocolv1.InferencePlannedRole, 0, len(plan.Assignments))
	for _, assignment := range plan.Assignments {
		assignments = append(assignments, &protocolv1.InferencePlannedRole{
			Role: assignment.Role, Provider: assignment.Provider, Model: assignment.Model,
		})
	}
	return &protocolv1.InferencePlan{
		Roles: roles, Mode: plan.Mode, Baseline: inferencePoolEstimatesWire(plan.Baseline),
		Models: modelDescriptorsWire(plan.Models), Hardware: inferenceHardwareWire(plan.Hardware),
		Model: modelDescriptorWire(plan.Model), Backend: plan.Backend, Reused: plan.Reused,
		EstimatedHotContext: int32(plan.EstimatedHotContext), Fingerprint: plan.Fingerprint,
		Assignments: assignments, Ceilings: inferencePoolCeilingsWire(plan.Ceilings),
	}
}

func inferenceRolePlanWire(plan autosetup.RolePlan) *protocolv1.InferenceRolePlan {
	alternatives := make([]*protocolv1.InferenceRoleCandidate, 0, len(plan.Alternatives))
	for _, candidate := range plan.Alternatives {
		alternatives = append(alternatives, inferenceRoleCandidateWirePtr(&candidate))
	}
	return &protocolv1.InferenceRolePlan{
		Constraint: inferenceRoleConstraintWire(plan.Constraint), State: plan.State,
		Selected: inferenceRoleCandidateWirePtr(plan.Selected), Alternatives: alternatives, Repairs: plan.Repairs,
	}
}

func inferenceRoleCandidateWirePtr(candidate *autosetup.RoleCandidate) *protocolv1.InferenceRoleCandidate {
	if candidate == nil {
		return nil
	}
	encoded := inferenceRoleCandidateWire(*candidate)
	return &encoded
}

func inferenceRoleCandidateWire(candidate autosetup.RoleCandidate) protocolv1.InferenceRoleCandidate {
	return protocolv1.InferenceRoleCandidate{
		Route: inferenceRouteProposalWire(candidate.Route), Artifact: modelDescriptorWirePtr(candidate.Artifact),
		ExecutionSettings: workerOptionsWire(candidate.ExecutionSettings),
		Pools:             inferencePoolEstimatesWire(candidate.Pools), CompletionMode: candidate.CompletionMode,
		ConnectionId: candidate.ConnectionID, Endpoint: candidate.Endpoint, Scheduling: candidate.Scheduling,
		Reason: candidate.Reason, ReviewError: candidate.ReviewError, ThreadLimit: int32(candidate.ThreadLimit),
		ContextTokens: int32(candidate.ContextTokens), Native: candidate.Native, Installed: candidate.Installed,
		SharedMemory: candidate.SharedMemory, Verified: candidate.Verified,
	}
}

func inferencePoolEstimatesWire(pools []autosetup.PoolEstimate) []*protocolv1.InferencePoolEstimate {
	out := make([]*protocolv1.InferencePoolEstimate, 0, len(pools))
	for _, pool := range pools {
		out = append(out, &protocolv1.InferencePoolEstimate{
			Host: pool.Host, PoolId: pool.PoolID, Bytes: pool.Bytes, AvailableBytes: pool.AvailableBytes,
			CeilingBytes: pool.CeilingBytes,
		})
	}
	return out
}

func inferenceRoleConstraintWire(constraint autosetup.RoleConstraint) *protocolv1.InferenceRoleConstraint {
	return &protocolv1.InferenceRoleConstraint{
		Role: constraint.Role, Policy: constraint.Policy, ConnectionId: constraint.ConnectionID,
		AllowedConnections: constraint.AllowedConnections, Inputs: string(constraint.Inputs),
		ExecutionMode: constraint.ExecutionMode, Importance: string(constraint.Importance),
	}
}

func inferencePoolCeilingsWire(ceilings []autosetup.PoolCeiling) []*protocolv1.InferencePoolCeiling {
	out := make([]*protocolv1.InferencePoolCeiling, 0, len(ceilings))
	for _, ceiling := range ceilings {
		out = append(out, &protocolv1.InferencePoolCeiling{ConnectionId: ceiling.ConnectionID, PoolId: ceiling.PoolID, Share: ceiling.Share})
	}
	return out
}

func inferencePoolCeilingsEngine(ceilings []*protocolv1.InferencePoolCeiling) []autosetup.PoolCeiling {
	if len(ceilings) == 0 {
		return nil
	}
	out := make([]autosetup.PoolCeiling, 0, len(ceilings))
	for _, ceiling := range ceilings {
		out = append(out, autosetup.PoolCeiling{ConnectionID: ceiling.GetConnectionId(), PoolID: ceiling.GetPoolId(), Share: ceiling.GetShare()})
	}
	return out
}

func inferenceHardwareWire(hardware modelhardware.Hardware) *protocolv1.InferenceHardware {
	devices := make([]*protocolv1.InferenceHardwareDevice, 0, len(hardware.Devices))
	for _, device := range hardware.Devices {
		devices = append(devices, &protocolv1.InferenceHardwareDevice{
			Vendor: device.Vendor, Name: device.Name, Kind: device.Kind,
			TotalBytes: device.TotalBytes, AvailableBytes: device.AvailableBytes,
			MemoryKnown: device.MemoryKnown, Source: device.Source,
			SharedTotalBytes: device.SharedTotalBytes, SharedAvailableBytes: device.SharedAvailableBytes,
			SharedMemoryKnown: device.SharedMemoryKnown,
		})
	}
	return &protocolv1.InferenceHardware{
		MemoryBytes: hardware.MemoryBytes, PreferredBackend: hardware.PreferredBackend, Kind: hardware.Kind,
		Label: hardware.Label, TotalBytes: hardware.TotalBytes, AvailableBytes: hardware.AvailableBytes,
		Known: hardware.Known, SystemTotalBytes: hardware.SystemTotalBytes,
		SystemAvailableBytes: hardware.SystemAvailableBytes, Devices: devices, Summary: hardware.Summary,
	}
}

func inferenceRouteProposalsWire(proposals []inferencesetup.RouteProposal) []*protocolv1.InferenceRouteProposal {
	out := make([]*protocolv1.InferenceRouteProposal, 0, len(proposals))
	for _, proposal := range proposals {
		out = append(out, inferenceRouteProposalWire(proposal))
	}
	return out
}

func inferenceRouteProposalWire(proposal inferencesetup.RouteProposal) *protocolv1.InferenceRouteProposal {
	return &protocolv1.InferenceRouteProposal{
		Settings: workerOptionsWire(proposal.Settings), Inputs: string(proposal.Inputs), Role: proposal.Role,
		Provider: proposal.Provider, Model: proposal.Model, RoleRevision: proposal.RoleRevision,
		ConnectionRevision: proposal.ConnectionRevision, Fingerprint: proposal.Fingerprint,
		SettingsChange: string(proposal.SettingsChange),
	}
}

func inferenceProposalRevisionWire(revision *inferencesetup.ProposalRevision) *protocolv1.InferenceProposalRevision {
	if revision == nil {
		return nil
	}
	return &protocolv1.InferenceProposalRevision{
		Execution: inferenceSessionExecutionWire(revision.Execution),
		Proposals: inferenceRouteProposalsWire(revision.Proposals), Reason: revision.Reason,
	}
}

func modelDescriptorsWire(descriptors []modelregistry.ModelDescriptor) []*protocolv1.ModelDescriptor {
	out := make([]*protocolv1.ModelDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		out = append(out, modelDescriptorWire(descriptor))
	}
	return out
}

func modelDescriptorWirePtr(descriptor *modelregistry.ModelDescriptor) *protocolv1.ModelDescriptor {
	if descriptor == nil {
		return nil
	}
	return modelDescriptorWire(*descriptor)
}

func modelDescriptorWire(descriptor modelregistry.ModelDescriptor) *protocolv1.ModelDescriptor {
	return &protocolv1.ModelDescriptor{
		Completion:    inferenceCompletionProfileWire(descriptor.Completion),
		NextEdit:      inferenceNextEditProfileWire(descriptor.NextEdit),
		Embedding:     inferenceEmbeddingProfileWire(descriptor.Embedding),
		AutoKvProfile: inferenceLayerKVProfileWire(descriptor.AutoKVProfile),
		Id:            descriptor.ID, Name: descriptor.Name, Origin: descriptor.Origin,
		ReleasedAt: descriptor.ReleasedAt, License: descriptor.License, SourceUrl: descriptor.SourceURL,
		SourceSha256: descriptor.SourceSHA256, Repo: descriptor.Repo, Revision: descriptor.Revision,
		Backend: descriptor.Backend, TokenizerCompanionId: descriptor.TokenizerCompanionID,
		ToolProtocol: descriptor.ToolProtocol, ReasoningProtocol: descriptor.ReasoningProtocol,
		ReasoningFormat: descriptor.ReasoningFormat, Family: descriptor.Family,
		DisplayLabel: descriptor.DisplayLabel, UseCase: descriptor.UseCase, AgenticTier: descriptor.AgenticTier,
		AgenticEvidence: descriptor.AgenticEvidence, MmprojUrl: descriptor.MMProjURL,
		ChatTemplateUrl: descriptor.ChatTemplateURL, Notes: descriptor.Notes,
		SizeBytes: descriptor.SizeBytes, MmprojSizeBytes: descriptor.MMProjSizeBytes,
		AutoContextLimit:  int32(descriptor.AutoContextLimit),
		RecommendedVramGb: int32(descriptor.RecommendedVRAMGB),
		Curated:           descriptor.Curated, AutoEligible: descriptor.AutoEligible, Vision: descriptor.Vision,
		AutoAcquirable: descriptor.AutoAcquirable(), ServesRoles: inferencesetup.RolesServed(descriptor),
	}
}

func inferenceCompletionProfileWire(profile modelregistry.CompletionProfile) *protocolv1.InferenceCompletionProfile {
	return &protocolv1.InferenceCompletionProfile{
		FimFormat: profile.FIMFormat, Mode: profile.Mode, Evidence: profile.Evidence,
		ContextTokens: int32(profile.ContextTokens),
	}
}

func inferenceNextEditProfileWire(profile modelregistry.NextEditProfile) *protocolv1.InferenceNextEditProfile {
	return &protocolv1.InferenceNextEditProfile{
		Format: profile.Format, Evidence: profile.Evidence, ContextTokens: int32(profile.ContextTokens),
	}
}

func inferenceEmbeddingProfileWire(profile modelregistry.EmbeddingProfile) *protocolv1.InferenceEmbeddingProfile {
	return &protocolv1.InferenceEmbeddingProfile{
		Pooling: profile.Pooling, QueryPrefix: profile.QueryPrefix, DocumentPrefix: profile.DocumentPrefix,
		Evidence: profile.Evidence, ContextTokens: int32(profile.ContextTokens),
		Dimension: int32(profile.Dimension), Normalize: profile.Normalize,
	}
}

func inferenceLayerKVProfileWire(profile capacity.LayerKVProfile) *protocolv1.InferenceLayerKVProfile {
	return &protocolv1.InferenceLayerKVProfile{
		GlobalLayers: int32(profile.GlobalLayers), WindowedLayers: int32(profile.WindowedLayers),
		Window: int32(profile.Window), PerLayerKvBytes: profile.PerLayerKVBytes,
	}
}

// inferenceAcquisitionEngine decodes the declared acquisition selection into
// the engine's own request.
func inferenceAcquisitionEngine(request *protocolv1.InferenceAcquisitionRequest) modelacquisition.Request {
	if request == nil {
		return modelacquisition.Request{}
	}
	return modelacquisition.Request{
		Target: request.GetTarget(), ConnectionID: request.GetConnectionId(), Model: request.GetModel(),
	}
}

// inferenceNativePrerequisiteEngine decodes the declared native prerequisite.
func inferenceNativePrerequisiteEngine(prerequisite *protocolv1.InferenceNativePrerequisite) autosetup.NativePrerequisite {
	if prerequisite == nil {
		return autosetup.NativePrerequisite{}
	}
	return autosetup.NativePrerequisite{Backend: prerequisite.GetBackend()}
}

// inferenceRegistrationEngine decodes the declared model registration.
func inferenceRegistrationEngine(registration *protocolv1.InferenceModelRegistration) inferencesetup.RegisterModelRequest {
	if registration == nil {
		return inferencesetup.RegisterModelRequest{}
	}
	return inferencesetup.RegisterModelRequest{
		ConnectionID: registration.GetConnectionId(), Model: registration.GetModel(),
		Facts: inferenceModelFactsEngine(registration.GetFacts()),
	}
}

func inferenceModelFactsEngine(facts *protocolv1.InferenceModelFacts) runtimetypes.ModelFacts {
	if facts == nil {
		return runtimetypes.ModelFacts{}
	}
	out := runtimetypes.ModelFacts{
		Registered: facts.GetRegistered(), CompletionMode: facts.GetCompletionMode(),
		FIMFormat: facts.GetFimFormat(), NextEditFormat: facts.GetNextEditFormat(),
		AudioExtension: facts.GetAudioExtension(), ContextLength: int(facts.GetContextLength()),
		MaxOutputTokens: int(facts.GetMaxOutputTokens()), Capabilities: facts.GetCapabilities(),
	}
	if pricing := facts.GetPricing(); pricing != nil {
		out.Pricing = &runtimetypes.ModelPricing{
			InputPerMillion: pricing.GetInputPerMillion(), CacheReadPerMillion: pricing.GetCacheReadPerMillion(),
			CacheWritePerMillion: pricing.GetCacheWritePerMillion(), OutputPerMillion: pricing.GetOutputPerMillion(),
			PerImage: pricing.GetPerImage(), PerAudioMebibyte: pricing.GetPerAudioMebibyte(),
		}
	}
	return out
}

// inferencePlanEngine decodes the reviewed automatic plan a request carries
// back into the engine's own projection.
func inferencePlanEngine(plan *protocolv1.InferencePlan) autosetup.Plan {
	if plan == nil {
		return autosetup.Plan{}
	}
	roles := make([]autosetup.RolePlan, 0, len(plan.GetRoles()))
	for _, role := range plan.GetRoles() {
		roles = append(roles, inferenceRolePlanEngine(role))
	}
	assignments := make([]autosetup.PlannedRole, 0, len(plan.GetAssignments()))
	for _, assignment := range plan.GetAssignments() {
		assignments = append(assignments, autosetup.PlannedRole{
			Role: assignment.GetRole(), Provider: assignment.GetProvider(), Model: assignment.GetModel(),
		})
	}
	return autosetup.Plan{
		Roles: roles, Mode: plan.GetMode(), Baseline: inferencePoolEstimatesEngine(plan.GetBaseline()),
		Models: modelDescriptorsEngine(plan.GetModels()), Hardware: inferenceHardwareEngine(plan.GetHardware()),
		Model: modelDescriptorEngine(plan.GetModel()), Backend: plan.GetBackend(), Reused: plan.GetReused(),
		EstimatedHotContext: int(plan.GetEstimatedHotContext()), Fingerprint: plan.GetFingerprint(),
		Assignments: assignments, Ceilings: inferencePoolCeilingsEngine(plan.GetCeilings()),
	}
}

func inferenceRolePlanEngine(plan *protocolv1.InferenceRolePlan) autosetup.RolePlan {
	if plan == nil {
		return autosetup.RolePlan{}
	}
	alternatives := make([]autosetup.RoleCandidate, 0, len(plan.GetAlternatives()))
	for _, candidate := range plan.GetAlternatives() {
		alternatives = append(alternatives, inferenceRoleCandidateEngine(candidate))
	}
	return autosetup.RolePlan{
		Constraint: inferenceRoleConstraintEngine(plan.GetConstraint()), State: plan.GetState(),
		Selected: inferenceRoleCandidateEnginePtr(plan.GetSelected()), Alternatives: alternatives,
		Repairs: plan.GetRepairs(),
	}
}

func inferenceRoleCandidateEnginePtr(candidate *protocolv1.InferenceRoleCandidate) *autosetup.RoleCandidate {
	if candidate == nil {
		return nil
	}
	decoded := inferenceRoleCandidateEngine(candidate)
	return &decoded
}

func inferenceRoleCandidateEngine(candidate *protocolv1.InferenceRoleCandidate) autosetup.RoleCandidate {
	if candidate == nil {
		return autosetup.RoleCandidate{}
	}
	return autosetup.RoleCandidate{
		CompletionMode: candidate.GetCompletionMode(), ThreadLimit: int(candidate.GetThreadLimit()),
		Route: inferenceRouteProposalEngine(candidate.GetRoute()), ConnectionID: candidate.GetConnectionId(),
		Endpoint: candidate.GetEndpoint(), Native: candidate.GetNative(), Installed: candidate.GetInstalled(),
		SharedMemory: candidate.GetSharedMemory(), Artifact: modelDescriptorEnginePtr(candidate.GetArtifact()),
		Pools: inferencePoolEstimatesEngine(candidate.GetPools()), ContextTokens: int(candidate.GetContextTokens()),
		Scheduling: candidate.GetScheduling(), Reason: candidate.GetReason(), Verified: candidate.GetVerified(),
		ReviewError: candidate.GetReviewError(), ExecutionSettings: workerOptionsEngine(candidate.GetExecutionSettings()),
	}
}

func inferencePoolEstimatesEngine(pools []*protocolv1.InferencePoolEstimate) []autosetup.PoolEstimate {
	out := make([]autosetup.PoolEstimate, 0, len(pools))
	for _, pool := range pools {
		out = append(out, autosetup.PoolEstimate{
			Host: pool.GetHost(), PoolID: pool.GetPoolId(), Bytes: pool.GetBytes(),
			AvailableBytes: pool.GetAvailableBytes(), CeilingBytes: pool.GetCeilingBytes(),
		})
	}
	return out
}

func inferenceRoleConstraintEngine(constraint *protocolv1.InferenceRoleConstraint) autosetup.RoleConstraint {
	if constraint == nil {
		return autosetup.RoleConstraint{}
	}
	return autosetup.RoleConstraint{
		Role: constraint.GetRole(), Policy: constraint.GetPolicy(), ConnectionID: constraint.GetConnectionId(),
		AllowedConnections: constraint.GetAllowedConnections(),
		Inputs:             inferencesetup.InputRequirement(constraint.GetInputs()),
		ExecutionMode:      constraint.GetExecutionMode(),
		Importance:         autosetup.Importance(constraint.GetImportance()),
	}
}

func inferenceHardwareEngine(hardware *protocolv1.InferenceHardware) modelhardware.Hardware {
	if hardware == nil {
		return modelhardware.Hardware{}
	}
	devices := make([]modelhardware.Device, 0, len(hardware.GetDevices()))
	for _, device := range hardware.GetDevices() {
		devices = append(devices, modelhardware.Device{
			Vendor: device.GetVendor(), Name: device.GetName(), Kind: device.GetKind(),
			TotalBytes: device.GetTotalBytes(), AvailableBytes: device.GetAvailableBytes(),
			MemoryKnown: device.GetMemoryKnown(), Source: device.GetSource(),
			SharedTotalBytes: device.GetSharedTotalBytes(), SharedAvailableBytes: device.GetSharedAvailableBytes(),
			SharedMemoryKnown: device.GetSharedMemoryKnown(),
		})
	}
	return modelhardware.Hardware{
		MemoryBytes: hardware.GetMemoryBytes(), PreferredBackend: hardware.GetPreferredBackend(),
		Kind: hardware.GetKind(), Label: hardware.GetLabel(), TotalBytes: hardware.GetTotalBytes(),
		AvailableBytes: hardware.GetAvailableBytes(), Known: hardware.GetKnown(),
		SystemTotalBytes: hardware.GetSystemTotalBytes(), SystemAvailableBytes: hardware.GetSystemAvailableBytes(),
		Devices: devices, Summary: hardware.GetSummary(),
	}
}

// inferenceRouteProposalsEngine decodes the reviewed proposals a request
// carries back into the engine's own projections.
func inferenceRouteProposalsEngine(proposals []*protocolv1.InferenceRouteProposal) []inferencesetup.RouteProposal {
	out := make([]inferencesetup.RouteProposal, 0, len(proposals))
	for _, proposal := range proposals {
		out = append(out, inferenceRouteProposalEngine(proposal))
	}
	return out
}

func inferenceRouteProposalEngine(proposal *protocolv1.InferenceRouteProposal) inferencesetup.RouteProposal {
	if proposal == nil {
		return inferencesetup.RouteProposal{}
	}
	return inferencesetup.RouteProposal{
		Assignment: inferencesetup.Assignment{
			Inputs: inferencesetup.InputRequirement(proposal.GetInputs()), Role: proposal.GetRole(),
			Provider: proposal.GetProvider(), Model: proposal.GetModel(),
		},
		RoleRevision: proposal.GetRoleRevision(), ConnectionRevision: proposal.GetConnectionRevision(),
		Fingerprint:    proposal.GetFingerprint(),
		SettingsChange: inferencesetup.SettingsChange(proposal.GetSettingsChange()),
		Settings:       workerOptionsEngine(proposal.GetSettings()),
	}
}

// inferenceRecommendationEngine decodes the declared role review request.
func inferenceRecommendationEngine(recommendation *protocolv1.InferenceRecommendationRequest) autosetup.RecommendationRequest {
	if recommendation == nil {
		return autosetup.RecommendationRequest{}
	}
	constraints := make([]autosetup.RoleConstraint, 0, len(recommendation.GetConstraints()))
	for _, constraint := range recommendation.GetConstraints() {
		constraints = append(constraints, inferenceRoleConstraintEngine(constraint))
	}
	return autosetup.RecommendationRequest{
		Mode: recommendation.GetMode(), Origin: recommendation.GetOrigin(), Constraints: constraints,
		Ceilings: inferencePoolCeilingsEngine(recommendation.GetCeilings()),
	}
}

func modelDescriptorsEngine(descriptors []*protocolv1.ModelDescriptor) []modelregistry.ModelDescriptor {
	out := make([]modelregistry.ModelDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		out = append(out, modelDescriptorEngine(descriptor))
	}
	return out
}

func modelDescriptorEnginePtr(descriptor *protocolv1.ModelDescriptor) *modelregistry.ModelDescriptor {
	if descriptor == nil {
		return nil
	}
	decoded := modelDescriptorEngine(descriptor)
	return &decoded
}

func modelDescriptorEngine(descriptor *protocolv1.ModelDescriptor) modelregistry.ModelDescriptor {
	if descriptor == nil {
		return modelregistry.ModelDescriptor{}
	}
	return modelregistry.ModelDescriptor{
		Completion: inferenceCompletionProfileEngine(descriptor.GetCompletion()),
		NextEdit:   inferenceNextEditProfileEngine(descriptor.GetNextEdit()),
		Embedding:  inferenceEmbeddingProfileEngine(descriptor.GetEmbedding()),
		AutoKVProfile: capacity.LayerKVProfile{
			GlobalLayers:    int(descriptor.GetAutoKvProfile().GetGlobalLayers()),
			WindowedLayers:  int(descriptor.GetAutoKvProfile().GetWindowedLayers()),
			Window:          int(descriptor.GetAutoKvProfile().GetWindow()),
			PerLayerKVBytes: descriptor.GetAutoKvProfile().GetPerLayerKvBytes(),
		},
		Origin: descriptor.GetOrigin(), ReleasedAt: descriptor.GetReleasedAt(), License: descriptor.GetLicense(),
		ID: descriptor.GetId(), Name: descriptor.GetName(), SourceURL: descriptor.GetSourceUrl(),
		SourceSHA256: descriptor.GetSourceSha256(), SizeBytes: descriptor.GetSizeBytes(),
		Curated: descriptor.GetCurated(), Backend: descriptor.GetBackend(), Repo: descriptor.GetRepo(),
		Revision: descriptor.GetRevision(), TokenizerCompanionID: descriptor.GetTokenizerCompanionId(),
		ToolProtocol: descriptor.GetToolProtocol(), ReasoningProtocol: descriptor.GetReasoningProtocol(),
		ReasoningFormat: descriptor.GetReasoningFormat(), Family: descriptor.GetFamily(),
		DisplayLabel: descriptor.GetDisplayLabel(), UseCase: descriptor.GetUseCase(),
		AgenticTier: descriptor.GetAgenticTier(), AgenticEvidence: descriptor.GetAgenticEvidence(),
		RecommendedVRAMGB: int(descriptor.GetRecommendedVramGb()), Vision: descriptor.GetVision(),
		MMProjURL: descriptor.GetMmprojUrl(), MMProjSizeBytes: descriptor.GetMmprojSizeBytes(),
		ChatTemplateURL: descriptor.GetChatTemplateUrl(), Notes: descriptor.GetNotes(),
		AutoContextLimit: int(descriptor.GetAutoContextLimit()), AutoEligible: descriptor.GetAutoEligible(),
	}
}

func inferenceCompletionProfileEngine(profile *protocolv1.InferenceCompletionProfile) modelregistry.CompletionProfile {
	if profile == nil {
		return modelregistry.CompletionProfile{}
	}
	return modelregistry.CompletionProfile{
		FIMFormat: profile.GetFimFormat(), Mode: profile.GetMode(),
		ContextTokens: int(profile.GetContextTokens()), Evidence: profile.GetEvidence(),
	}
}

func inferenceNextEditProfileEngine(profile *protocolv1.InferenceNextEditProfile) modelregistry.NextEditProfile {
	if profile == nil {
		return modelregistry.NextEditProfile{}
	}
	return modelregistry.NextEditProfile{
		Format: profile.GetFormat(), ContextTokens: int(profile.GetContextTokens()), Evidence: profile.GetEvidence(),
	}
}

func inferenceEmbeddingProfileEngine(profile *protocolv1.InferenceEmbeddingProfile) modelregistry.EmbeddingProfile {
	if profile == nil {
		return modelregistry.EmbeddingProfile{}
	}
	return modelregistry.EmbeddingProfile{
		Pooling: profile.GetPooling(), ContextTokens: int(profile.GetContextTokens()),
		Dimension: int(profile.GetDimension()), Normalize: profile.GetNormalize(),
		QueryPrefix: profile.GetQueryPrefix(), DocumentPrefix: profile.GetDocumentPrefix(),
		Evidence: profile.GetEvidence(),
	}
}
