package libmodelprovider

import (
	"context"
	"fmt"
	"net/http"
)

var _ Provider = (*GeminiProvider)(nil)

type GeminiProvider struct {
	id            string
	apiKey        string
	modelName     string
	baseURL       string
	httpClient    *http.Client
	contextLength int
	canChat       bool
	canPrompt     bool
	canEmbed      bool
	canStream     bool
}

func NewGeminiProvider(apiKey string, modelName string, baseURLs []string, httpClient *http.Client) *GeminiProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if len(baseURLs) == 0 {
		baseURLs = []string{"https://generativelanguage.googleapis.com"}
	}
	var canChat, canPrompt, canEmbed, canStream bool
	contextLength := 32768
	apiBaseURL := baseURLs[0]

	switch modelName {
	default:
		canChat = true
		canPrompt = true
		canStream = true
		contextLength = 32768
	}
	id := fmt.Sprintf("gemini-%s", modelName)
	return &GeminiProvider{
		id:            id,
		apiKey:        apiKey,
		modelName:     "gemini-2.5-flash", // we always use the same model for now
		baseURL:       apiBaseURL,
		httpClient:    httpClient,
		contextLength: contextLength,
		canChat:       canChat,
		canPrompt:     canPrompt,
		canEmbed:      canEmbed,
		canStream:     canStream,
	}
}

func (p *GeminiProvider) GetBackendIDs() []string {
	return []string{"default"}
}

func (p *GeminiProvider) ModelName() string {
	return p.modelName
}

func (p *GeminiProvider) GetID() string {
	return p.id
}

func (p *GeminiProvider) GetType() string {
	return "gemini"
}

func (p *GeminiProvider) GetContextLength() int {
	return p.contextLength
}

func (p *GeminiProvider) CanChat() bool {
	return p.canChat
}

func (p *GeminiProvider) CanEmbed() bool {
	return p.canEmbed
}

func (p *GeminiProvider) CanStream() bool {
	return p.canStream
}

// CanPrompt returns true if the model supports prompting.
func (p *GeminiProvider) CanPrompt() bool {
	return p.canPrompt
}

// GetChatConnection returns an LLMChatClient for the specified backend ID.
func (p *GeminiProvider) GetChatConnection(ctx context.Context, backendID string) (LLMChatClient, error) {
	if !p.CanChat() {
		return nil, fmt.Errorf("model %s does not support chat interactions", p.modelName)
	}
	return &geminiChatClient{
		geminiClient: geminiClient{
			modelName:  p.modelName,
			baseURL:    p.baseURL,
			httpClient: p.httpClient,
			maxTokens:  p.contextLength, // Max output tokens derived from context length
			apiKey:     p.apiKey,
		},
	}, nil
}

// GetPromptConnection returns an LLMPromptExecClient for the specified backend ID.
func (p *GeminiProvider) GetPromptConnection(ctx context.Context, backendID string) (LLMPromptExecClient, error) {
	if !p.CanPrompt() {
		return nil, fmt.Errorf("model %s does not support prompt interactions", p.modelName)
	}
	return &geminiPromptClient{
		geminiClient: geminiClient{
			modelName:  p.modelName,
			baseURL:    p.baseURL,
			httpClient: p.httpClient,
			maxTokens:  p.contextLength,
			apiKey:     p.apiKey,
		},
	}, nil
}

// GetEmbedConnection returns an LLMEmbedClient for the specified backend ID.
func (p *GeminiProvider) GetEmbedConnection(ctx context.Context, backendID string) (LLMEmbedClient, error) {
	if !p.CanEmbed() {
		return nil, fmt.Errorf("model %s does not support embedding interactions", p.modelName)
	}
	return &geminiEmbedClient{
		geminiClient: geminiClient{
			modelName:  p.modelName,
			baseURL:    p.baseURL,
			httpClient: p.httpClient,
			apiKey:     p.apiKey,
		},
	}, nil
}

// GetStreamConnection returns an LLMStreamClient for the specified backend ID.
func (p *GeminiProvider) GetStreamConnection(ctx context.Context, backendID string) (LLMStreamClient, error) {
	if !p.CanStream() {
		return nil, fmt.Errorf("model %s does not support streaming interactions", p.modelName)
	}
	return &geminiStreamClient{
		geminiClient: geminiClient{
			modelName:  p.modelName,
			baseURL:    p.baseURL,
			httpClient: p.httpClient,
			maxTokens:  p.contextLength,
			apiKey:     p.apiKey,
		},
	}, nil
}

// geminiPart represents a part of the content, typically text.
type geminiPart struct {
	Text string `json:"text,omitempty"`
	// TODO: for multimodal inputs, other fields like inlineData, fileData would go here
}

// geminiContent represents a conversational turn with a role and parts.
type geminiContent struct {
	Role  string       `json:"role"` // "user" or "model"
	Parts []geminiPart `json:"parts"`
}

// geminiSystemInstruction represents the system-level instructions for the model.
type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

// geminiGenerateContentRequest is the request payload for the Gemini generateContent API.
type geminiGenerateContentRequest struct {
	// SystemInstruction provides instructions to the model that apply to the entire request.
	// This is separate from the conversational turns in 'Contents'.
	SystemInstruction *geminiSystemInstruction `json:"system_instruction,omitempty"`
	Contents          []geminiContent          `json:"contents"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
	SafetySettings    []geminiSafetySetting    `json:"safetySettings,omitempty"`
	Tools             []geminiTool             `json:"tools,omitempty"`
}

// geminiGenerationConfig defines parameters for text generation.
type geminiGenerationConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	TopK            int      `json:"topK,omitempty"`
	CandidateCount  int      `json:"candidateCount,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// geminiSafetySetting defines safety categories and their thresholds.
type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// geminiTool defines tools (e.g., function declarations) that the model can use.
type geminiTool struct {
	FunctionDeclarations []any `json:"functionDeclarations"`
}

// Gemini GenerateContent (Chat/Prompt) API response
type geminiGenerateContentResponse struct {
	Candidates []struct {
		Content          geminiContent         `json:"content"`
		FinishReason     string                `json:"finishReason,omitempty"`
		SafetyRatings    []geminiSafetySetting `json:"safetyRatings,omitempty"`
		CitationMetadata *struct {
			Citations []struct {
				StartIndex int    `json:"startIndex"`
				EndIndex   int    `json:"endIndex"`
				URI        string `json:"uri"`
				Title      string `json:"title"`
				License    string `json:"license"`
			} `json:"citations"`
		} `json:"citationMetadata,omitempty"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason   string                `json:"blockReason,omitempty"`
		SafetyRatings []geminiSafetySetting `json:"safetyRatings,omitempty"`
	}
}

// geminiEmbedContentRequest is the request payload for the Gemini embedContent API.
type geminiEmbedContentRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

// geminiEmbedContentResponse is the response payload for the Gemini embedContent API.
type geminiEmbedContentResponse struct {
	Embedding struct {
		Values []float64 `json:"values"`
	} `json:"embedding"`
}

// convertToGeminiMessages converts a slice of generic Message types to geminiContent,
// separating system instructions into a dedicated return value.
func convertToGeminiMessages(messages []Message) ([]geminiContent, geminiSystemInstruction) {
	geminiMsgs := make([]geminiContent, 0) // Initialize with 0 length as we might filter out system messages
	systemInstruction := geminiSystemInstruction{
		Parts: []geminiPart{},
	}
	for _, msg := range messages {
		// Gemini API expects "user" and "model" roles for conversational turns.
		// System instructions are handled separately.
		if msg.Role == "system" {
			systemInstruction.Parts = append(systemInstruction.Parts, geminiPart{Text: msg.Content})
		} else {
			role := msg.Role
			if role == "assistant" {
				role = "model"
			}
			geminiMsgs = append(geminiMsgs, geminiContent{
				Role:  role,
				Parts: []geminiPart{{Text: msg.Content}},
			})
		}
	}
	return geminiMsgs, systemInstruction
}
