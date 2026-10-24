package ollama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type Capability string

const (
	CapabilityCompletion = Capability("completion")
	CapabilityTools      = Capability("tools")
	CapabilityInsert     = Capability("insert")
	CapabilityVision     = Capability("vision")
	CapabilityEmbedding  = Capability("embedding")
	CapabilityThinking   = Capability("thinking")
	CapabilityImage      = Capability("image")
	CapabilityAudio      = Capability("audio")
	CapabilityDecision   = Capability("decision")
)

type ImageData []byte

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration < 0 {
		return []byte("-1"), nil
	}
	return []byte("\"" + d.Duration.String() + "\""), nil
}

func (d *Duration) UnmarshalJSON(b []byte) (err error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}

	d.Duration = 5 * time.Minute

	switch t := v.(type) {
	case float64:
		if t < 0 {
			d.Duration = time.Duration(math.MaxInt64)
		} else {
			d.Duration = time.Duration(t * float64(time.Second))
		}
	case string:
		d.Duration, err = time.ParseDuration(t)
		if err != nil {
			return err
		}
		if d.Duration < 0 {
			d.Duration = time.Duration(math.MaxInt64)
		}
	default:
		return fmt.Errorf("unsupported duration type: '%T'", v)
	}

	return nil
}

type ThinkValue struct {
	Value any
}

func (t *ThinkValue) MarshalJSON() ([]byte, error) {
	if t == nil || t.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(t.Value)
}

func (t *ThinkValue) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch value.(type) {
	case nil, bool, string:
		t.Value = value
		return nil
	default:
		return fmt.Errorf("think must be a boolean or a level name")
	}
}

type ThinkingMetadata struct {
	Values  []any `json:"values"`
	Default any   `json:"default"`
}

func (t *ThinkingMetadata) Supports(value any) bool {
	if t == nil {
		return false
	}
	for _, declared := range t.Values {
		if declared == value {
			return true
		}
	}
	return false
}

func (t *ThinkingMetadata) Levels() []string {
	if t == nil {
		return nil
	}
	levels := make([]string, 0, len(t.Values))
	for _, value := range t.Values {
		if level, ok := value.(string); ok && level != "" {
			levels = append(levels, level)
		}
	}
	return levels
}

type orderedMap[V any] struct {
	keys   []string
	values map[string]V
}

func newOrderedMap[V any]() *orderedMap[V] {
	return &orderedMap[V]{values: make(map[string]V)}
}

func (m *orderedMap[V]) set(key string, value V) {
	if m.values == nil {
		m.values = make(map[string]V)
	}
	if _, ok := m.values[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *orderedMap[V]) MarshalJSON() ([]byte, error) {
	if m == nil || m.values == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(m.values[key])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (m *orderedMap[V]) UnmarshalJSON(data []byte) error {
	m.keys = nil
	m.values = nil
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected JSON object, got %v", tok)
	}

	m.values = make(map[string]V)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("expected string key, got %v", keyTok)
		}
		var value V
		if err := dec.Decode(&value); err != nil {
			return err
		}
		m.set(key, value)
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

type Message struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	Thinking   string      `json:"thinking,omitempty"`
	Images     []ImageData `json:"images,omitempty"`
	Audios     []ImageData `json:"audios,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolName   string      `json:"tool_name,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

func (m *Message) UnmarshalJSON(b []byte) error {
	type Alias Message
	var a Alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}

	*m = Message(a)
	m.Role = strings.ToLower(m.Role)
	return nil
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Index     int                       `json:"index"`
	Name      string                    `json:"name"`
	Arguments ToolCallFunctionArguments `json:"arguments"`
}

type ToolCallFunctionArguments struct {
	om *orderedMap[any]
}

func (t ToolCallFunctionArguments) MarshalJSON() ([]byte, error) {
	if t.om == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(t.om)
}

func (t *ToolCallFunctionArguments) UnmarshalJSON(data []byte) error {
	t.om = newOrderedMap[any]()
	return json.Unmarshal(data, t.om)
}

func (t *ToolCallFunctionArguments) String() string {
	if t == nil || t.om == nil {
		return "{}"
	}
	bts, _ := json.Marshal(t.om)
	return string(bts)
}

type Tools []Tool

type Tool struct {
	Type     string       `json:"type"`
	Items    any          `json:"items,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  ToolFunctionParameters `json:"parameters"`
}

type ToolFunctionParameters struct {
	Type       string             `json:"type"`
	Defs       any                `json:"$defs,omitempty"`
	Items      any                `json:"items,omitempty"`
	Required   []string           `json:"required,omitempty"`
	Properties *ToolPropertiesMap `json:"properties"`
}

type ToolPropertiesMap struct {
	om *orderedMap[ToolProperty]
}

func (t ToolPropertiesMap) MarshalJSON() ([]byte, error) {
	if t.om == nil {
		return []byte("null"), nil
	}
	return json.Marshal(t.om)
}

func (t *ToolPropertiesMap) UnmarshalJSON(data []byte) error {
	t.om = newOrderedMap[ToolProperty]()
	return json.Unmarshal(data, t.om)
}

type ToolProperty struct {
	AnyOf       []ToolProperty     `json:"anyOf,omitempty"`
	Type        PropertyType       `json:"type,omitempty"`
	Items       any                `json:"items,omitempty"`
	Description string             `json:"description,omitempty"`
	Enum        []any              `json:"enum,omitempty"`
	Properties  *ToolPropertiesMap `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
}

type PropertyType []string

func (pt PropertyType) MarshalJSON() ([]byte, error) {
	if len(pt) == 1 {
		return json.Marshal(pt[0])
	}
	return json.Marshal([]string(pt))
}

func (pt *PropertyType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*pt = []string{s}
		return nil
	}

	var a []string
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*pt = a
	return nil
}

type GenerateRequest struct {
	Model           string          `json:"model"`
	Runner          string          `json:"runner,omitempty"`
	Prompt          string          `json:"prompt"`
	Suffix          string          `json:"suffix"`
	System          string          `json:"system"`
	Template        string          `json:"template"`
	Context         []int           `json:"context,omitempty"`
	Stream          *bool           `json:"stream,omitempty"`
	Raw             bool            `json:"raw,omitempty"`
	Format          json.RawMessage `json:"format,omitempty"`
	KeepAlive       *Duration       `json:"keep_alive,omitempty"`
	Images          []ImageData     `json:"images,omitempty"`
	Options         map[string]any  `json:"options"`
	Think           *ThinkValue     `json:"think,omitempty"`
	Truncate        *bool           `json:"truncate,omitempty"`
	Shift           *bool           `json:"shift,omitempty"`
	DebugRenderOnly bool            `json:"_debug_render_only,omitempty"`
	Logprobs        bool            `json:"logprobs,omitempty"`
	TopLogprobs     int             `json:"top_logprobs,omitempty"`
	Width           int32           `json:"width,omitempty"`
	Height          int32           `json:"height,omitempty"`
	Steps           int32           `json:"steps,omitempty"`
}

type ChatRequest struct {
	Model           string          `json:"model"`
	Runner          string          `json:"runner,omitempty"`
	Messages        []Message       `json:"messages"`
	Stream          *bool           `json:"stream,omitempty"`
	Format          json.RawMessage `json:"format,omitempty"`
	KeepAlive       *Duration       `json:"keep_alive,omitempty"`
	Tools           `json:"tools,omitempty"`
	Options         map[string]any `json:"options"`
	Think           *ThinkValue    `json:"think,omitempty"`
	Truncate        *bool          `json:"truncate,omitempty"`
	Shift           *bool          `json:"shift,omitempty"`
	DebugRenderOnly bool           `json:"_debug_render_only,omitempty"`
	Logprobs        bool           `json:"logprobs,omitempty"`
	TopLogprobs     int            `json:"top_logprobs,omitempty"`
	Session         string         `json:"contenox_session,omitempty"`
}

type Metrics struct {
	TotalDuration         time.Duration `json:"total_duration,omitempty"`
	PeakMemory            uint64        `json:"peak_memory,omitempty"`
	LoadDuration          time.Duration `json:"load_duration,omitempty"`
	PromptEvalCount       int           `json:"prompt_eval_count,omitempty"`
	PromptEvalCachedCount *int          `json:"prompt_eval_cached_count,omitempty"`
	PromptEvalDuration    time.Duration `json:"prompt_eval_duration,omitempty"`
	EvalCount             int           `json:"eval_count,omitempty"`
	EvalDuration          time.Duration `json:"eval_duration,omitempty"`
}

func (m Metrics) Usage() *modelrepo.TokenUsage {
	usage := &modelrepo.TokenUsage{
		PromptTokens:     m.PromptEvalCount,
		CompletionTokens: m.EvalCount,
		TotalTokens:      m.PromptEvalCount + m.EvalCount,
	}
	if m.PromptEvalCachedCount != nil {
		usage.CacheReadTokens = *m.PromptEvalCachedCount
	}
	return usage
}

type DebugInfo struct {
	RenderedTemplate string `json:"rendered_template"`
	ImageCount       int    `json:"image_count,omitempty"`
}

type TokenLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
	Bytes   []int   `json:"bytes,omitempty"`
}

type Logprob struct {
	TokenLogprob
	TopLogprobs []TokenLogprob `json:"top_logprobs,omitempty"`
}

type ChatResponse struct {
	Model       string     `json:"model"`
	RemoteModel string     `json:"remote_model,omitempty"`
	RemoteHost  string     `json:"remote_host,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Message     Message    `json:"message"`
	Done        bool       `json:"done"`
	DoneReason  string     `json:"done_reason,omitempty"`
	DebugInfo   *DebugInfo `json:"_debug_info,omitempty"`
	Logprobs    []Logprob  `json:"logprobs,omitempty"`

	Metrics
}

type GenerateResponse struct {
	Model       string    `json:"model"`
	RemoteModel string    `json:"remote_model,omitempty"`
	RemoteHost  string    `json:"remote_host,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Response    string    `json:"response"`
	Thinking    string    `json:"thinking,omitempty"`
	Done        bool      `json:"done"`
	DoneReason  string    `json:"done_reason,omitempty"`
	Context     []int     `json:"context,omitempty"`

	Metrics

	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	DebugInfo *DebugInfo `json:"_debug_info,omitempty"`
	Logprobs  []Logprob  `json:"logprobs,omitempty"`
	Image     string     `json:"image,omitempty"`
	Completed int64      `json:"completed,omitempty"`
	Total     int64      `json:"total,omitempty"`
}

type EmbedRequest struct {
	Model      string         `json:"model"`
	Input      any            `json:"input"`
	KeepAlive  *Duration      `json:"keep_alive,omitempty"`
	Truncate   *bool          `json:"truncate,omitempty"`
	Dimensions int            `json:"dimensions,omitempty"`
	Options    map[string]any `json:"options"`
}

type EmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`

	TotalDuration   time.Duration `json:"total_duration,omitempty"`
	LoadDuration    time.Duration `json:"load_duration,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
}

type DeleteRequest struct {
	Model string `json:"model"`

	Name string `json:"name"`
}

type ShowRequest struct {
	Model        string `json:"model"`
	Runner       string `json:"runner,omitempty"`
	AllManifests bool   `json:"all_manifests,omitempty"`
	System       string `json:"system"`

	Template string `json:"template"`
	Verbose  bool   `json:"verbose"`

	Options map[string]any `json:"options"`

	Name string `json:"name"`
}

type ShowResponse struct {
	License        string            `json:"license,omitempty"`
	Modelfile      string            `json:"modelfile,omitempty"`
	Parameters     string            `json:"parameters,omitempty"`
	Template       string            `json:"template,omitempty"`
	System         string            `json:"system,omitempty"`
	Renderer       string            `json:"renderer,omitempty"`
	Parser         string            `json:"parser,omitempty"`
	Details        ModelDetails      `json:"details,omitempty"`
	Messages       []Message         `json:"messages,omitempty"`
	RemoteModel    string            `json:"remote_model,omitempty"`
	RemoteHost     string            `json:"remote_host,omitempty"`
	ModelInfo      map[string]any    `json:"model_info"`
	ProjectorInfo  map[string]any    `json:"projector_info,omitempty"`
	Tensors        []Tensor          `json:"tensors,omitempty"`
	Capabilities   []Capability      `json:"capabilities,omitempty"`
	Thinking       *ThinkingMetadata `json:"thinking,omitempty"`
	Manifests      []ManifestSummary `json:"manifests,omitempty"`
	NextEditFormat string            `json:"next_edit_format,omitempty"`
	CanNextEdit    bool              `json:"next_edit,omitempty"`
	ModifiedAt     time.Time         `json:"modified_at,omitempty"`
	Requires       string            `json:"requires,omitempty"`
}

type ManifestSummary struct {
	Digest   string `json:"digest,omitempty"`
	Runner   string `json:"runner,omitempty"`
	Format   string `json:"format,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

func (r *ShowResponse) SupportsInsert() bool {
	if r == nil {
		return false
	}
	return slices.Contains(r.Capabilities, CapabilityInsert)
}

type Tensor struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Shape []uint64 `json:"shape"`
}

type ListResponse struct {
	Models []ListModelResponse `json:"models"`
}

type ListModelResponse struct {
	Name        string       `json:"name"`
	Model       string       `json:"model"`
	RemoteModel string       `json:"remote_model,omitempty"`
	RemoteHost  string       `json:"remote_host,omitempty"`
	ModifiedAt  time.Time    `json:"modified_at"`
	Size        int64        `json:"size"`
	Digest      string       `json:"digest"`
	Details     ModelDetails `json:"details,omitempty"`
}

type ModelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
	ContextLength     int      `json:"context_length,omitempty"`
	EmbeddingLength   int      `json:"embedding_length,omitempty"`
	Runner            string   `json:"runner,omitempty"`
}
