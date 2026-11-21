package inferencesetup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
)

// RegistrationContract describes explicit model registration independently of catalog discovery or verification.
var RegistrationContract = servicecontract.Contract{
	ID: "inference-model-registration", Summary: "Register a model on this connection",
	Description: "Declare a model identity, context and operation capabilities when this endpoint has no usable model catalog. Registration is scoped to this connection. Each assigned role still requires production verification.",
	Operations: []servicecontract.Operation{{
		ID: "register", Summary: "Save model declaration",
		Spec: `Set a manual provider/model override.

The --think flag records whether this provider/model supports reasoning request
controls, and --vision whether it accepts image input. Both are provider-level:
they apply to every backend of that type. A flag you omit leaves its setting as
it was.

--context, --max-output, --capabilities and the pricing flags are backend-level.
Pass --backend to write them to one backend, or let them fan out to every
backend of the given provider type. A capability list REPLACES the observed set
rather than adding to it, so --capabilities states the whole set.

--completion declares how the model completes code: instruct runs the chat
completion chain, prompt runs one bounded prompt operation, and fim runs the
model's native fill-in-the-middle tokens in the format --fim-format names (the
formats are those the runtime declares). A fim declaration also states
--context when the connection does not report the model's context window, keeps
the capabilities already declared or observed for the model, and adds
autocomplete. --completion stands alone: it declares the contract and does not
verify or assign the model, which the autocomplete role assignment then does
with a real completion request.

Pricing is USD per million tokens and is what the monthly spend ceiling is
metered against: a model with no declared rate card costs nothing as far as the
meter is concerned.`,
		Inputs: []servicecontract.Input{{
			ID: "registration", Summary: "Connection id, model identity and the declared facts and capabilities", Type: "object", Required: true,
		}},
		Output: servicecontract.Output{
			Stdout: "The model identity and the facts now recorded for it.",
			Format: "text",
		},
		Outcomes: []servicecontract.Outcome{
			{Code: 0, When: "the declaration is stored", Meaning: "every surface reading this connection sees the declared facts"},
		},
		Refusals: []servicecontract.Refusal{
			{When: "the connection does not exist", Message: "names the connection and the command that lists them"},
			{When: "a capability list is scoped to one backend", Message: "refuses the partial scope rather than writing facts to a single backend"},
		},
	}},
}

// RegisterModelRequest declares one model on an existing connection.
type RegisterModelRequest struct {
	ConnectionID string                  `json:"connectionId"`
	Model        string                  `json:"model"`
	Facts        runtimetypes.ModelFacts `json:"facts"`
}

// RegisterModel saves a connection-scoped model declaration without issuing a discovery request.
func (s *Service) RegisterModel(ctx context.Context, request RegisterModelRequest) error {
	request.ConnectionID, request.Model = strings.TrimSpace(request.ConnectionID), strings.TrimSpace(request.Model)
	if request.ConnectionID == "" || request.Model == "" || !request.Facts.Registered {
		return errors.New("model registration requires a connection, model identity and explicit registration")
	}
	if err := runtimetypes.ValidateModelFacts(request.Facts); err != nil {
		return err
	}
	tx, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	store := runtimetypes.New(tx)
	backend, err := store.GetBackend(ctx, request.ConnectionID)
	if err != nil {
		return err
	}
	if backend.Type == "modeld" || backend.Type == "local" {
		return errors.New("native model registration requires installed artifacts; acquire the model from its modeld connection")
	}
	capabilities, err := runtimestate.DeclaredModelCapabilities(*backend, request.Facts)
	if err != nil {
		return err
	}
	for _, capability := range request.Facts.Capabilities {
		if !runtimestate.ModelCapabilityIsAdapterOperation(capability) {
			continue
		}
		if !slices.Contains(capabilities, capability) {
			return fmt.Errorf("%s adapter does not execute %s for this model's declared formats; select a connection supporting that operation", backend.Type, capability)
		}
	}
	if (request.Facts.CompletionMode == "instruct" || request.Facts.CompletionMode == "prompt") && !slices.Contains(request.Facts.Capabilities, "completion") {
		return errors.New("instructed completion requires the completion capability")
	}
	if backend.Type == "ollama" && slices.Contains(request.Facts.Capabilities, "audio") && !request.Facts.AudioExtension {
		return errors.New("Ollama audio requires an explicit declaration that this endpoint supports the inline audio extension")
	}
	if request.Facts.AudioExtension && backend.Type != "ollama" {
		return errors.New("the inline audio extension applies only to Ollama-compatible endpoints")
	}
	if err := store.SetLLMProviderModelFact(ctx, backend.ID, request.Model, request.Facts); err != nil {
		return err
	}
	if err := store.UpdateBackend(ctx, backend); err != nil {
		return err
	}
	if err := commit(ctx); err != nil {
		return err
	}
	if err := s.state.ReconcileDeclarations(ctx, backend.ID); err != nil {
		return err
	}
	return s.invalidateRoles(ctx, runtimestate.ConnectionSelector(backend.ID), request.Model, RoleIDs())
}
