package inferencesetup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

const verificationCompletionTokens = 128

// OperationDeps are the collaborators OperationModelFactory requires; every field must be set.
type OperationDeps struct {
	DB      libdbexec.DBManager
	Bus     libbus.Messenger
	State   *runtimestate.State
	Tracker libtracker.ActivityTracker
	// Models executes native completion, prompt completion and next edit on the selected route.
	Models llmrepo.ModelRepo
	// Agent runs the instructed completion chain.
	Agent codecompletion.Prompter
	// Chain is the loaded instructed completion chain.
	Chain *taskengine.TaskChainDefinition
}

// OperationModelFactory serves ModelFactory, CompletionFactory and NextEditFactory from the production completion and next-edit services.
type OperationModelFactory struct {
	RuntimeModelFactory
	models llmrepo.ModelRepo
	agent  codecompletion.Prompter
	chain  *taskengine.TaskChainDefinition
}

// NewOperationModelFactory refuses a construction that lacks any dependency, naming it.
func NewOperationModelFactory(deps OperationDeps) (*OperationModelFactory, error) {
	var missing []string
	if deps.DB == nil {
		missing = append(missing, "database")
	}
	if deps.Bus == nil {
		missing = append(missing, "bus")
	}
	if deps.State == nil {
		missing = append(missing, "runtime state")
	}
	if deps.Tracker == nil {
		missing = append(missing, "tracker")
	}
	if deps.Models == nil {
		missing = append(missing, "model repository")
	}
	if deps.Agent == nil {
		missing = append(missing, "completion agent")
	}
	if deps.Chain == nil {
		missing = append(missing, "completion chain")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("operation model factory requires: %s", strings.Join(missing, ", "))
	}
	return &OperationModelFactory{
		RuntimeModelFactory: RuntimeModelFactory{DB: deps.DB, Bus: deps.Bus, State: deps.State, Tracker: deps.Tracker},
		models:              deps.Models, agent: deps.Agent, chain: deps.Chain,
	}, nil
}

func (f *OperationModelFactory) completionService(mode string) (*codecompletion.Service, error) {
	switch mode {
	case modelregistry.CompletionFIM:
		return codecompletion.NewNative(f.models)
	case modelregistry.CompletionInstruct:
		return codecompletion.New(f.agent, f.chain, f.models)
	case modelregistry.CompletionPrompt:
		return codecompletion.NewPromptOnly(f.models, f.models)
	default:
		return nil, errors.New("autocomplete requires an explicit completion mode")
	}
}

// CompletionFingerprint identifies the loaded instructed completion chain.
func (f *OperationModelFactory) CompletionFingerprint(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	service, err := f.completionService(modelregistry.CompletionInstruct)
	if err != nil {
		return "", err
	}
	return service.Fingerprint()
}

// ExecutionFingerprint identifies the completion operation selected by mode and FIM format.
func (f *OperationModelFactory) ExecutionFingerprint(ctx context.Context, mode, format string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	service, err := f.completionService(mode)
	if err != nil {
		return "", err
	}
	return service.ExecutionFingerprint(mode, format)
}

// Complete runs the production completion operation the assigned route declares.
func (f *OperationModelFactory) Complete(ctx context.Context, assignment Assignment, prefix, suffix string) (string, error) {
	mode, _, err := completionContract(ctx, f.State, assignment)
	if err != nil {
		return "", err
	}
	service, err := f.completionService(mode)
	if err != nil {
		return "", err
	}
	vars := map[string]string{
		"provider": assignment.Provider, "model": assignment.Model,
		"default_provider": assignment.Provider, "default_model": assignment.Model,
		"max_tokens": fmt.Sprint(verificationCompletionTokens),
	}
	store := runtimetypes.New(f.DB.WithoutTransaction())
	if val, _ := clikv.ReadConfig(ctx, store, "", "default-autocomplete-think"); strings.TrimSpace(val) != "" {
		think := strings.TrimSpace(val)
		vars["think"] = think
	}
	return service.Complete(ctx, codecompletion.Request{
		Mode: mode, Provider: assignment.Provider, Model: assignment.Model,
		Prefix: prefix, Suffix: suffix, MaxTokens: verificationCompletionTokens, TemplateVars: vars,
	})
}

// NextEdit runs the production next-edit operation the assigned route declares.
func (f *OperationModelFactory) NextEdit(ctx context.Context, assignment Assignment, input modelcontract.NextEditInput) ([]modelcontract.TextEdit, error) {
	format, err := nextEditContract(ctx, f.State, assignment)
	if err != nil {
		return nil, err
	}
	profile, err := modelcontract.ResolveNextEdit(format)
	if err != nil {
		return nil, err
	}
	service, err := codecompletion.NewNextEdit(f.models)
	if err != nil {
		return nil, err
	}
	return service.Predict(ctx, codecompletion.NextEditRequest{
		Provider: assignment.Provider, Model: assignment.Model, Format: format, Input: input, MaxTokens: profile.MaxTokens,
	})
}

// NextEditExecutionFingerprint identifies the next-edit operation for format.
func (f *OperationModelFactory) NextEditExecutionFingerprint(ctx context.Context, format string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	service, err := codecompletion.NewNextEdit(f.models)
	if err != nil {
		return "", err
	}
	return service.ExecutionFingerprint(format)
}

var (
	_ ModelFactory      = (*OperationModelFactory)(nil)
	_ CompletionFactory = (*OperationModelFactory)(nil)
	_ NextEditFactory   = (*OperationModelFactory)(nil)
)
