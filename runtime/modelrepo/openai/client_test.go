package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func TestUnit_OpenAIReasoningEffort(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }
	cases := []struct {
		model string
		think *string
		want  string
	}{
		{"gpt-5", ptr("true"), "high"},
		{"gpt-5", ptr("minimal"), "minimal"},
		{"gpt-5", ptr("none"), ""},
		{"gpt-5.1", ptr("none"), "none"},
		{"gpt-5.4", ptr("none"), "none"},
		{"gpt-5.4", ptr("xhigh"), "xhigh"},
		{"gpt-5.5", ptr("xhigh"), "xhigh"},
		{"gpt-6-astra", ptr("max"), "max"},
		{"o3-mini", ptr("false"), ""},
		{"o3-mini", ptr("xhigh"), "xhigh"},
	}

	for _, tc := range cases {
		got, err := openAIReasoningEffort(tc.model, tc.think, nil)
		if err != nil {
			t.Errorf("openAIReasoningEffort(%q, %q) errored: %v", tc.model, *tc.think, err)
			continue
		}
		if got != tc.want {
			t.Errorf("openAIReasoningEffort(%q, %q) = %q, want %q", tc.model, *tc.think, got, tc.want)
		}
	}
}

func TestUnit_OpenAIReasoningEffortRefusesUndocumentedLevel(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }
	cases := []struct {
		model string
		think *string
	}{
		{"gpt-5.1", ptr("xhigh")},
		{"gpt-5.4", ptr("minimal")},
		{"gpt-5-pro", ptr("low")},
		{"gpt-6-astra", ptr("minimal")},
	}
	for _, tc := range cases {
		if _, err := openAIReasoningEffort(tc.model, tc.think, nil); err == nil {
			t.Errorf("openAIReasoningEffort(%q, %q) must refuse the level", tc.model, *tc.think)
		}
	}
}

func TestUnit_OpenAIReasoningEffortUsesDeclaredControls(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }
	declared := &modelrepo.ThinkingTypes{Disabled: true, EffortLevels: []string{"low", "high"}}

	got, err := openAIReasoningEffort("gpt-oss-120b", ptr("high"), declared)
	if err != nil || got != "high" {
		t.Fatalf("declared effort = %q, %v; want high", got, err)
	}
	if _, err := openAIReasoningEffort("gpt-oss-120b", ptr("xhigh"), declared); err == nil {
		t.Fatal("a level outside the declaration must be refused")
	}
	off, err := openAIReasoningEffort("gpt-oss-120b", ptr("off"), declared)
	if err != nil || off != "none" {
		t.Fatalf("declared off = %q, %v; want none", off, err)
	}
}

func TestUnit_OpenAISamplingParamGating(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model     string
		reasoning string
		omit      bool
	}{
		{"gpt-5", "", true},
		{"openai/gpt-5", "", true},
		{"gpt-5.1", "", false},
		{"gpt-5.4", "none", false},
		{"gpt-5.4", "high", true},
		{"gpt-5-pro", "high", true},
		{"gpt-5-nano", "", true},
		{"gpt-5-nano", "low", true},
		{"o4-mini", "", true},
		{"gpt-4o", "", false},
	}
	for _, tc := range cases {
		if got := openAIShouldOmitSamplingParams(tc.model, tc.reasoning); got != tc.omit {
			t.Errorf("openAIShouldOmitSamplingParams(%q, %q) = %v, want %v", tc.model, tc.reasoning, got, tc.omit)
		}
	}
}

func TestUnit_BuildOpenAIRequest_GPT5OmitsTemperature(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequest("gpt-5", msgs, []modelrepo.ChatArgument{
		modelrepo.WithTemperature(0.7),
	})
	if req.Temperature != nil {
		t.Fatalf("expected temperature omitted for gpt-5, got %v", req.Temperature)
	}
}

func TestUnit_BuildOpenAIRequest_GPT5NamespacedOmitsTemperature(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequest("openai/gpt-5", msgs, []modelrepo.ChatArgument{
		modelrepo.WithTemperature(0.1),
	})
	if req.Temperature != nil {
		t.Fatalf("expected temperature omitted for namespaced gpt-5, got %v", req.Temperature)
	}
}

func TestUnit_BuildOpenAIRequest_GPT54NoneKeepsTemperature(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequest("gpt-5.4", msgs, []modelrepo.ChatArgument{
		modelrepo.WithTemperature(0.7),
		modelrepo.WithThink("none"),
	})
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Fatalf("expected temperature preserved for gpt-5.4 reasoning=none, got %v", req.Temperature)
	}
	if req.ReasoningEffort != "none" {
		t.Fatalf("reasoning_effort = %q, want none", req.ReasoningEffort)
	}
}

func TestUnit_BuildOpenAIRequest_GPT54HighOmitsTemperature(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequest("gpt-5.4", msgs, []modelrepo.ChatArgument{
		modelrepo.WithTemperature(0.7),
		modelrepo.WithThink("high"),
	})
	if req.Temperature != nil {
		t.Fatalf("expected temperature omitted for gpt-5.4 reasoning=high, got %v", req.Temperature)
	}
	if req.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", req.ReasoningEffort)
	}
}

func TestUnit_BuildOpenAIRequest_UsesMaxCompletionTokensJSON(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequest("gpt-4o", msgs, []modelrepo.ChatArgument{
		modelrepo.WithMaxTokens(42),
	})
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"max_completion_tokens":42`) {
		t.Fatalf("expected max_completion_tokens in payload, got %s", s)
	}
	if strings.Contains(s, `"max_tokens"`) {
		t.Fatalf("did not expect deprecated max_tokens in payload, got %s", s)
	}
}

func TestUnit_OpenAIClient_ClampsChatAndResponsesMaxOutputTokens(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	client := &openAIClient{maxOutputTokens: 64}

	chatReq, _ := buildOpenAIRequest("gpt-4o", msgs, []modelrepo.ChatArgument{
		modelrepo.WithMaxTokens(128),
	})
	client.clampChatMaxOutputTokens(&chatReq)
	if chatReq.MaxCompletionTokens == nil || *chatReq.MaxCompletionTokens != 64 {
		t.Fatalf("chat max_completion_tokens = %v, want 64", chatReq.MaxCompletionTokens)
	}

	responsesReq, _, err := buildOpenAIResponsesRequestWithCapabilities("gpt-5", msgs, []modelrepo.ChatArgument{
		modelrepo.WithMaxTokens(128),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	client.clampResponsesMaxOutputTokens(&responsesReq)
	if responsesReq.MaxOutputTokens == nil || *responsesReq.MaxOutputTokens != 64 {
		t.Fatalf("responses max_output_tokens = %v, want 64", responsesReq.MaxOutputTokens)
	}
}

func TestUnit_BuildOpenAIRequest_GPT4KeepsTemperature(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequest("gpt-4o", msgs, []modelrepo.ChatArgument{
		modelrepo.WithTemperature(0.7),
	})
	if req.Temperature == nil {
		t.Fatal("expected temperature set for gpt-4o")
	}
	if *req.Temperature != 0.7 {
		t.Fatalf("temperature = %v, want 0.7", *req.Temperature)
	}
}

func TestUnit_BuildOpenAIRequest_OmitsReasoningWhenCanThinkFalse(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequestWithCapabilities("gpt-5", msgs, []modelrepo.ChatArgument{
		modelrepo.WithThink("high"),
	}, false)
	if req.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want empty when CanThink=false", req.ReasoningEffort)
	}
}

func TestUnit_BuildOpenAIRequest_EmitsReasoningWhenCanThinkTrue(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}
	req, _ := buildOpenAIRequestWithCapabilities("gpt-5", msgs, []modelrepo.ChatArgument{
		modelrepo.WithThink("high"),
	}, true)
	if req.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", req.ReasoningEffort)
	}
}
