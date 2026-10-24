package ollama

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func TestUnit_OllamaChat_SerializesImageInput(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"model":"llava","message":{"role":"assistant","content":"a cat"},"done":true,"done_reason":"stop"}`)
	}))
	defer srv.Close()

	provider := NewOllamaProvider("llava", []string{srv.URL}, srv.Client(), modelrepo.CapabilityConfig{CanChat: true}, "", nil)
	chat, err := provider.GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	_, err = chat.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "describe this", Images: []modelrepo.ImagePart{{Data: pngBytes, MimeType: "image/png"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages: %#v", gotBody["messages"])
	}
	imgs, ok := msgs[0].(map[string]any)["images"].([]any)
	if !ok || len(imgs) != 1 {
		t.Fatalf("expected 1 image on the ollama message, got %#v", msgs[0])
	}
	if want := base64.StdEncoding.EncodeToString(pngBytes); imgs[0] != want {
		t.Errorf("image base64 mismatch:\n want %s\n  got %v", want, imgs[0])
	}
}

func TestUnit_OllamaHTTPClient_ListUsesBearerAuthAndNormalizesAPIPath(t *testing.T) {
	t.Parallel()

	var (
		gotPath string
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	client, err := newOllamaHTTPClient(srv.URL+"/api", "test-key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.List(context.Background()); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/tags" {
		t.Fatalf("path = %q, want /api/tags", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("authorization = %q, want Bearer test-key", gotAuth)
	}
}

func TestUnit_OllamaHTTPClient_GenerateStreamsNDJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"response":"hel","done":false}`)
		fmt.Fprintln(w, `{"response":"lo","done":true,"done_reason":"stop"}`)
	}))
	defer srv.Close()

	client, err := newOllamaHTTPClient(srv.URL, "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	err = client.Generate(context.Background(), &GenerateRequest{Model: "test"}, func(resp GenerateResponse) error {
		chunks = append(chunks, resp.Response)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0] != "hel" || chunks[1] != "lo" {
		t.Fatalf("unexpected streamed chunks: %#v", chunks)
	}
}

func TestUnit_OllamaProvider_DoesNotInferThinkingFromModelName(t *testing.T) {
	provider := NewOllamaProvider("qwen3:8b", []string{"http://localhost:11434"}, nil, modelrepo.CapabilityConfig{}, "", nil)
	if provider.CanThink() {
		t.Fatalf("expected qwen3 model name alone not to set CanThink")
	}

	provider = NewOllamaProvider("custom", []string{"http://localhost:11434"}, nil, modelrepo.CapabilityConfig{CanThink: true}, "", nil)
	if !provider.CanThink() {
		t.Fatalf("expected explicit capability metadata to set CanThink")
	}
}

func TestUnit_OllamaChat_OmitsThinkWhenCapabilityIsFalse(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"model":"qwen3:8b","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`)
	}))
	defer srv.Close()

	provider := NewOllamaProvider("qwen3:8b", []string{srv.URL}, srv.Client(), modelrepo.CapabilityConfig{CanChat: true}, "", nil)
	chat, err := provider.GetChatConnection(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("high"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["think"]; ok {
		t.Fatalf("provider with CanThink=false sent Ollama think option: %#v", gotBody["think"])
	}
}

func TestUnit_BuildOllamaThink_NormalizesLevels(t *testing.T) {
	cfg := &modelrepo.ChatConfig{}
	if got, err := buildOllamaThink(cfg, nil); got != nil || err != nil {
		t.Fatalf("nil think config should omit Ollama think, got %#v (%v)", got, err)
	}

	modelrepo.WithThink("auto").Apply(cfg)
	if got, err := buildOllamaThink(cfg, nil); got != nil || err != nil {
		t.Fatalf("auto should omit Ollama think, got %#v (%v)", got, err)
	}

	cfg = &modelrepo.ChatConfig{}
	modelrepo.WithThink("off").Apply(cfg)
	got, err := buildOllamaThink(cfg, nil)
	if err != nil {
		t.Fatalf("off without a declaration: %v", err)
	}
	if got == nil || got.Value != false {
		t.Fatalf("off = %#v, want bool false", got)
	}

	cfg = &modelrepo.ChatConfig{}
	modelrepo.WithThink("minimal").Apply(cfg)
	got, err = buildOllamaThink(cfg, nil)
	if err != nil {
		t.Fatalf("minimal without a declaration: %v", err)
	}
	if got == nil || got.Value != "low" {
		t.Fatalf("minimal = %#v, want low", got)
	}

	cfg = &modelrepo.ChatConfig{}
	modelrepo.WithThink("xhigh").Apply(cfg)
	got, err = buildOllamaThink(cfg, nil)
	if err != nil {
		t.Fatalf("xhigh without a declaration: %v", err)
	}
	if got == nil || got.Value != "high" {
		t.Fatalf("xhigh = %#v, want high", got)
	}
}

func TestUnit_BuildOllamaThink_UsesTheDeclaredLevels(t *testing.T) {
	declared := &modelrepo.ThinkingTypes{Enabled: true, Disabled: true, EffortLevels: []string{"low", "medium", "high", "max"}}

	cfg := &modelrepo.ChatConfig{}
	modelrepo.WithThink("max").Apply(cfg)
	got, err := buildOllamaThink(cfg, declared)
	if err != nil {
		t.Fatalf("max against a model that declares it: %v", err)
	}
	if got == nil || got.Value != "max" {
		t.Fatalf("max = %#v, want max", got)
	}

	cfg = &modelrepo.ChatConfig{}
	modelrepo.WithThink("high").Apply(cfg)
	got, err = buildOllamaThink(cfg, &modelrepo.ThinkingTypes{Enabled: true, EffortLevels: []string{"low"}})
	if err != nil {
		t.Fatalf("high against a model declaring only low: %v", err)
	}
	if got == nil || got.Value != "low" {
		t.Fatalf("high with only low declared = %#v, want low", got)
	}

	cfg = &modelrepo.ChatConfig{}
	modelrepo.WithThink("off").Apply(cfg)
	if _, err := buildOllamaThink(cfg, &modelrepo.ThinkingTypes{Enabled: true, EffortLevels: []string{"low"}}); err == nil {
		t.Fatal("off against a model that advertises no thinking-off control must be refused")
	}

	cfg = &modelrepo.ChatConfig{}
	modelrepo.WithThink("high").Apply(cfg)
	if _, err := buildOllamaThink(cfg, &modelrepo.ThinkingTypes{EffortLevels: []string{"ultra"}}); err == nil {
		t.Fatal("a level the declaration cannot express must be refused rather than defaulted")
	}
}

func TestUnit_BuildOllamaOptions_ClampsPositiveNumPredictOnly(t *testing.T) {
	cfg := &modelrepo.ChatConfig{}
	modelrepo.WithMaxTokens(9000).Apply(cfg)
	opts := buildOllamaOptions(cfg, 2048, 0)
	if got := opts["num_predict"]; got != 2048 {
		t.Fatalf("num_predict = %#v, want 2048", got)
	}
	if _, ok := opts["num_ctx"]; ok {
		t.Fatalf("a model with no declared window must not be asked for one: %#v", opts)
	}

	negative := -1
	cfg = &modelrepo.ChatConfig{MaxTokens: &negative}
	opts = buildOllamaOptions(cfg, 2048, 0)
	if got := opts["num_predict"]; got != -1 {
		t.Fatalf("negative num_predict sentinel = %#v, want -1", got)
	}
}

func TestUnit_BuildOllamaOptions_RequestsTheDeclaredWindow(t *testing.T) {
	opts := buildOllamaOptions(&modelrepo.ChatConfig{}, 0, 8192)
	if got := opts["num_ctx"]; got != 8192 {
		t.Fatalf("num_ctx = %#v, want 8192", got)
	}
}
