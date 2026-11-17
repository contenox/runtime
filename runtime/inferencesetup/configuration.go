package inferencesetup

import (
	"context"
	"sort"

	"github.com/contenox/runtime/runtime/providerservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/servicecontract"
)

// ConfigurationContract describes persistent setup independently of native health and recommendations.
var ConfigurationContract = servicecontract.Contract{
	ID: "inference-configuration", Summary: "Configure AI roles independently",
	Description: "Configure Chat, Recovery, Autocomplete and Workspace Search independently using cloud accounts, custom endpoints or local models.",
	Operations: []servicecontract.Operation{
		{
			ID: "read", Summary: "Read AI configuration", Description: "Read saved assignments and connections without starting inference or waiting for provider discovery.",
			Spec: "List common defaults, their stored values and inherited values before session,\nagent and model constraints. --all includes advanced settings; --describe adds\ncomplete descriptions and the locations of settings held outside this store.",
			Output: servicecontract.Output{
				Stdout: "Every declared setting with the value in force, its scope and its fallback.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the settings are rendered", Meaning: "an unset setting is reported as inherited rather than as missing"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the local store cannot be read", Message: "names the store that failed; an unreadable store is never rendered as an unset configuration"},
			},
		},
		{
			ID: "apply", Summary: "Verify and apply reviewed roles", Description: "Verify proposed routes and execution settings before atomically saving their assignments, settings and evidence. Failed or cancelled verification preserves the current configuration. Chat and Recovery verify tool calls and consumption of their results. Required image input is checked with distinct colors; required audio input is checked with distinct beep counts. These input checks do not qualify speech transcription quality.",
			Spec: `Set a default for future launches. Names use dotted namespaces and snake_case
fields; units are part of numeric names. Legacy names remain aliases of the same row.

Examples:
  contenox-runtime config set inference.context.window_tokens 131072
  contenox-runtime config set inference.generation.max_output_tokens 8192
  contenox-runtime config set inference.reasoning.effort high
  contenox-runtime config set inference.autocomplete.model qwen2.5-coder:7b
  contenox-runtime config set execution.permissions.policy hitl-policy-strict.json

Use --scope global to set the fallback for a workspace-scoped key. Other keys
are global. Session pickers and slash commands do not save defaults.
Use 'config get <key> --explain' to inspect inheritance and constraints.`,
			Inputs: []servicecontract.Input{
				{ID: "proposals", Summary: "Reviewed role assignments to verify before saving", Type: "array", Required: true},
			},
			Examples: []servicecontract.Example{
				{Command: "contenox-runtime config set inference.context.window_tokens 131072", Explanation: "Session history window in tokens."},
				{Command: "contenox-runtime config set inference.generation.max_output_tokens 8192", Explanation: "Response token cap."},
				{Command: "contenox-runtime config set inference.reasoning.effort high", Explanation: "Reasoning effort for supported models."},
				{Command: "contenox-runtime config set inference.autocomplete.model qwen2.5-coder:7b", Explanation: "Autocomplete model."},
				{Command: "contenox-runtime config set execution.permissions.policy hitl-policy-strict.json", Explanation: "Envelope a dispatched unit runs under."},
			},
			Output: servicecontract.Output{
				Stdout: "The setting and the value now in force.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the value is stored", Meaning: "the next launch reads it"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the key is not a declared setting", Message: "names the unknown key and lists the valid ones"},
				{When: "the value is rejected by its own rule", Message: "names the constraint, such as a boolean or a non-negative count"},
				{When: "the requested scope does not apply to the key", Message: "names the key as global and points at the session control for a temporary override"},
			},
		},
		{
			ID: "configure-resources", Summary: "Verify and apply device and resource settings", Description: "Exercise the selected role with its proposed device and resources before committing changes. Concurrent role, settings or credential edits require another review.",
			Spec: "Write the device, context, thread, batch, KV cache and residency settings of one role. The proposed settings are exercised through the role before they are committed, so a configuration that cannot run is refused rather than saved.",
			Inputs: []servicecontract.Input{
				{ID: "role", Summary: "Role whose device and resource settings are verified", Type: "string", Required: true, Values: RoleIDs()},
				{ID: "options", Summary: "Device, context, thread, batch, KV cache and residency settings to verify", Type: "object"},
			},
			Output: servicecontract.Output{
				Stdout: "The setting and the value now in force.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the settings are exercised and committed", Meaning: "the role runs with the resources that were verified"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the role cannot run with the proposed resources", Message: "names the role and the resource that failed"},
				{When: "the role or an assignment changed during verification", Message: "requires another review instead of committing over the change"},
			},
		},
		{
			ID: "connect", Summary: "Connect an account or endpoint", Description: "Save one named provider connection and discover its models. Discovery errors remain attached to the saved connection.",
			Spec: `Register a named LLM backend endpoint in the local SQLite database.

The --type flag determines which provider protocol is used.
  openai, anthropic,
  gemini                        Cloud providers. Base URL inferred if --url is omitted. Requires --api-key-env.
  openai-codex                  ChatGPT subscription. Fixed endpoint; use backend login, no API key.
  modeld                        Native inference worker (local or remote gRPC).
  ollama                        Local daemon (requires 'ollama serve') or hosted Ollama Cloud (use
                                --url https://ollama.com/api and --api-key-env OLLAMA_API_KEY).
  vllm                          Self-hosted OpenAI-compatible endpoint (requires --url).
  vertex-google                 Google Cloud Vertex AI / Gemini (requires gcloud auth application-default login).
  scripted-test                 TEST ONLY. Replays a scripted dialog from a JSON file (requires --script).

API keys should be passed via --api-key-env (reads from environment) rather than
--api-key (inline literal) to avoid leaking secrets into shell history.

The scripted-test type is a fake: it calls no model and replays the turns in its
--script file in order, one per model turn. Every surface that names the active
provider shows "scripted-test", and 'contenox-runtime doctor' warns while it is the default.
It proves the machinery (chain loop, tool dispatch, HITL gate), not the agent's
judgement — you wrote the tool calls, so no real model was asked to choose them.

Examples:
  contenox-runtime backend add ollama     --type ollama
  contenox-runtime backend add ollama-cloud --type ollama    --url https://ollama.com/api --api-key-env OLLAMA_API_KEY
  contenox-runtime backend add openai     --type openai      --api-key-env OPENAI_API_KEY
  contenox-runtime backend add gemini     --type gemini      --api-key-env GEMINI_API_KEY
  contenox-runtime backend add myvllm    --type vllm         --url http://gpu-host:8000
  contenox-runtime backend add contenox --type contenox
  contenox-runtime backend add scripted  --type scripted-test --script ./dialog.json`,
			Inputs: []servicecontract.Input{
				{ID: "id", Summary: "Existing connection to update", Type: "string"},
				{ID: "name", Summary: "Connection name", Type: "string", Required: true, Positional: 1, Placeholder: "<name>"},
				{ID: "provider", Summary: "Supported provider type", Type: "string", Required: true, Flag: servicecontract.FlagSpec{Name: "type", Scope: servicecontract.FlagScopeCommand}},
				{ID: "url", Summary: "Provider API base URL", Type: "string", Required: true, Flag: servicecontract.FlagSpec{Name: "url", Scope: servicecontract.FlagScopeCommand}},
				{ID: "apiKey", Summary: "API key for this connection", Type: "secret", Flag: servicecontract.FlagSpec{Name: "api-key", Scope: servicecontract.FlagScopeCommand, Hidden: true}},
				{ID: "apiKeyEnv", Summary: "Environment variable containing the API key", Type: "string", Flag: servicecontract.FlagSpec{Name: "api-key-env", Scope: servicecontract.FlagScopeCommand}},
				{ID: "credentialSource", Summary: "Selected source of this connection's credentials", Type: "string", Required: true, Flag: servicecontract.FlagSpec{Name: "credential-source", Scope: servicecontract.FlagScopeCommand}},
				{ID: "credentialFile", Summary: "Credential JSON file to validate and import", Type: "string", Flag: servicecontract.FlagSpec{Name: "credential-file", Scope: servicecontract.FlagScopeCommand}},
			},
			Examples: []servicecontract.Example{
				{Command: "contenox-runtime backend add ollama --type ollama", Explanation: "A local daemon."},
				{Command: "contenox-runtime backend add ollama-cloud --type ollama --url https://ollama.com/api --api-key-env OLLAMA_API_KEY", Explanation: "A hosted Ollama endpoint."},
				{Command: "contenox-runtime backend add openai --type openai --api-key-env OPENAI_API_KEY", Explanation: "A cloud provider with an inferred base URL."},
				{Command: "contenox-runtime backend add myvllm --type vllm --url http://gpu-host:8000", Explanation: "A self-hosted OpenAI-compatible endpoint."},
			},
			Output: servicecontract.Output{
				Stdout: "The saved connection and the models discovery found for it.",
				Stderr: "A discovery failure, which stays attached to the saved connection.",
				Format: "text",
			},
			Outcomes: []servicecontract.Outcome{
				{Code: 0, When: "the connection is saved", Meaning: "the connection is usable by every role that names it"},
			},
			Refusals: []servicecontract.Refusal{
				{When: "the provider type is not one this runtime speaks", Message: "names the unsupported type and lists the supported ones"},
				{When: "a required field for the chosen type is absent", Message: "names the field, such as a URL for a self-hosted endpoint"},
				{When: "the endpoint is unreachable", Message: "reports the discovery failure and keeps the saved connection"},
			},
		},
	},
}

// ProviderConnection reports a saved connection and its latest discovery result.
type ProviderConnection struct {
	RegistrationCapabilities []string `json:"registrationCapabilities"`
	CredentialSource         string   `json:"credentialSource"`
	AuthorizationState       string   `json:"authorizationState"`
	AuthorizationError       string   `json:"authorizationError,omitempty"`
	DiscoveryState           string   `json:"discoveryState"`
	Revision                 string   `json:"revision"`
	Selector                 string   `json:"selector"`
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	Type                     string   `json:"type"`
	URL                      string   `json:"url"`
	State                    string   `json:"state"`
	Error                    string   `json:"error,omitempty"`
}

// AvailableModel reports a model advertised by a connected provider.
type AvailableModel struct {
	ContextTokens          int      `json:"contextTokens"`
	Digest                 string   `json:"digest"`
	NativeBackend          string   `json:"nativeBackend"`
	Registered             bool     `json:"registered"`
	DeclaredCapabilities   []string `json:"declaredCapabilities"`
	AdvertisedCapabilities []string `json:"advertisedCapabilities"`
	AdapterCapabilities    []string `json:"adapterCapabilities"`
	Vision                 bool     `json:"vision"`
	Audio                  bool     `json:"audio"`
	Provider               string   `json:"provider"`
	Name                   string   `json:"name"`
	Chat                   bool     `json:"chat"`
	Embed                  bool     `json:"embed"`
	Prompt                 bool     `json:"prompt"`
	FIMFormat              string   `json:"fimFormat,omitempty"`
	NextEditFormat         string   `json:"nextEditFormat,omitempty"`
	Autocomplete           bool     `json:"autocomplete"`
	NextEdit               bool     `json:"nextEdit"`
	CompletionMode         string   `json:"completionMode,omitempty"`
}

// Configuration is a read-only view of saved configuration and current observations.
type Configuration struct {
	Roles         []RoleStatus                         `json:"roles"`
	Providers     []ProviderConnection                 `json:"providers"`
	ProviderTypes []providerservice.ProviderCapability `json:"providerTypes"`
	Models        []AvailableModel                     `json:"models"`
}

// Configuration reads setup without refreshing discovery or requiring a running model.
func (s *Service) Configuration(ctx context.Context) (Configuration, error) {
	result := Configuration{Providers: []ProviderConnection{}, Models: []AvailableModel{}}
	types, err := providerservice.New(s.db, runtimetypes.LocalTenantID).ListSupportedProviders(ctx)
	if err != nil {
		return result, err
	}
	result.ProviderTypes = types
	roles, err := s.Roles(ctx)
	if err != nil {
		return result, err
	}
	result.Roles = roles
	backends, err := runtimetypes.New(s.db.WithoutTransaction()).ListBackends(ctx, nil, runtimetypes.MAXLIMIT)
	if err != nil {
		return result, err
	}
	states := s.state.Get(ctx)
	store := runtimetypes.New(s.db.WithoutTransaction())
	seen := map[string]bool{}
	for _, backend := range backends {
		connection := ProviderConnection{ID: backend.ID, Selector: runtimestate.ConnectionSelector(backend.ID), Name: backend.Name, Type: backend.Type, URL: backend.BaseURL, State: "checking"}
		connection.RegistrationCapabilities, err = runtimestate.RegistrationCapabilities(*backend)
		if err != nil {
			connection.State, connection.Error = "needs-attention", err.Error()
		}
		connection.DiscoveryState = "not-observed"
		connection.Revision, err = connectionRevision(ctx, store, backend)
		if err != nil {
			return result, err
		}
		connection.CredentialSource, connection.AuthorizationState, err = s.connectionCredentials(ctx, store, backend)
		if err != nil {
			connection.AuthorizationError = err.Error()
		}
		for _, role := range roles {
			if role.ConnectionID == backend.ID && role.Verification != nil && role.State == "available" {
				connection.AuthorizationState = "verified"
			}
		}
		if state, observed := states[backend.ID]; observed {
			connection.State = "available"
			connection.DiscoveryState = "available"
			if state.Error != "" {
				connection.State, connection.Error = "needs-attention", state.Error
				connection.DiscoveryState = "failed"
			}
			if state.DiscoveryError != "" {
				connection.DiscoveryState, connection.Error = "failed", state.DiscoveryError
			}
			if state.Backend.Revision != backend.Revision {
				connection.DiscoveryState = "stale"
			}
			for _, model := range state.PulledModels {
				if len(model.AdapterCapabilities) == 0 {
					model.AdapterCapabilities = connection.RegistrationCapabilities
				}
				name := model.Model
				if name == "" {
					name = model.Name
				}
				key := backend.ID + "/" + name
				if name != "" && !seen[key] {
					result.Models = append(result.Models, AvailableModel{ContextTokens: model.ContextLength, Digest: model.Digest, NativeBackend: model.NativeBackend, Registered: model.Registered, DeclaredCapabilities: model.DeclaredCapabilities, AdvertisedCapabilities: model.AdvertisedCapabilities, AdapterCapabilities: model.AdapterCapabilities, Provider: runtimestate.ConnectionSelector(backend.ID), Name: name, Chat: model.CanChat, Prompt: model.CanPrompt, Embed: model.CanEmbed, Vision: model.CanVision, Audio: model.CanAudio, CompletionMode: model.CompletionMode, FIMFormat: model.FIMFormat, Autocomplete: model.CanAutocomplete, NextEditFormat: model.NextEditFormat, NextEdit: model.CanNextEdit})
					seen[key] = true
				}
			}
		}
		result.Providers = append(result.Providers, connection)
	}
	sort.Slice(result.Models, func(i, j int) bool {
		return result.Models[i].Provider+result.Models[i].Name < result.Models[j].Provider+result.Models[j].Name
	})
	return result, nil
}
