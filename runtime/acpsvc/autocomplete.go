package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/internal/modeld/admission"
	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/internal/remedy"
	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

// extMethodAutocomplete is the fill-in-the-middle extension method.
const extMethodAutocomplete = "_contenox/autocomplete"

// autocompleteResult is the completion one inference produced, before it is
// mapped onto its declared message.
type autocompleteResult struct {
	Kind        string
	Completion  string
	Mode        string
	Fingerprint string
}

// autocompleteOutcome maps one completion outcome onto its declared token.
func autocompleteOutcome(kind string) string {
	if kind == "cancelled" {
		return "cancelled"
	}
	return "completion"
}

// autocompleteResultWire maps one completion onto its declared message.
func autocompleteResultWire(result autocompleteResult) *protocolv1.AutocompleteResult {
	return &protocolv1.AutocompleteResult{
		Kind: autocompleteOutcome(result.Kind), Completion: result.Completion,
		Mode: result.Mode, Fingerprint: result.Fingerprint,
	}
}

func (t *Transport) handleAutocomplete(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	if t.deps.Engine == nil || t.deps.OpenInference == nil {
		if lerr, ok := errSetupRequired().(*libacp.Error); ok {
			return nil, lerr
		}
		return nil, libacp.InternalError("engine is not configured")
	}
	p := &protocolv1.AutocompleteRequest{}
	if rpcErr := unmarshalProtocolRequest("autocomplete", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.GetPrefix()) == "" && strings.TrimSpace(p.GetSuffix()) == "" {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "prefix or suffix is required")
	}
	contextSize := 0
	for _, snippet := range p.GetCodeContext() {
		contextSize += len(snippet.GetContent())
		if len(snippet.GetPath()) > 4096 {
			return nil, libacp.NewError(libacp.ErrInvalidParams, "code context path exceeds 4096 bytes")
		}
	}
	if len(p.GetCodeContext()) > 3 || contextSize > 65536 {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "code context exceeds 3 excerpts or 65536 bytes")
	}

	res, err := t.autocomplete(ctx, p)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, admission.ErrSuperseded) || errors.Is(err, codecompletion.ErrSuperseded) {
			return marshalProtocol(autocompleteResultWire(autocompleteResult{Kind: "cancelled"}))
		}
		return nil, libacp.InternalError(err.Error())
	}
	return marshalProtocol(autocompleteResultWire(res))
}

func (t *Transport) autocomplete(ctx context.Context, p *protocolv1.AutocompleteRequest) (autocompleteResult, error) {
	maxTokens := int(p.GetMaxTokens())
	if maxTokens <= 0 {
		maxTokens = 128
	}
	setup, _, closeSetup, err := t.deps.OpenInference(ctx)
	if err != nil {
		return autocompleteResult{}, err
	}
	defer closeSetup()
	revision, err := setup.RoleRevision(ctx, inferencesetup.RoleAutocomplete)
	if err != nil {
		return autocompleteResult{}, err
	}
	vars, err := t.autocompleteTemplateVars(ctx, p, maxTokens)
	if err != nil {
		return autocompleteResult{}, err
	}
	assignment := inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: vars["provider"], Model: vars["model"]}
	mode, _, err := setup.CompletionContract(ctx, assignment)
	if err != nil {
		return autocompleteResult{}, err
	}
	service, clientID, err := t.completionService(mode)
	if err != nil {
		return autocompleteResult{}, err
	}
	fingerprint, err := setup.ConfigurationFingerprint(ctx, assignment)
	if err != nil {
		return autocompleteResult{}, err
	}
	request := codecompletion.Request{
		Mode:     mode,
		Provider: vars["provider"], Model: vars["model"], Prefix: autocompleteCodePrefix(p) + p.GetPrefix(),
		Suffix: p.GetSuffix(), MaxTokens: maxTokens, TemplateVars: vars,
	}
	var text string
	if p.GetUri() == "" {
		text, err = service.Complete(ctx, request)
	} else {
		text, err = service.CompleteLatest(ctx, codecompletion.Editor{ClientID: clientID, DocumentID: p.GetUri()}, request)
	}
	if err != nil {
		return autocompleteResult{}, err
	}
	currentFingerprint, err := setup.ConfigurationFingerprint(ctx, assignment)
	if err != nil {
		return autocompleteResult{}, err
	}
	currentRevision, err := setup.RoleRevision(ctx, inferencesetup.RoleAutocomplete)
	if err != nil {
		return autocompleteResult{}, err
	}
	if revision != currentRevision || fingerprint != currentFingerprint {
		return autocompleteResult{}, errors.New("autocomplete configuration changed during inference; retry with the current assignment")
	}
	return autocompleteResult{Kind: "completion", Completion: text, Mode: mode, Fingerprint: fingerprint}, nil
}

func (t *Transport) completionService(mode string) (*codecompletion.Service, string, error) {
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	if t.completion != nil && t.completionMode != mode {
		t.completion.CancelPending()
		t.completion = nil
	}
	if t.completion == nil {
		service, err := t.newCompletionService(mode)
		if err != nil {
			return nil, "", err
		}
		t.completion, t.completionClientID, t.completionMode = service, uuid.NewString(), mode
	}
	return t.completion, t.completionClientID, nil
}

func (t *Transport) newCompletionService(mode string) (*codecompletion.Service, error) {
	switch mode {
	case modelregistry.CompletionFIM:
		return codecompletion.NewNative(t.deps.Engine.Models)
	case modelregistry.CompletionInstruct:
		if t.deps.CompletionChain == nil {
			return nil, errors.New("instructed autocomplete requires the configured completion chain")
		}
		chain, err := t.deps.CompletionChain()
		if err != nil {
			return nil, err
		}
		if chain == nil {
			return nil, errors.New("instructed autocomplete requires the configured completion chain")
		}
		return codecompletion.New(t.autocompleteAgent(), chain, t.deps.Engine.Models)
	case modelregistry.CompletionPrompt:
		return codecompletion.NewPromptOnly(t.deps.Engine.Models, t.deps.Engine.Models)
	default:
		return nil, errors.New("autocomplete requires an explicit completion mode")
	}
}

func autocompleteCodePrefix(p *protocolv1.AutocompleteRequest) string {
	if len(p.GetCodeContext()) == 0 {
		return ""
	}
	comment := "// "
	switch p.GetLanguageId() {
	case "python", "ruby", "shellscript", "yaml", "toml", "r", "perl":
		comment = "# "
	case "sql", "lua", "haskell":
		comment = "-- "
	}
	var out strings.Builder
	escape := strings.NewReplacer("<fim_prefix>", "< fim_prefix >", "<fim_suffix>", "< fim_suffix >", "<fim_middle>", "< fim_middle >")
	for _, snippet := range p.GetCodeContext() {
		out.WriteString(comment + "Code context: " + escape.Replace(strings.ReplaceAll(strings.ReplaceAll(snippet.GetPath(), "\n", " "), "\r", " ")) + "\n")
		content := escape.Replace(snippet.GetContent())
		for _, line := range strings.Split(content, "\n") {
			out.WriteString(comment + line + "\n")
		}
	}
	out.WriteString(comment + "Current file continuation:\n")
	return out.String()
}

// autocompleteAgent returns the session-less agentservice.Agent used for FIM
// completions.
func (t *Transport) autocompleteAgent() agentservice.Agent {
	if t.acAgent != nil {
		return t.acAgent
	}
	return agentservice.New(agentservice.Deps{
		Engine:      t.deps.Engine,
		DB:          t.deps.DB,
		WorkspaceID: t.workspaceID(),
		Identity:    "acp-client",
	})
}

// autocompleteTemplateVars resolves the completion model from the request
// params, then the configured autocomplete role, and carries the role's
// reasoning level. Nothing is guessed and nothing is retried with other
// settings; a model the chain cannot resolve fails loudly.
func (t *Transport) autocompleteTemplateVars(
	ctx context.Context,
	p *protocolv1.AutocompleteRequest,
	maxTokens int,
) (map[string]string, error) {
	vars := t.defaultAutocompleteTemplateVars(maxTokens)
	if think := t.configuredAutocompleteThink(ctx); think != "" {
		vars["think"] = think
	}
	if provider := strings.TrimSpace(p.GetProvider()); provider != "" {
		vars["provider"] = provider
	}
	if model := strings.TrimSpace(p.GetModel()); model != "" {
		vars["model"] = model
	}
	if strings.TrimSpace(p.GetProvider()) != "" || strings.TrimSpace(p.GetModel()) != "" {
		if strings.TrimSpace(p.GetProvider()) == "" || strings.TrimSpace(p.GetModel()) == "" {
			return nil, errors.New("autocomplete requires both an explicit provider and model")
		}
		return vars, nil
	}
	if provider, model, ok := t.configuredAutocompleteModel(ctx); ok {
		if provider != "" {
			vars["provider"] = provider
		}
		if model != "" {
			vars["model"] = model
		}
		if provider == "" || model == "" {
			return nil, errors.New("autocomplete assignment is incomplete; " + remedy.AutocompleteRole)
		}
		return vars, nil
	}
	return nil, errors.New("autocomplete is not configured; " + remedy.AutocompleteRole)
}

func (t *Transport) defaultAutocompleteTemplateVars(maxTokens int) map[string]string {
	vars := t.chainTemplateVars(nil)
	vars["max_tokens"] = fmt.Sprintf("%d", maxTokens)
	return vars
}

func (t *Transport) configuredAutocompleteThink(ctx context.Context) string {
	if t.deps.DB == nil {
		return ""
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	return strings.TrimSpace(clikv.Read(ctx, store, "default-autocomplete-think"))
}

func (t *Transport) configuredAutocompleteModel(ctx context.Context) (string, string, bool) {
	if t.deps.DB == nil {
		return "", "", false
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	provider := strings.TrimSpace(clikv.Read(ctx, store, "default-autocomplete-provider"))
	model := strings.TrimSpace(clikv.Read(ctx, store, "default-autocomplete-model"))
	return provider, model, provider != "" || model != ""
}
