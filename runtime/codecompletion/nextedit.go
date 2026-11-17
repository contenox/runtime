package codecompletion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/google/uuid"
)

// NextEditRequest selects a route and an explicit editable document snapshot.
type NextEditRequest struct {
	Provider  string
	Model     string
	Format    string
	Input     modelcontract.NextEditInput
	MaxTokens int
}

// NextEditExecutor executes the selected model's declared edit operation.
type NextEditExecutor interface {
	NextEdit(context.Context, llmrepo.Request, modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error)
}

// NextEditService owns prediction identity, supersession and replacement validation.
type NextEditService struct {
	executor NextEditExecutor
	mu       sync.Mutex
	active   map[Editor]*completion
}

// NewNextEdit constructs edit prediction with its required model executor.
func NewNextEdit(executor NextEditExecutor) (*NextEditService, error) {
	if executor == nil {
		return nil, errors.New("next edit requires a model executor")
	}
	return &NextEditService{executor: executor, active: make(map[Editor]*completion)}, nil
}

// ExecutionFingerprint identifies the edit prompt, parser and service semantics.
func (s *NextEditService) ExecutionFingerprint(format string) (string, error) {
	contract, err := modelcontract.NextEditFingerprint(format)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("codecompletion:next-edit:window-v1:" + contract))
	return hex.EncodeToString(sum[:]), nil
}

// CancelPending fences predictions before their route is replaced.
func (s *NextEditService) CancelPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for editor, request := range s.active {
		request.cancel(ErrSuperseded)
		delete(s.active, editor)
	}
}

// PredictLatest supersedes the previous prediction for this client and document.
func (s *NextEditService) PredictLatest(ctx context.Context, editor Editor, request NextEditRequest) ([]modelcontract.TextEdit, error) {
	if ctx == nil || editor.ClientID == "" || editor.DocumentID == "" {
		return nil, errors.New("next edit requires context, client and document identity")
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
	edits, err := s.predict(ctx, request, fmt.Sprintf("next-edit:%q:%q", editor.ClientID, editor.DocumentID))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[editor] != current {
		return nil, ErrSuperseded
	}
	delete(s.active, editor)
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	return edits, err
}

// Predict verifies or executes an isolated prediction on the selected route.
func (s *NextEditService) Predict(ctx context.Context, request NextEditRequest) ([]modelcontract.TextEdit, error) {
	return s.predict(ctx, request, "next-edit:"+uuid.NewString())
}

func (s *NextEditService) predict(ctx context.Context, request NextEditRequest, identity string) ([]modelcontract.TextEdit, error) {
	if ctx == nil {
		return nil, errors.New("next edit context is required")
	}
	if strings.TrimSpace(request.Provider) == "" || strings.TrimSpace(request.Model) == "" || request.MaxTokens <= 0 {
		return nil, errors.New("next edit requires a selected connection, model and output limit; " + remedy.NextEditRole)
	}
	if err := request.Input.Validate(); err != nil {
		return nil, err
	}
	input, err := selectNextEditInput(request)
	if err != nil {
		return nil, err
	}
	ctx = llmrepo.WithSessionKey(ctx, llmrepo.DeriveSessionKey(identity))
	ctx = modeldconn.WithWorkerRole(ctx, "next-edit")
	ctx = modeldconn.WithAdmissionOptions(ctx, modeldconn.AdmissionOptions{Priority: admission.Foreground, RequesterID: uuid.NewString()})
	result, _, err := s.executor.NextEdit(ctx, llmrepo.Request{WorkerRole: "next-edit", ProviderTypes: []string{request.Provider}, ModelNames: []string{request.Model}}, modelrepo.NextEditRequest{Input: input, MaxTokens: request.MaxTokens, Temperature: 0, TopP: 1, Seed: 0})
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if err != nil {
		return nil, err
	}
	if result.FinishReason != "stop" {
		return nil, errors.New("next edit did not return a complete prediction")
	}
	if err := modelcontract.ValidateEdits(input, result.Edits); err != nil {
		return nil, err
	}
	return result.Edits, nil
}
