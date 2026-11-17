package inferencesetup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

// ContextLengthRequiredError reports a fill-in-the-middle declaration for a
// model whose connection does not report a context length and whose
// declaration states none.
type ContextLengthRequiredError struct {
	Connection string
	Model      string
}

// Error implements error.
func (e *ContextLengthRequiredError) Error() string {
	return fmt.Sprintf("the context length of model %q on connection %q is not reported and the declaration states none", e.Model, e.Connection)
}

// CompletionDeclaration states how one model completes code on one connection.
// Mode is instruct, prompt or fim; FIMFormat names the native format and
// applies to fim alone. ContextLength states the model's context window for fim
// when its connection does not report one.
type CompletionDeclaration struct {
	Provider      string
	Model         string
	Mode          string
	FIMFormat     string
	ContextLength int
}

// DeclareCompletionContract records the code-completion contract of one model:
// the chat completion chain (instruct), one bounded prompt operation (prompt),
// or a native fill-in-the-middle format (fim). Instruct and prompt declare
// through [Service.DeclareCompletion]; fim registers the model on its connection
// through [Service.RegisterModel], keeping the capabilities and facts already
// declared or observed for it. The declaration neither verifies nor assigns the
// route.
func (s *Service) DeclareCompletionContract(ctx context.Context, declaration CompletionDeclaration) error {
	declaration.Provider, declaration.Model = strings.TrimSpace(declaration.Provider), strings.TrimSpace(declaration.Model)
	declaration.Mode, declaration.FIMFormat = strings.TrimSpace(declaration.Mode), strings.TrimSpace(declaration.FIMFormat)
	switch declaration.Mode {
	case modelregistry.CompletionInstruct, modelregistry.CompletionPrompt:
		if declaration.FIMFormat != "" || declaration.ContextLength != 0 {
			return fmt.Errorf("a fill-in-the-middle format and a context length apply to %s alone", modelregistry.CompletionFIM)
		}
		return s.DeclareCompletion(ctx, declaration.Provider, declaration.Model, declaration.Mode)
	case modelregistry.CompletionFIM:
		return s.declareFIM(ctx, declaration)
	}
	return fmt.Errorf("completion mode %q is not one of %s, %s, %s", declaration.Mode, modelregistry.CompletionInstruct, modelregistry.CompletionPrompt, modelregistry.CompletionFIM)
}

func (s *Service) declareFIM(ctx context.Context, declaration CompletionDeclaration) error {
	if declaration.Provider == "" || declaration.Model == "" {
		return errors.New("completion declaration requires a provider and model")
	}
	if !slices.Contains(modelcontract.FIMFormats(), declaration.FIMFormat) {
		return fmt.Errorf("fill-in-the-middle format %q is not one of %s", declaration.FIMFormat, strings.Join(modelcontract.FIMFormats(), ", "))
	}
	if declaration.ContextLength < 0 {
		return fmt.Errorf("context length %d is negative", declaration.ContextLength)
	}
	var connection *runtimestate.BackendRuntimeState
	var observed *runtimestate.ModelPullStatus
	for _, backend := range s.state.Get(ctx) {
		if !runtimestate.MatchesBackend(declaration.Provider, backend.Backend) {
			continue
		}
		if connection != nil && connection.ID != backend.ID {
			return fmt.Errorf("%q matches several connections; name one", declaration.Provider)
		}
		current := backend
		connection = &current
	}
	if connection == nil {
		return &ConnectionUnavailableError{Connection: declaration.Provider, Reason: "no connection of that name has been discovered"}
	}
	if connection.Backend.Type == "modeld" || connection.Backend.Type == "local" {
		return fmt.Errorf("a local model carries its completion contract in its downloaded artifact; %q on %q has nothing to declare", declaration.Model, declaration.Provider)
	}
	for index := range connection.PulledModels {
		candidate := &connection.PulledModels[index]
		if candidate.Model == declaration.Model || candidate.Model == "" && candidate.Name == declaration.Model {
			observed = candidate
		}
	}
	declared, err := runtimetypes.New(s.db.WithoutTransaction()).GetLLMProviderModelFacts(ctx, connection.ID)
	if err != nil {
		return err
	}
	facts := declared[declaration.Model]
	facts.Registered = true
	facts.CompletionMode, facts.FIMFormat = modelregistry.CompletionFIM, declaration.FIMFormat
	switch {
	case declaration.ContextLength > 0:
		facts.ContextLength = declaration.ContextLength
	case facts.ContextLength > 0:
	case observed != nil && observed.ContextLength > 0:
		facts.ContextLength = observed.ContextLength
	default:
		return &ContextLengthRequiredError{Connection: declaration.Provider, Model: declaration.Model}
	}
	if len(facts.Capabilities) == 0 && observed != nil {
		facts.Capabilities = observedCapabilities(*observed)
		if slices.Contains(facts.Capabilities, runtimetypes.CapabilityNextEdit) {
			facts.NextEditFormat = observed.NextEditFormat
		}
	}
	if !slices.Contains(facts.Capabilities, runtimetypes.CapabilityAutocomplete) {
		facts.Capabilities = append(slices.Clone(facts.Capabilities), runtimetypes.CapabilityAutocomplete)
	}
	return s.RegisterModel(ctx, RegisterModelRequest{ConnectionID: connection.ID, Model: declaration.Model, Facts: facts})
}

func observedCapabilities(model runtimestate.ModelPullStatus) []string {
	var capabilities []string
	add := func(present bool, capability string) {
		if present {
			capabilities = append(capabilities, capability)
		}
	}
	add(model.CanPrompt, runtimetypes.CapabilityCompletion)
	add(model.CanChat && !model.CanPrompt, runtimetypes.CapabilityTools)
	add(model.CanEmbed, runtimetypes.CapabilityEmbedding)
	add(model.CanVision, runtimetypes.CapabilityVision)
	add(model.CanThink, runtimetypes.CapabilityThinking)
	add(model.CanAudio, runtimetypes.CapabilityAudio)
	add(model.CanNextEdit && model.NextEditFormat != "", runtimetypes.CapabilityNextEdit)
	return capabilities
}
