package ollama_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/ollama"
)

type ollamaStub struct {
	show      string
	responses map[string][]string
	status    map[string]int

	bodies []map[string]any
}

func (s *ollamaStub) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read %s body: %v", r.URL.Path, err)
			return
		}
		var body map[string]any
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decode %s body: %v (%s)", r.URL.Path, err, raw)
				return
			}
		}
		s.bodies = append(s.bodies, body)

		if code, ok := s.status[r.URL.Path]; ok {
			w.WriteHeader(code)
			fmt.Fprint(w, `{"error":"scripted failure"}`)
			return
		}
		lines, ok := s.responses[r.URL.Path]
		if !ok {
			switch r.URL.Path {
			case "/api/show":
				lines = []string{s.show}
			case "/api/version", "/api/contenox":
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *ollamaStub) lastBody() map[string]any {
	if len(s.bodies) == 0 {
		return nil
	}
	return s.bodies[len(s.bodies)-1]
}

func ollamaProvider(t *testing.T, srv *httptest.Server, model string, caps modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	return ollama.NewOllamaProvider(model, []string{srv.URL}, srv.Client(), caps, "", nil)
}

func TestSystem_OllamaChat_SurfacesAnInBandStreamError(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {
			`{"model":"m","message":{"role":"assistant","content":"par"},"done":false}`,
			`{"error":"an error was encountered while running the model"}`,
		},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("an in-band stream error must not read as a completed turn")
	}
	if !strings.Contains(err.Error(), "an error was encountered while running the model") {
		t.Fatalf("error lost the vendor's diagnosis: %v", err)
	}
}

func TestSystem_OllamaChat_ClassifiesAnInBandStreamError(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"error":"the input length exceeds the context length"}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, modelrepo.ErrContextLengthExceeded) {
		t.Fatalf("in-band overflow error = %v, want ErrContextLengthExceeded", err)
	}
}

func TestUnit_OllamaChat_ReportsPromptCacheReads(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop","prompt_eval_count":100,"prompt_eval_cached_count":64,"eval_count":7}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	res, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage == nil {
		t.Fatal("no usage reported")
	}
	if res.Usage.CacheReadTokens != 64 {
		t.Fatalf("cache read tokens = %d, want 64", res.Usage.CacheReadTokens)
	}
	if res.Usage.PromptTokens != 100 {
		t.Fatalf("prompt tokens = %d, want the vendor's whole count 100", res.Usage.PromptTokens)
	}
}

func TestUnit_OllamaStream_ReportsPromptCacheReads(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {
			`{"model":"m","message":{"role":"assistant","content":"ok"},"done":false}`,
			`{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":100,"prompt_eval_cached_count":64,"eval_count":7}`,
		},
	}}
	srv := stub.start(t)
	stream, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanStream: true}).GetStreamConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	ch, err := stream.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	var terminal *modelrepo.StreamTerminal
	for parcel := range ch {
		if parcel.Error != nil {
			t.Fatal(parcel.Error)
		}
		if parcel.Terminal != nil {
			terminal = parcel.Terminal
		}
	}
	if terminal == nil || terminal.Usage == nil {
		t.Fatal("no terminal usage reported")
	}
	if terminal.Usage.CacheReadTokens != 64 {
		t.Fatalf("cache read tokens = %d, want 64", terminal.Usage.CacheReadTokens)
	}
}

func TestSystem_OllamaAutocomplete_SendsTheSuffixToAnInsertModel(t *testing.T) {
	stub := &ollamaStub{
		show: `{"capabilities":["completion","insert"],"model_info":{"llama.context_length":8192}}`,
		responses: map[string][]string{
			"/api/generate": {`{"model":"m","response":"result","done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":3}`},
		},
	}
	srv := stub.start(t)
	provider := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanAutocomplete: true})

	client, err := provider.GetAutocompleteConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{
		Prefix:      "def gcd(a, b):",
		Suffix:      "    return result",
		MaxTokens:   32,
		Temperature: 0,
		TopP:        1,
		SessionKey:  "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "result" {
		t.Fatalf("text = %q, want result", res.Text)
	}

	body := stub.lastBody()
	if body["suffix"] != "    return result" {
		t.Fatalf("suffix = %#v, want the caller's suffix", body["suffix"])
	}
	if body["prompt"] != "def gcd(a, b):" {
		t.Fatalf("prompt = %#v, want the caller's prefix verbatim", body["prompt"])
	}
	if raw, ok := body["raw"]; ok && raw != false {
		t.Fatalf("raw = %#v; raw mode would bypass the model's own FIM template", raw)
	}
}

func TestSystem_OllamaAutocomplete_RendersTheDeclaredTokenContract(t *testing.T) {
	stub := &ollamaStub{
		show: `{"capabilities":["completion"]}`,
		responses: map[string][]string{
			"/api/generate": {`{"model":"m","response":"result","done":true,"done_reason":"stop"}`},
		},
	}
	srv := stub.start(t)
	provider := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{
		CanAutocomplete: true,
		CompletionMode:  "fim",
		FIMFormat:       "qwen2.5-coder",
	})

	client, err := provider.GetAutocompleteConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{
		Prefix: "def gcd(a, b):", Suffix: "    return result",
		MaxTokens: 32, Temperature: 0, TopP: 1, SessionKey: "session",
	}); err != nil {
		t.Fatal(err)
	}

	body := stub.lastBody()
	if raw, _ := body["raw"].(bool); !raw {
		t.Fatalf("raw = %#v, want true for a locally rendered prompt", body["raw"])
	}
	if _, ok := body["suffix"]; !ok {
		t.Fatalf("the vendor's suffix member is part of GenerateRequest: %#v", body)
	}
	if body["suffix"] != "" {
		t.Fatalf("suffix = %#v; a locally rendered prompt already carries both sides", body["suffix"])
	}
	if prompt, _ := body["prompt"].(string); !strings.Contains(prompt, "def gcd(a, b):") || !strings.Contains(prompt, "return result") {
		t.Fatalf("prompt = %q, want the rendered contract carrying both sides", prompt)
	}
}

func TestSystem_OllamaCatalog_DerivesTheInsertCapability(t *testing.T) {
	stub := &ollamaStub{
		show: `{"capabilities":["completion","insert"],"model_info":{"llama.context_length":4096}}`,
		responses: map[string][]string{
			"/api/tags": {`{"models":[{"name":"coder:1b","model":"coder:1b"}]}`},
		},
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "ollama", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	models, err := catalog.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || !models[0].CanAutocomplete {
		t.Fatalf("observed models = %#v, want the insert-capable model to declare autocomplete", models)
	}
}

func TestSystem_OllamaCatalog_InsertModelCarriesACompletionContractAndServesIt(t *testing.T) {
	stub := &ollamaStub{
		show: `{"capabilities":["completion","insert","tools"],"model_info":{"llama.context_length":8192}}`,
		responses: map[string][]string{
			"/api/tags":     {`{"models":[{"name":"deepseek-flash","model":"deepseek-flash"}]}`},
			"/api/generate": {`{"model":"deepseek-flash","response":"result","done":true,"done_reason":"stop"}`},
		},
	}
	srv := stub.start(t)
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "ollama", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	models, err := catalog.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("observed models = %#v", models)
	}
	model := models[0]
	if !model.CanAutocomplete || model.CompletionMode != runtimetypes.CompletionModeFIM {
		t.Fatalf("observed = autocomplete %t mode %q, want autocomplete with fim", model.CanAutocomplete, model.CompletionMode)
	}
	if _, err := modelcontract.ResolveFIM(model.FIMFormat); err != nil {
		t.Fatalf("observed FIM format %q does not resolve: %v", model.FIMFormat, err)
	}

	client, err := catalog.ProviderFor(model).GetAutocompleteConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{Prefix: "def f():", Suffix: "    return 1", MaxTokens: 16, TopP: 1, SessionKey: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "result" {
		t.Fatalf("text = %q", res.Text)
	}
	body := stub.lastBody()
	if body["suffix"] != "    return 1" || body["prompt"] != "def f():" {
		t.Fatalf("body = %#v, want prompt and suffix", body)
	}
	if raw, ok := body["raw"]; ok && raw != false {
		t.Fatalf("raw = %#v", raw)
	}
	if body["stream"] != false {
		t.Fatalf("stream = %#v, want false", body["stream"])
	}
	if options, _ := body["options"].(map[string]any); options["num_predict"] != float64(16) {
		t.Fatalf("options = %#v, want num_predict", body["options"])
	}
}

func TestSystem_OllamaChat_RequestsTheDeclaredContextWindow(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true, ContextLength: 8192}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}

	options, ok := stub.lastBody()["options"].(map[string]any)
	if !ok {
		t.Fatalf("no options member on the request: %#v", stub.lastBody())
	}
	if options["num_ctx"] != float64(8192) {
		t.Fatalf("num_ctx = %#v, want 8192", options["num_ctx"])
	}
}

func TestSystem_OllamaChat_KeepsNestedToolRequired(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	tool := modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{
		Name: "fs_write",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"options": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"mode": map[string]any{"type": "string"},
					},
					"required": []string{"mode"},
				},
			},
			"required": []string{"options"},
		},
	}}
	if _, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithTool(tool)); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(stub.lastBody()["tools"])
	if err != nil {
		t.Fatal(err)
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %s, want one tool", raw)
	}
	parameters := tools[0]["function"].(map[string]any)["parameters"].(map[string]any)
	if got := fmt.Sprint(parameters["required"]); got != "[options]" {
		t.Fatalf("top-level required = %v, want [options]", parameters["required"])
	}
	nested := parameters["properties"].(map[string]any)["options"].(map[string]any)
	if got := fmt.Sprint(nested["required"]); got != "[mode]" {
		t.Fatalf("nested required = %v, want [mode]; the schema reached Ollama without it", nested["required"])
	}
}

func TestSystem_OllamaChat_SendsTheDeclaredThinkingLevel(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`},
	}}
	srv := stub.start(t)
	provider := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{
		CanChat:       true,
		CanThink:      true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, Disabled: true, EffortLevels: []string{"low", "medium", "high", "max"}},
	})
	chat, err := provider.GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("max")); err != nil {
		t.Fatal(err)
	}
	if got := stub.lastBody()["think"]; got != "max" {
		t.Fatalf("think = %#v, want the vendor's max level", got)
	}
}

func TestSystem_OllamaChat_SendsStructuredOutputFormat(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"{}"},"done":true,"done_reason":"stop"}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONObject})); err != nil {
		t.Fatal(err)
	}
	if got := stub.lastBody()["format"]; got != "json" {
		t.Fatalf("format = %#v, want the vendor's json literal", got)
	}

	schema := map[string]any{"type": "object", "properties": map[string]any{"age": map[string]any{"type": "integer"}}}
	if _, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONSchema, Name: "person", Schema: schema})); err != nil {
		t.Fatal(err)
	}
	format, ok := stub.lastBody()["format"].(map[string]any)
	if !ok || format["type"] != "object" {
		t.Fatalf("format = %#v, want the JSON schema document", stub.lastBody()["format"])
	}
}

func TestSystem_OllamaChat_AcceptsAnAbsentDoneReason(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	res, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("a done response with no done_reason must be a successful turn: %v", err)
	}
	if res.Message.Content != "ok" {
		t.Fatalf("content = %q, want ok", res.Message.Content)
	}
}

func TestSystem_OllamaCatalog_KeepsTheSelectedManifestOfAManifestList(t *testing.T) {
	stub := &ollamaStub{
		show: `{"capabilities":["completion"],"model_info":{"llama.context_length":4096},"manifests":[{"runner":"cpu","selected":false},{"runner":"cuda","selected":true}]}`,
		responses: map[string][]string{
			"/api/tags": {`{"models":[{"name":"m:1b","model":"m:1b","details":{"runner":"cpu"}},{"name":"m:1b","model":"m:1b","details":{"runner":"cuda"}}]}`},
		},
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "ollama", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	models, err := catalog.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("observed %d models for one tag: %#v", len(models), models)
	}
	if models[0].Name != "m:1b" || !models[0].CanChat {
		t.Fatalf("manifest-list model = %#v", models[0])
	}
}

func TestSystem_OllamaCatalog_ReportsAFailedModelDescription(t *testing.T) {
	stub := &ollamaStub{
		status: map[string]int{"/api/show": http.StatusInternalServerError},
		responses: map[string][]string{
			"/api/tags": {`{"models":[{"name":"m:1b","model":"m:1b"}]}`},
		},
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "ollama", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.ListModels(context.Background())
	if err == nil {
		t.Fatal("a failed /api/show must not be read as a model with no capabilities")
	}
	if !strings.Contains(err.Error(), "m:1b") {
		t.Fatalf("error does not name the model it could not describe: %v", err)
	}
}

func TestUnit_OllamaChat_CarriesNoExtensionMembersByDefault(t *testing.T) {
	stub := &ollamaStub{responses: map[string][]string{
		"/api/chat": {`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`},
	}}
	srv := stub.start(t)
	chat, err := ollamaProvider(t, srv, "m", modelrepo.CapabilityConfig{CanChat: true}).GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}

	body := stub.lastBody()
	for _, extension := range []string{"contenox_session", "audios", "width", "height", "steps", "_debug_render_only"} {
		if _, ok := body[extension]; ok {
			t.Fatalf("extension member %q reached a server that never declared it: %#v", extension, body)
		}
	}
	for _, vendor := range []string{"model", "messages", "stream", "options", "keep_alive"} {
		if _, ok := body[vendor]; !ok {
			t.Fatalf("vendor member %q missing from the request: %#v", vendor, body)
		}
	}
}
