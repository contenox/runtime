package taskengine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine/llmretry"
	"gopkg.in/yaml.v3"
)

type TaskHandler string

const (
	HandleRaiseError       TaskHandler = "raise_error"
	HandleRoute            TaskHandler = "route"
	HandleChatCompletion   TaskHandler = "chat_completion"
	HandleExecuteToolCalls TaskHandler = "execute_tool_calls"
	HandleNoop             TaskHandler = "noop"
	HandleTools            TaskHandler = "tools"
)

func (t TaskHandler) String() string {
	return string(t)
}

func IsAssistantProseHandler(handler string) bool {
	return TaskHandler(handler) == HandleChatCompletion
}

func IsToolBearingHandler(handler string) bool {
	switch TaskHandler(handler) {
	case HandleExecuteToolCalls, HandleTools, HandleChatCompletion, HandleRoute:
		return true
	default:
		return false
	}
}

const (
	TransitionExecuted      = "executed"
	TransitionToolCall      = "tool_call"
	TransitionNoop          = "noop"
	TransitionNoCallsFound  = "no_calls_found"
	TransitionToolsExecuted = "tools_executed"
	TransitionFailed        = "failed"
)

func (d DataType) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d DataType) MarshalYAML() (any, error) {
	return d.String(), nil
}

func (dt *DataType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := DataTypeFromString(s)
	if err != nil {
		return err
	}
	*dt = v
	return nil
}

func (dt *DataType) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	v, err := DataTypeFromString(s)
	if err != nil {
		return err
	}
	*dt = v
	return nil
}

type TaskTransition struct {
	OnFailure string `yaml:"on_failure" json:"on_failure" example:"error_handler"`

	Branches []TransitionBranch `yaml:"branches" json:"branches"`
}

type TransitionBranch struct {
	Operator OperatorTerm `yaml:"operator,omitempty" json:"operator,omitempty" example:"equals"`

	When string `yaml:"when" json:"when" example:"tool_call"`

	Goto string `yaml:"goto" json:"goto" example:"positive_response"`

	Edge string `yaml:"edge,omitempty" json:"edge,omitempty" example:"chat->run_tools"`
}

type OperatorTerm string

const (
	OpEquals               OperatorTerm = "equals"
	OpContains             OperatorTerm = "contains"
	OpStartsWith           OperatorTerm = "starts_with"
	OpEndsWith             OperatorTerm = "ends_with"
	OpDefault              OperatorTerm = "default"
	OpEdgeTraversedAtLeast OperatorTerm = "edge_traversed_at_least"
)

func (t OperatorTerm) String() string {
	return string(t)
}

func SupportedOperators() []string {
	return []string{
		string(OpEquals),
		string(OpContains),
		string(OpStartsWith),
		string(OpEndsWith),
		string(OpDefault),
		string(OpEdgeTraversedAtLeast),
	}
}

func ToOperatorTerm(s string) (OperatorTerm, error) {
	switch s {
	case string(OpEquals):
		return OpEquals, nil
	case string(OpContains):
		return OpContains, nil
	case string(OpStartsWith):
		return OpStartsWith, nil
	case string(OpEndsWith):
		return OpEndsWith, nil
	case string(OpDefault):
		return OpDefault, nil
	case string(OpEdgeTraversedAtLeast):
		return OpEdgeTraversedAtLeast, nil
	default:
		return "", fmt.Errorf("unsupported operator: %s", s)
	}
}

type LLMExecutionConfig struct {
	ModelRole         string                       `yaml:"-" json:"-"`
	Model             string                       `yaml:"model" json:"model" example:"llama2:7b"`
	Models            []string                     `yaml:"models,omitempty" json:"models,omitempty" example:"[\"gpt-4\", \"gpt-3.5-turbo\"]"`
	Provider          string                       `yaml:"provider,omitempty" json:"provider,omitempty" example:"ollama"`
	Providers         []string                     `yaml:"providers,omitempty" json:"providers,omitempty" example:"[\"ollama\", \"openai\"]"`
	Temperature       *float32                     `yaml:"temperature,omitempty" json:"temperature,omitempty" example:"0.7"`
	Tools             []string                     `yaml:"tools,omitempty" json:"tools,omitempty" example:"[\"local_shell\", \"nws\"]"`
	HideTools         []string                     `yaml:"hide_tools,omitempty" json:"hide_tools,omitempty" example:"[\"tool1\", \"tools_name1.tool1\"]"`
	ToolsPolicies     map[string]map[string]string `yaml:"tools_policies,omitempty" json:"tools_policies,omitempty"`
	PassClientsTools  bool                         `yaml:"pass_clients_tools" json:"pass_clients_tools"`
	Think             string                       `yaml:"think,omitempty" json:"think,omitempty" example:"high"`
	MaxTokens         *int                         `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty" example:"8192"`
	MaxTokensTemplate string                       `yaml:"-" json:"-"`
	Shift             bool                         `yaml:"shift,omitempty" json:"shift,omitempty"`
	RetryPolicy       *llmretry.RetryPolicy        `yaml:"retry_policy,omitempty" json:"retry_policy,omitempty"`
}

func (c *LLMExecutionConfig) UnmarshalJSON(data []byte) error {
	type noMethods LLMExecutionConfig
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	maxTokensRaw, hasMaxTokens := fields["max_tokens"]
	delete(fields, "max_tokens")

	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var out noMethods
	if err := json.Unmarshal(rest, &out); err != nil {
		return err
	}
	*c = LLMExecutionConfig(out)

	if !hasMaxTokens {
		return nil
	}
	return c.unmarshalMaxTokens(maxTokensRaw)
}

func (c *LLMExecutionConfig) UnmarshalYAML(value *yaml.Node) error {
	type noMethods LLMExecutionConfig
	if value == nil {
		return nil
	}

	var maxTokensNode *yaml.Node
	decodeNode := value
	if value.Kind == yaml.MappingNode {
		clone := *value
		clone.Content = make([]*yaml.Node, 0, len(value.Content))
		for i := 0; i+1 < len(value.Content); i += 2 {
			key, val := value.Content[i], value.Content[i+1]
			if key.Value == "max_tokens" {
				maxTokensNode = val
				continue
			}
			clone.Content = append(clone.Content, key, val)
		}
		decodeNode = &clone
	}

	var out noMethods
	if err := decodeNode.Decode(&out); err != nil {
		return err
	}
	*c = LLMExecutionConfig(out)

	if maxTokensNode == nil {
		return nil
	}
	return c.unmarshalMaxTokensYAML(maxTokensNode)
}

func (c LLMExecutionConfig) MarshalJSON() ([]byte, error) {
	type noMethods LLMExecutionConfig
	data, err := json.Marshal(noMethods(c))
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.MaxTokensTemplate) != "" {
		b, err := json.Marshal(c.MaxTokensTemplate)
		if err != nil {
			return nil, err
		}
		fields["max_tokens"] = b
	}
	return json.Marshal(fields)
}

func (c *LLMExecutionConfig) unmarshalMaxTokens(raw json.RawMessage) error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		c.MaxTokens = nil
		c.MaxTokensTemplate = ""
		return nil
	}

	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		c.MaxTokens = &n
		c.MaxTokensTemplate = ""
		return nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			c.MaxTokens = nil
			c.MaxTokensTemplate = ""
			return nil
		}
		if parsed, err := strconv.Atoi(s); err == nil {
			c.MaxTokens = &parsed
			c.MaxTokensTemplate = ""
			return nil
		}
		c.MaxTokens = nil
		c.MaxTokensTemplate = s
		return nil
	}

	return fmt.Errorf("max_tokens must be an integer or string macro")
}

func (c *LLMExecutionConfig) unmarshalMaxTokensYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		c.MaxTokens = nil
		c.MaxTokensTemplate = ""
		return nil
	}

	var n int
	if err := value.Decode(&n); err == nil {
		c.MaxTokens = &n
		c.MaxTokensTemplate = ""
		return nil
	}

	var s string
	if err := value.Decode(&s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			c.MaxTokens = nil
			c.MaxTokensTemplate = ""
			return nil
		}
		if parsed, err := strconv.Atoi(s); err == nil {
			c.MaxTokens = &parsed
			c.MaxTokensTemplate = ""
			return nil
		}
		c.MaxTokens = nil
		c.MaxTokensTemplate = s
		return nil
	}

	return fmt.Errorf("max_tokens must be an integer or string macro")
}

type ToolsCall struct {
	Name string `yaml:"name" json:"name" example:"slack"`

	ToolName string            `yaml:"tool_name" json:"tool_name" example:"send_slack_notification"`
	Args     map[string]string `yaml:"args,omitempty" json:"args,omitempty" example:"{\"channel\": \"#alerts\", \"message\": \"Task completed successfully\"}"`
}

type TaskDefinition struct {
	ID string `yaml:"id" json:"id" example:"validate_input" jsonschema:"required"`

	Description string `yaml:"description" json:"description" example:"Validates user input meets quality requirements"`

	Handler TaskHandler `yaml:"handler" json:"handler" example:"chat_completion" jsonschema:"required"`

	SystemInstruction string `yaml:"system_instruction,omitempty" json:"system_instruction,omitempty" example:"You are a quality control assistant. Respond only with 'valid' or 'invalid'."`

	ExecuteConfig *LLMExecutionConfig `yaml:"execute_config,omitempty" json:"execute_config,omitempty"`

	Tools *ToolsCall `yaml:"tools,omitempty" json:"tools,omitempty"`

	Print string `yaml:"print,omitempty" json:"print,omitempty" example:"Validation result: {{.validate_input}}"`

	PromptTemplate string `yaml:"prompt_template,omitempty" json:"prompt_template,omitempty" example:"Is this input valid? {{.input}}"`

	OutputTemplate string `yaml:"output_template,omitempty" json:"output_template,omitempty" example:"Tools result: {{.status}}"`

	InputVar string `yaml:"input_var,omitempty" json:"input_var,omitempty" example:"input"`

	InputMaxBytes int `yaml:"input_max_bytes,omitempty" json:"input_max_bytes,omitempty" example:"8192"`

	Transition TaskTransition `yaml:"transition" json:"transition"`

	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty" example:"30s"`

	RetryOnFailure int `yaml:"retry_on_failure,omitempty" json:"retry_on_failure,omitempty" example:"2"`
}

type ChainTerms string

const (
	TermEnd = "end"
)

type TaskChainDefinition struct {
	ID string `yaml:"id" json:"id" jsonschema:"required"`

	Debug bool `yaml:"debug" json:"debug"`

	Description string `yaml:"description" json:"description"`

	Tasks []TaskDefinition `yaml:"tasks" json:"tasks" jsonschema:"required,minItems=1"`

	TokenLimit int64 `yaml:"token_limit" json:"token_limit"`
}

type ChatHistory struct {
	Messages     []Message `json:"messages"`
	Model        string    `json:"model" example:"llama2:7b"`
	InputTokens  int       `json:"inputTokens" example:"15"`
	OutputTokens int       `json:"outputTokens" example:"10"`
	FinishReason string    `json:"finishReason,omitempty" example:"stop"`
}

type Message struct {
	Continuation    *modelrepo.Continuation `json:"continuation,omitempty"`
	ID              string                  `json:"id" example:"msg_123456"`
	Role            string                  `json:"role" example:"user"`
	Content         string                  `json:"content,omitempty" example:"What is the capital of France?"`
	Images          []ImagePart             `json:"images,omitempty"`
	Audio           []AudioPart             `json:"audio,omitempty"`
	Thinking        string                  `json:"thinking,omitempty"`
	ToolCallID      string                  `json:"tool_call_id,omitempty"`
	ToolResultError bool                    `json:"tool_result_error,omitempty"`
	CallTools       []ToolCall              `json:"callTools,omitempty"`
	Timestamp       time.Time               `json:"timestamp" example:"2023-11-15T14:30:45Z"`
	RequestID       string                  `json:"requestId,omitempty"`
	ChainRef        string                  `json:"chainRef,omitempty"`
	Author          *MessageAuthor          `json:"author,omitempty"`
}

type MessageAuthor struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type ImagePart struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type" example:"image/png"`
}

type AudioPart struct {
	Data     []byte `json:"data"`
	MimeType string `json:"mime_type" example:"audio/wav"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function FunctionTool `json:"function"`
}

type FunctionTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID           string            `json:"id" example:"call_abc123"`
	Type         string            `json:"type" example:"function"`
	Function     FunctionCall      `json:"function"`
	ProviderMeta map[string]string `json:"provider_meta,omitempty" example:"{\"thought_signature\":\"123456\"}"`
	ArgsMissing  bool              `json:"-"`
}

type FunctionCall struct {
	Name      string `json:"name" example:"get_current_weather"`
	Arguments string `json:"arguments" example:"{\n  \"location\": \"San Francisco, CA\",\n  \"unit\": \"celsius\"\n}"`
}

type FunctionCallObject struct {
	Name      string `json:"name" example:"get_current_weather"`
	Arguments any    `json:"arguments"`
}
