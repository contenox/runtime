package vertex

import (
	"encoding/json"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type vertexRequest struct {
	SystemInstruction *vertexContent          `json:"systemInstruction,omitempty"`
	Contents          []vertexContent         `json:"contents"`
	GenerationConfig  *vertexGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []vertexToolRequest     `json:"tools,omitempty"`
}

type vertexGenerationConfig struct {
	Temperature        *float64              `json:"temperature,omitempty"`
	TopP               *float64              `json:"topP,omitempty"`
	MaxOutputTokens    *int                  `json:"maxOutputTokens,omitempty"`
	Seed               *int                  `json:"seed,omitempty"`
	ResponseMimeType   string                `json:"responseMimeType,omitempty"`
	ResponseJsonSchema any                   `json:"responseJsonSchema,omitempty"`
	ThinkingConfig     *vertexThinkingConfig `json:"thinkingConfig,omitempty"`
}

type vertexThinkingConfig struct {
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

type vertexToolRequest struct {
	FunctionDeclarations []vertexFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type vertexFunctionDeclaration struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	ParametersJsonSchema json.RawMessage `json:"parametersJsonSchema,omitempty"`
}

type vertexContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []vertexPart `json:"parts"`
}

type vertexPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *vertexInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *vertexFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *vertexFunctionResponse `json:"functionResponse,omitempty"`
}

type vertexInlineData struct {
	MimeType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

type vertexFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type vertexFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type vertexResponse struct {
	Candidates []struct {
		Content      vertexContent `json:"content"`
		FinishReason string        `json:"finishReason,omitempty"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason,omitempty"`
	} `json:"promptFeedback"`
	UsageMetadata *vertexUsageMetadata `json:"usageMetadata,omitempty"`
}

type vertexUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

func (u *vertexUsageMetadata) neutralUsage() *modelrepo.TokenUsage {
	if u == nil {
		return nil
	}
	total := u.TotalTokenCount
	if total == 0 {
		total = u.PromptTokenCount + u.CandidatesTokenCount + u.ThoughtsTokenCount
	}
	return &modelrepo.TokenUsage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.CandidatesTokenCount + u.ThoughtsTokenCount,
		ThinkingTokens:   u.ThoughtsTokenCount,
		TotalTokens:      total,
		CacheReadTokens:  u.CachedContentTokenCount,
	}
}

type vertexErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

type vertexContinuation struct {
	Parts []vertexPart `json:"parts"`
}

const continuationProvider = "vertex"

const thoughtSignatureMetaKey = "thought_signature"

const skipThoughtSignatureValidator = "skip_thought_signature_validator"

func replayContinuation(continuation *modelrepo.Continuation, modelName string) ([]vertexPart, bool) {
	if continuation == nil || continuation.Provider != continuationProvider || continuation.Model != modelName {
		return nil, false
	}
	var stored vertexContinuation
	if err := json.Unmarshal(continuation.Items, &stored); err != nil {
		return nil, false
	}
	if len(stored.Parts) == 0 {
		return nil, false
	}
	return stored.Parts, true
}

func responseContinuation(modelName string, parts []vertexPart) *modelrepo.Continuation {
	if len(parts) == 0 {
		return nil
	}
	raw, err := json.Marshal(vertexContinuation{Parts: parts})
	if err != nil {
		return nil
	}
	return &modelrepo.Continuation{Provider: continuationProvider, Model: modelName, Items: raw}
}
