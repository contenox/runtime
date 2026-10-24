package compatapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/taskengine"
)

type ChatCompletionRequest struct {
	Model               string        `json:"model"`
	Messages            []ChatMessage `json:"messages"`
	Stream              bool          `json:"stream"`
	Temperature         *float64      `json:"temperature,omitempty"`
	MaxTokens           *int          `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int          `json:"max_completion_tokens,omitempty"`
	Stop                []string      `json:"stop,omitempty"`
}

type ChatMessage struct {
	Role    string                 `json:"role"`
	Content string                 `json:"content"`
	Images  []taskengine.ImagePart `json:"-"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	var wire struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.Role = wire.Role
	m.Content = ""
	m.Images = nil

	trimmed := strings.TrimSpace(string(wire.Content))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		return json.Unmarshal(wire.Content, &m.Content)
	}

	var parts []chatContentPart
	if err := json.Unmarshal(wire.Content, &parts); err != nil {
		return fmt.Errorf("message content must be a string or an array of content parts: %w", err)
	}
	var texts []string
	for _, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		case "image_url":
			if part.ImageURL == nil || part.ImageURL.URL == "" {
				return fmt.Errorf("image_url content part is missing its url")
			}
			img, err := decodeImageDataURI(part.ImageURL.URL)
			if err != nil {
				return err
			}
			m.Images = append(m.Images, img)
		default:
			return fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	m.Content = strings.Join(texts, "\n")
	return nil
}

func decodeImageDataURI(uri string) (taskengine.ImagePart, error) {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return taskengine.ImagePart{}, fmt.Errorf("image_url must be a data: URI carrying the image inline")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok {
		return taskengine.ImagePart{}, fmt.Errorf("malformed data: URI in image_url content part")
	}
	mimeType := meta
	encoding := ""
	if i := strings.LastIndex(meta, ";"); i >= 0 {
		mimeType, encoding = meta[:i], meta[i+1:]
	}
	if encoding != "base64" {
		return taskengine.ImagePart{}, fmt.Errorf("image_url data: URI must be base64-encoded")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return taskengine.ImagePart{}, fmt.Errorf("image_url data: URI payload is not valid base64: %w", err)
	}
	return taskengine.ImagePart{Data: raw, MimeType: mimeType}, nil
}

type FIMCompletionRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	Suffix      string   `json:"suffix,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stream      bool     `json:"stream"`
	Stop        []string `json:"stop,omitempty"`
}

type chatCompletionChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []chatChunkChoice    `json:"choices"`
	Usage   *chatCompletionUsage `json:"usage,omitempty"`
}

type chatChunkChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type chatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type chatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatCompletionResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []chatChoiceResponse `json:"choices"`
	Usage   chatCompletionUsage  `json:"usage"`
}

type chatChoiceResponse struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type fimChunk struct {
	Choices []fimChunkChoice     `json:"choices"`
	Usage   *chatCompletionUsage `json:"usage,omitempty"`
}

type fimChunkChoice struct {
	Text         string  `json:"text"`
	FinishReason *string `json:"finish_reason"`
}

type fimCompletionResponse struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []fimChoiceResponse `json:"choices"`
	Usage   chatCompletionUsage `json:"usage"`
}

type fimChoiceResponse struct {
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
}
