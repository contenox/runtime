package inferencesetup

import (
	"context"
	"errors"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimestate"
)

// CompletionMode resolves the selected connection's declared production operation.
func (s *Service) CompletionMode(ctx context.Context, assignment Assignment) (string, error) {
	mode, _, err := s.CompletionContract(ctx, assignment)
	return mode, err
}

// CompletionContract resolves the selected connection's completion mode and FIM format.
func (s *Service) CompletionContract(ctx context.Context, assignment Assignment) (string, string, error) {
	return completionContract(ctx, s.state, assignment)
}

func completionContract(ctx context.Context, state *runtimestate.State, assignment Assignment) (string, string, error) {
	mode := ""
	fimFormat := ""
	for _, backend := range state.Get(ctx) {
		if !runtimestate.MatchesBackend(assignment.Provider, backend.Backend) || backend.Error != "" {
			continue
		}
		for _, candidate := range backend.PulledModels {
			name := candidate.Model
			if name == "" {
				name = candidate.Name
			}
			if name != assignment.Model {
				continue
			}
			if mode != "" {
				return "", "", errors.New("autocomplete route is ambiguous; name one connection")
			}
			switch candidate.CompletionMode {
			case modelregistry.CompletionFIM:
				if !candidate.CanAutocomplete {
					return "", "", errors.New("selected connection cannot execute native FIM")
				}
				if _, err := modelcontract.ResolveFIM(candidate.FIMFormat); err != nil {
					return "", "", err
				}
			case modelregistry.CompletionInstruct:
				if !candidate.CanChat {
					return "", "", errors.New("the instructed completion chain requires a chat-capable model")
				}
			case modelregistry.CompletionPrompt:
				if !candidate.CanPrompt {
					return "", "", errors.New("prompt completion requires a prompt-capable model")
				}
			default:
				return "", "", &CompletionContractMissingError{Connection: assignment.Provider, Model: assignment.Model}
			}
			mode, fimFormat = candidate.CompletionMode, candidate.FIMFormat
		}
	}
	if mode == "" {
		return "", "", errors.New("selected autocomplete connection and model are unavailable")
	}
	return mode, fimFormat, nil
}

// NextEditContract resolves the selected connection's declared independent edit format.
func (s *Service) NextEditContract(ctx context.Context, assignment Assignment) (string, error) {
	return nextEditContract(ctx, s.state, assignment)
}

func nextEditContract(ctx context.Context, state *runtimestate.State, assignment Assignment) (string, error) {
	format := ""
	for _, backend := range state.Get(ctx) {
		if !runtimestate.MatchesBackend(assignment.Provider, backend.Backend) || backend.Error != "" {
			continue
		}
		for _, candidate := range backend.PulledModels {
			name := candidate.Model
			if name == "" {
				name = candidate.Name
			}
			if name != assignment.Model {
				continue
			}
			if format != "" {
				return "", errors.New("next edit route is ambiguous; name one connection")
			}
			if !candidate.CanNextEdit {
				return "", errors.New("selected connection cannot execute next edit")
			}
			if _, err := modelcontract.ResolveNextEdit(candidate.NextEditFormat); err != nil {
				return "", err
			}
			format = candidate.NextEditFormat
		}
	}
	if format == "" {
		return "", errors.New("selected next edit connection and model are unavailable")
	}
	return format, nil
}
