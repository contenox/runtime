// Package codecompletion owns native FIM and explicitly selected instructed completion.
package codecompletion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/google/uuid"
)

// Request identifies a complete, explicitly selected completion route and input.
type Request struct {
	Mode         string
	Provider     string
	Model        string
	Prefix       string
	Suffix       string
	MaxTokens    int
	TemplateVars map[string]string
}

// Service owns the same completion operation used by IDE editing and setup verification.
type Service struct {
	native     Autocompleter
	prompt     PromptExecutor
	instructed instructedCompleter
	mu         sync.Mutex
	active     map[Editor]*completion
}

// Editor identifies one document in one IDE client, independently of its selected route.
type Editor struct {
	ClientID   string
	DocumentID string
}

type completion struct{ cancel context.CancelCauseFunc }

type instructedCompleter interface {
	complete(context.Context, Request) (string, error)
	fingerprint() (string, error)
}

type chainCompleter struct {
	agent Prompter
	chain *taskengine.TaskChainDefinition
}

type unconfiguredInstructed struct{}

type unconfiguredPrompt struct{}

func (unconfiguredInstructed) complete(context.Context, Request) (string, error) {
	return "", errors.New("instructed completion is unconfigured on this completion service; " + remedy.DeclareCompletion)
}

func (unconfiguredInstructed) fingerprint() (string, error) {
	return "", errors.New("instructed completion is unconfigured on this completion service; " + remedy.DeclareCompletion)
}

func (unconfiguredPrompt) PromptExecute(context.Context, llmrepo.Request, string, float32, string, ...modelrepo.PromptArgument) (string, llmrepo.Meta, error) {
	return "", llmrepo.Meta{}, errors.New("prompt completion is unconfigured on this completion service; " + remedy.DeclareCompletion)
}

// ErrSuperseded identifies a completion replaced by a newer request for the same editor.
var ErrSuperseded = errors.New("code completion was superseded")

// Prompter executes the configured completion chain without requiring a conversation session.
type Prompter interface {
	Prompt(context.Context, agentservice.PromptRequest) (*agentservice.PromptResponse, error)
}

// Autocompleter executes a native FIM request on the selected runtime route.
type Autocompleter interface {
	Autocomplete(context.Context, llmrepo.Request, modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, llmrepo.Meta, error)
}

// PromptExecutor executes one bounded prompt on the selected model route.
type PromptExecutor interface {
	PromptExecute(context.Context, llmrepo.Request, string, float32, string, ...modelrepo.PromptArgument) (string, llmrepo.Meta, error)
}

// New constructs completion with its required native executor, agent and instructed chain.
func New(agent Prompter, chain *taskengine.TaskChainDefinition, native Autocompleter) (*Service, error) {
	if agent == nil || chain == nil || native == nil {
		return nil, errors.New("code completion requires a native executor, agent and completion chain")
	}
	return &Service{native: native, prompt: unconfiguredPrompt{}, instructed: chainCompleter{agent: agent, chain: chain}, active: make(map[Editor]*completion)}, nil
}

// NewNative constructs native completion without an instructed-chain dependency.
func NewNative(native Autocompleter) (*Service, error) {
	if native == nil {
		return nil, errors.New("native completion requires an executor")
	}
	return &Service{native: native, prompt: unconfiguredPrompt{}, instructed: unconfiguredInstructed{}, active: make(map[Editor]*completion)}, nil
}

// NewPromptOnly constructs completion with explicit prompt execution and native FIM dependencies.
func NewPromptOnly(prompt PromptExecutor, native Autocompleter) (*Service, error) {
	if prompt == nil || native == nil {
		return nil, errors.New("prompt completion requires prompt and native executors")
	}
	return &Service{native: native, prompt: prompt, instructed: unconfiguredInstructed{}, active: make(map[Editor]*completion)}, nil
}

// Fingerprint identifies the loaded completion chain used by editing and verification.
func (s *Service) Fingerprint() (string, error) {
	return s.instructed.fingerprint()
}

func (c chainCompleter) fingerprint() (string, error) {
	raw, err := json.Marshal(c.chain)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ExecutionFingerprint binds verification to the selected operation implementation.
func (s *Service) ExecutionFingerprint(mode, format string) (string, error) {
	var identity string
	var err error
	switch mode {
	case modelregistry.CompletionFIM:
		identity, err = modelcontract.FIMFingerprint(format)
	case modelregistry.CompletionInstruct:
		if format != "" {
			return "", errors.New("instructed completion cannot select a native FIM format")
		}
		identity, err = s.Fingerprint()
	case modelregistry.CompletionPrompt:
		if format != "" {
			return "", errors.New("prompt completion cannot select a native FIM format")
		}
		identity = "bounded-instruction-prompt:v1"
	default:
		return "", errors.New("completion fingerprint requires an explicit operation")
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("codecompletion:v2:" + mode + ":" + identity))
	return hex.EncodeToString(sum[:]), nil
}

// CancelPending fences results and requests cancellation before replacing a route executor.
func (s *Service) CancelPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for editor, request := range s.active {
		request.cancel(ErrSuperseded)
		delete(s.active, editor)
	}
}

// CompleteLatest supersedes the entire prior completion operation for this editor.
func (s *Service) CompleteLatest(ctx context.Context, editor Editor, request Request) (string, error) {
	if ctx == nil || editor.ClientID == "" || editor.DocumentID == "" {
		return "", errors.New("editor completion requires context, client and document identity")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	current := &completion{cancel: cancel}
	s.mu.Lock()
	if previous := s.active[editor]; previous != nil {
		previous.cancel(ErrSuperseded)
	}
	s.active[editor] = current
	s.mu.Unlock()
	defer cancel(context.Canceled)
	text, err := s.complete(ctx, request, fmt.Sprintf("autocomplete:%q:%q", editor.ClientID, editor.DocumentID))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[editor] != current {
		return "", ErrSuperseded
	}
	delete(s.active, editor)
	if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	return text, err
}

// Complete runs the selected operation without substituting a route or retrying different settings.
func (s *Service) Complete(ctx context.Context, request Request) (string, error) {
	return s.complete(ctx, request, "autocomplete:"+uuid.NewString())
}

func (s *Service) complete(ctx context.Context, request Request, cacheIdentity string) (string, error) {
	if ctx == nil {
		return "", errors.New("code completion context is required")
	}
	ctx = llmrepo.WithSessionKey(ctx, llmrepo.DeriveSessionKey(cacheIdentity))
	ctx = modeldconn.WithWorkerRole(ctx, "autocomplete")
	ctx = modeldconn.WithAdmissionOptions(ctx, modeldconn.AdmissionOptions{Priority: admission.Autocomplete, RequesterID: uuid.NewString()})
	if strings.TrimSpace(request.Provider) == "" || strings.TrimSpace(request.Model) == "" {
		return "", errors.New("code completion requires an explicit provider and model; " + remedy.AutocompleteRole)
	}
	if request.MaxTokens <= 0 {
		return "", errors.New("code completion requires a positive output limit")
	}
	if request.Prefix == "" && request.Suffix == "" {
		return "", errors.New("code completion requires a prefix or suffix")
	}
	switch request.Mode {
	case modelregistry.CompletionFIM:
		result, _, err := s.native.Autocomplete(ctx, llmrepo.Request{WorkerRole: "autocomplete", ProviderTypes: []string{request.Provider}, ModelNames: []string{request.Model}}, modelrepo.AutocompleteRequest{Prefix: request.Prefix, Suffix: request.Suffix, MaxTokens: request.MaxTokens, Temperature: 0, TopP: 1, Seed: 0})
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		if err != nil {
			return "", err
		}
		return result.Text, nil
	case modelregistry.CompletionInstruct:
		return s.instructed.complete(ctx, request)
	case modelregistry.CompletionPrompt:
		text, _, err := s.prompt.PromptExecute(ctx, llmrepo.Request{WorkerRole: "autocomplete", ProviderTypes: []string{request.Provider}, ModelNames: []string{request.Model}},
			"Complete the source code at the cursor. Return only the code to insert. Do not explain, add markdown, repeat the prefix, or modify the suffix.", 0,
			"Insert the smallest code fragment that completes this source at the cursor. Return only inserted code.\n\nSource before cursor:\n"+request.Prefix+"\n\nSource after cursor:\n"+request.Suffix,
			modelrepo.WithPromptMaxTokens(request.MaxTokens))
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		if err != nil {
			return "", err
		}
		text = strings.TrimRightFunc(instructedText(text), unicode.IsSpace)
		if text == "" {
			return "", errors.New("autocomplete: the model returned no completion text")
		}
		return text, nil
	default:
		return "", errors.New("code completion requires an explicit native FIM, instructed, or prompt mode; " + remedy.DeclareCompletion)
	}
}

func (c chainCompleter) complete(ctx context.Context, request Request) (string, error) {
	if request.TemplateVars == nil {
		return "", errors.New("instructed completion requires template configuration")
	}
	vars := make(map[string]string, len(request.TemplateVars)+3)
	for key, value := range request.TemplateVars {
		vars[key] = value
	}
	vars["provider"], vars["model"] = request.Provider, request.Model
	vars["max_tokens"] = fmt.Sprint(request.MaxTokens)
	response, err := c.agent.Prompt(libtracker.WithNewRequestID(ctx), agentservice.PromptRequest{
		Input: "<fim_prefix>" + request.Prefix + "<fim_suffix>" + request.Suffix + "<fim_middle>",
		Chain: c.chain, TemplateVars: vars,
	})
	if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", errors.New("code completion returned no response")
	}
	text := strings.TrimRightFunc(instructedText(AssistantText(response.Output)), unicode.IsSpace)
	if text == "" {
		return "", errors.New("autocomplete: the model returned no completion text")
	}
	return text, nil
}

func instructedText(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) > 1 && strings.HasPrefix(strings.TrimLeftFunc(lines[0], unicode.IsSpace), "```") {
		lines = lines[1:]
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		return strings.Join(lines, "\n")
	}
	if strings.TrimSpace(text) == "```" {
		return ""
	}
	return text
}

// AssistantText extracts visible assistant output from a completion-chain result.
func AssistantText(output any) string {
	switch value := output.(type) {
	case string:
		return value
	case taskengine.ChatHistory:
		for index := len(value.Messages) - 1; index >= 0; index-- {
			if value.Messages[index].Role == "assistant" && value.Messages[index].Content != "" {
				return value.Messages[index].Content
			}
		}
	case *taskengine.ChatHistory:
		if value != nil {
			return AssistantText(*value)
		}
	}
	return ""
}
