package modelrepo

import (
	"sort"
	"sync"
)

type AvailabilityScoper interface {
	AvailabilityScope() string
}

var unavailableModels = struct {
	mu     sync.Mutex
	scopes map[string]map[string]struct{}
}{scopes: map[string]map[string]struct{}{}}

func MarkModelUnavailable(scope, model string) {
	if scope == "" || model == "" {
		return
	}
	unavailableModels.mu.Lock()
	defer unavailableModels.mu.Unlock()
	models := unavailableModels.scopes[scope]
	if models == nil {
		models = map[string]struct{}{}
		unavailableModels.scopes[scope] = models
	}
	models[model] = struct{}{}
}

func MarkModelAvailable(scope, model string) {
	unavailableModels.mu.Lock()
	defer unavailableModels.mu.Unlock()
	models := unavailableModels.scopes[scope]
	if _, hidden := models[model]; !hidden {
		return
	}
	delete(models, model)
	if len(models) == 0 {
		delete(unavailableModels.scopes, scope)
	}
}

func UnavailableModels(scope string) []string {
	unavailableModels.mu.Lock()
	defer unavailableModels.mu.Unlock()
	out := make([]string, 0, len(unavailableModels.scopes[scope]))
	for model := range unavailableModels.scopes[scope] {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func ResetUnavailableModels(scope string) {
	unavailableModels.mu.Lock()
	defer unavailableModels.mu.Unlock()
	delete(unavailableModels.scopes, scope)
}

func WithoutUnavailableModels(scope string, models []ObservedModel) []ObservedModel {
	if scope == "" {
		return models
	}
	unavailableModels.mu.Lock()
	hidden := unavailableModels.scopes[scope]
	unavailableModels.mu.Unlock()
	if len(hidden) == 0 {
		return models
	}
	out := make([]ObservedModel, 0, len(models))
	for _, m := range models {
		if _, skip := hidden[m.Name]; !skip {
			out = append(out, m)
		}
	}
	return out
}
