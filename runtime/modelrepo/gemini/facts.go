package gemini

import (
	"fmt"
	"sort"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const (
	vendorLevelMinimal = "MINIMAL"
	vendorLevelLow     = "LOW"
	vendorLevelMedium  = "MEDIUM"
	vendorLevelHigh    = "HIGH"
)

const documentedAutoBudget = 8192

const documentedLowBudget = 1024

type ModelFacts struct {
	ContextLength   int
	MaxOutputTokens int
	ThinkingLevels  []string
	ThinkingBudget  bool
	MinBudget       int
	MaxBudget       int
	DisableThinking bool
}

var geminiModelFacts = map[string]ModelFacts{
	"gemini-4-argon": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.8-flash-cyber": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.8-flash": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.7-flash": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.6-flash": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.5-flash-lite": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.5-flash": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.1-pro": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.1-flash-lite-image": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelHigh},
	},
	"gemini-3.1-flash-lite": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-3.1-flash-image": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelHigh},
	},
	"gemini-3-pro-image": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelHigh},
	},
	"gemini-3-pro": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelLow, vendorLevelHigh},
	},
	"gemini-3-flash": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-robotics-er-2": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelLow, vendorLevelMedium, vendorLevelHigh},
	},
	"nano-banana-2.1": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingLevels:  []string{vendorLevelMinimal, vendorLevelMedium, vendorLevelHigh},
	},
	"gemini-2.5-pro": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingBudget:  true,
		MinBudget:       128,
		MaxBudget:       32768,
	},
	"gemini-2.5-flash-lite": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingBudget:  true,
		MinBudget:       512,
		MaxBudget:       24576,
		DisableThinking: true,
	},
	"gemini-2.5-flash": {
		ContextLength:   1048576,
		MaxOutputTokens: 65536,
		ThinkingBudget:  true,
		MinBudget:       1,
		MaxBudget:       24576,
		DisableThinking: true,
	},
}

var geminiModelFactKeys = func() []string {
	keys := make([]string, 0, len(geminiModelFacts))
	for key := range geminiModelFacts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}()

func ModelFactsFor(model string) (ModelFacts, bool) {
	base := normalizeModelID(model)
	if fact, ok := geminiModelFacts[base]; ok {
		return fact, true
	}
	for _, key := range geminiModelFactKeys {
		if strings.HasPrefix(base, key+"-") {
			return geminiModelFacts[key], true
		}
	}
	return ModelFacts{}, false
}

func normalizeModelID(model string) string {
	base := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	return base
}

func ThinkingDeclarationFor(model string) *modelrepo.ThinkingTypes {
	fact, ok := ModelFactsFor(model)
	if !ok {
		return nil
	}
	declared := &modelrepo.ThinkingTypes{
		Enabled:  fact.ThinkingBudget,
		Disabled: fact.DisableThinking,
	}
	for _, level := range fact.ThinkingLevels {
		if runtimeLevel, ok := runtimeLevelFor(level); ok && runtimeLevel != reasoning.Minimal {
			declared.EffortLevels = append(declared.EffortLevels, runtimeLevel)
		}
	}
	if err := declared.Validate(); err != nil {
		return nil
	}
	if len(declared.EffortLevels) == 0 && !declared.Enabled {
		return nil
	}
	return declared
}

type ThinkingControl struct {
	Level           string
	Budget          *int
	IncludeThoughts bool
}

func ThinkingControlFor(model string, think *string) (*ThinkingControl, error) {
	if think == nil {
		return nil, nil
	}
	level, ok, err := reasoning.NormalizeOptional(*think)
	if err != nil {
		return nil, err
	}
	if !ok || level == reasoning.Auto {
		return nil, nil
	}

	fact, documented := ModelFactsFor(model)
	if !documented {
		return nil, fmt.Errorf("gemini: model %s publishes no thinking control", model)
	}
	if level == reasoning.Off && (!fact.ThinkingBudget || !fact.DisableThinking) {
		return nil, nil
	}
	if fact.ThinkingBudget {
		budget, err := budgetFor(model, fact, level)
		if err != nil {
			return nil, err
		}
		return &ThinkingControl{Budget: &budget, IncludeThoughts: level != reasoning.Off}, nil
	}

	vendorLevel, err := levelFor(model, fact, level)
	if err != nil {
		return nil, err
	}
	return &ThinkingControl{Level: vendorLevel, IncludeThoughts: true}, nil
}

func budgetFor(model string, fact ModelFacts, level string) (int, error) {
	switch level {
	case reasoning.Off:
		return 0, nil
	case reasoning.Minimal:
		return fact.MinBudget, nil
	case reasoning.Low:
		return clampBudget(documentedLowBudget, fact), nil
	case reasoning.Medium:
		return clampBudget(documentedAutoBudget, fact), nil
	case reasoning.High, reasoning.XHigh, reasoning.Max:
		return fact.MaxBudget, nil
	default:
		return 0, &modelrepo.UnsupportedDialectLevelError{Dialect: "gemini", Level: level}
	}
}

func levelFor(model string, fact ModelFacts, level string) (string, error) {
	requested, ok := vendorRank(level)
	if !ok {
		return "", &modelrepo.UnsupportedDialectLevelError{Dialect: "gemini", Level: level}
	}
	best := ""
	bestRank := -1
	lowest := ""
	lowestRank := 0
	for _, candidate := range fact.ThinkingLevels {
		rank, ok := vendorLevelRank(candidate)
		if !ok {
			continue
		}
		if lowest == "" || rank < lowestRank {
			lowest, lowestRank = candidate, rank
		}
		if rank <= requested && rank > bestRank {
			best, bestRank = candidate, rank
		}
	}
	if best != "" {
		return best, nil
	}
	if lowest != "" {
		return lowest, nil
	}
	return "", fmt.Errorf("gemini: model %s publishes no thinking level for level %q", model, level)
}

func clampBudget(budget int, fact ModelFacts) int {
	if budget < fact.MinBudget {
		return fact.MinBudget
	}
	if budget > fact.MaxBudget {
		return fact.MaxBudget
	}
	return budget
}

func runtimeLevelFor(vendorLevel string) (string, bool) {
	switch vendorLevel {
	case vendorLevelMinimal:
		return reasoning.Minimal, true
	case vendorLevelLow:
		return reasoning.Low, true
	case vendorLevelMedium:
		return reasoning.Medium, true
	case vendorLevelHigh:
		return reasoning.High, true
	default:
		return "", false
	}
}

func vendorRank(level string) (int, bool) {
	switch level {
	case reasoning.Minimal:
		return 0, true
	case reasoning.Low:
		return 1, true
	case reasoning.Medium:
		return 2, true
	case reasoning.High:
		return 3, true
	case reasoning.XHigh:
		return 4, true
	case reasoning.Max:
		return 5, true
	default:
		return 0, false
	}
}

func vendorLevelRank(vendorLevel string) (int, bool) {
	switch vendorLevel {
	case vendorLevelMinimal:
		return 0, true
	case vendorLevelLow:
		return 1, true
	case vendorLevelMedium:
		return 2, true
	case vendorLevelHigh:
		return 3, true
	default:
		return 0, false
	}
}
