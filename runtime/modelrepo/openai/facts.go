package openai

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const reasoningOffLiteral = "none"

type openAIModelFact struct {
	Reasoning             bool
	Efforts               []string
	MaxOutputTokens       int
	Vision                bool
	ResponsesOnly         bool
	ChatOnly              bool
	ToolsRequireResponses bool
}

var openAIModelFacts = map[string]openAIModelFact{
	"gpt-6-astra": {
		Reasoning:             true,
		Efforts:               []string{reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max},
		MaxOutputTokens:       128000,
		Vision:                true,
		ToolsRequireResponses: true,
	},
	"gpt-6-sol": {
		Reasoning:             true,
		Efforts:               []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max},
		MaxOutputTokens:       128000,
		Vision:                true,
		ToolsRequireResponses: true,
	},
	"gpt-6.1-sol": {
		Reasoning:             true,
		Efforts:               []string{reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max},
		MaxOutputTokens:       128000,
		Vision:                true,
		ToolsRequireResponses: true,
	},
	"gpt-6-luna": {
		Reasoning:             true,
		Efforts:               []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max},
		MaxOutputTokens:       128000,
		Vision:                true,
		ToolsRequireResponses: true,
	},
	"gpt-5.6": {
		Reasoning:       true,
		Efforts:         []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max},
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5.6-cyber": {
		Reasoning:       true,
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.5": {
		Reasoning:       true,
		Efforts:         []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5.5-pro": {
		Reasoning:       true,
		Efforts:         []string{reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.4": {
		Reasoning:       true,
		Efforts:         []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5.4-pro": {
		Reasoning:       true,
		Efforts:         []string{reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.3-codex": {
		Reasoning:       true,
		Efforts:         []string{reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.2": {
		Reasoning:       true,
		Efforts:         []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5.2-pro": {
		Reasoning:       true,
		Efforts:         []string{reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.2-codex": {
		Reasoning:       true,
		Efforts:         []string{reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh},
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.1-codex-max": {
		Reasoning:       true,
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.1-codex": {
		Reasoning:       true,
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5.1": {
		Reasoning:       true,
		Efforts:         []string{reasoningOffLiteral, reasoning.Low, reasoning.Medium, reasoning.High},
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5-chat": {
		MaxOutputTokens: 16384,
		Vision:          true,
	},
	"gpt-5-codex": {
		Reasoning:       true,
		MaxOutputTokens: 128000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5-mini": {
		Reasoning:       true,
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5-nano": {
		Reasoning:       true,
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"gpt-5-pro": {
		Reasoning:       true,
		Efforts:         []string{reasoning.High},
		MaxOutputTokens: 272000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"gpt-5": {
		Reasoning:       true,
		Efforts:         []string{reasoning.Minimal, reasoning.Low, reasoning.Medium, reasoning.High},
		MaxOutputTokens: 128000,
		Vision:          true,
	},
	"o4-mini": {
		Reasoning:       true,
		MaxOutputTokens: 100000,
		Vision:          true,
	},
	"o3-pro": {
		Reasoning:       true,
		MaxOutputTokens: 100000,
		Vision:          true,
		ResponsesOnly:   true,
	},
	"o3-mini": {
		Reasoning:       true,
		MaxOutputTokens: 100000,
	},
	"o3": {
		Reasoning:       true,
		MaxOutputTokens: 100000,
		Vision:          true,
	},
	"o1-preview": {
		Reasoning:       true,
		MaxOutputTokens: 32768,
		ChatOnly:        true,
	},
	"o1-mini": {
		Reasoning:       true,
		MaxOutputTokens: 65536,
		ChatOnly:        true,
	},
	"o1": {
		Reasoning:       true,
		MaxOutputTokens: 100000,
		Vision:          true,
	},
	"gpt-4.1": {
		MaxOutputTokens: 32768,
		Vision:          true,
	},
	"gpt-4o": {
		MaxOutputTokens: 16384,
		Vision:          true,
	},
	"gpt-4-turbo-preview": {},
	"gpt-4-turbo": {
		Vision: true,
	},
	"chatgpt-4o": {
		Vision: true,
	},
	"computer-use": {
		Vision: true,
	},
}

var openAIModelFactKeys = func() []string {
	keys := make([]string, 0, len(openAIModelFacts))
	for key := range openAIModelFacts {
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

func openAIModelFactFor(model string) (openAIModelFact, bool) {
	base := openAIAPIBaseModelID(model)
	if fact, ok := openAIModelFacts[base]; ok {
		return fact, true
	}
	for _, key := range openAIModelFactKeys {
		if strings.HasPrefix(base, key+"-") {
			return openAIModelFacts[key], true
		}
	}
	return openAIModelFact{}, false
}

func openAIToolsRequireResponses(model string) bool {
	fact, ok := openAIModelFactFor(model)
	return ok && fact.ToolsRequireResponses
}

func openAIModelReasons(model string) bool {
	fact, ok := openAIModelFactFor(model)
	return ok && fact.Reasoning
}

func openAIUsesResponsesEndpoint(model string, withTools bool) bool {
	fact, ok := openAIModelFactFor(model)
	if !ok {
		return false
	}
	if fact.ChatOnly {
		return false
	}
	if fact.ResponsesOnly {
		return true
	}
	if withTools && fact.ToolsRequireResponses {
		return true
	}
	return fact.Reasoning
}

func openAIReasoningEffort(model string, think *string, declared *modelrepo.ThinkingTypes) (string, error) {
	if think == nil {
		return "", nil
	}
	level, ok, err := reasoning.NormalizeOptional(*think)
	if err != nil {
		return "", err
	}
	if !ok || level == reasoning.Auto {
		return "", nil
	}

	fact, documented := openAIModelFactFor(model)
	if documented {
		if level == reasoning.Off && !slices.Contains(fact.Efforts, reasoningOffLiteral) {
			return "", nil
		}
		if len(fact.Efforts) == 0 {
			return wireEffort(level), nil
		}
		wire := wireEffort(level)
		if !slices.Contains(fact.Efforts, wire) {
			return "", fmt.Errorf("model %s does not support reasoning effort %q (documented: %s)",
				model, level, strings.Join(fact.Efforts, ", "))
		}
		return wire, nil
	}

	if declared != nil {
		if level == reasoning.Off {
			if declared.Disabled {
				return reasoningOffLiteral, nil
			}
			return "", nil
		}
		if len(declared.EffortLevels) > 0 && !declared.SupportsEffort(level) {
			return "", fmt.Errorf("model %s declares no reasoning effort %q (declared: %s)",
				model, level, strings.Join(declared.EffortLevels, ", "))
		}
		return wireEffort(level), nil
	}

	return wireEffort(level), nil
}

func wireEffort(level string) string {
	if level == reasoning.Off {
		return reasoningOffLiteral
	}
	return level
}

func openAIShouldOmitSamplingParams(model, reasoningEffort string) bool {
	fact, ok := openAIModelFactFor(model)
	if !ok || !fact.Reasoning {
		return false
	}
	if len(fact.Efforts) == 0 {
		return true
	}
	if reasoningEffort == "" {
		return !slices.Contains(fact.Efforts, reasoningOffLiteral)
	}
	return reasoningEffort != reasoningOffLiteral
}

func openAIModelSupportsVision(model string) bool {
	fact, ok := openAIModelFactFor(model)
	return ok && fact.Vision
}

func openAIMaxOutputTokens(model string) int {
	fact, ok := openAIModelFactFor(model)
	if !ok {
		return 0
	}
	return fact.MaxOutputTokens
}

func openAIThinkingDeclaration(model string, declared *modelrepo.ThinkingTypes) *modelrepo.ThinkingTypes {
	if documented := openAIThinkingTypes(model); documented != nil {
		return documented
	}
	if declared == nil {
		return nil
	}
	if err := declared.Validate(); err != nil {
		return nil
	}
	return declared
}

func openAIThinkingTypes(model string) *modelrepo.ThinkingTypes {
	fact, ok := openAIModelFactFor(model)
	if !ok || !fact.Reasoning {
		return nil
	}
	declared := &modelrepo.ThinkingTypes{Disabled: slices.Contains(fact.Efforts, reasoningOffLiteral)}
	for _, effort := range fact.Efforts {
		switch effort {
		case reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max:
			declared.EffortLevels = append(declared.EffortLevels, effort)
		}
	}
	if err := declared.Validate(); err != nil {
		return nil
	}
	return declared
}
