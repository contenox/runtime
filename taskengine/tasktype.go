package taskengine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TaskHandler defines how task outputs are processed and interpreted.
type TaskHandler string

const (
	// HandleConditionKey interprets response as a condition key for transition branching.
	// Requires ValidConditions to be set with allowed values.
	HandleConditionKey TaskHandler = "condition_key"

	// HandleParseNumber expects a numeric response and parses it into an integer.
	// Returns error if response cannot be parsed as integer.
	HandleParseNumber TaskHandler = "parse_number"

	// HandleParseScore expects a floating-point score (e.g., quality rating).
	// Returns error if response cannot be parsed as float.
	HandleParseScore TaskHandler = "parse_score"

	// HandleParseRange expects a numeric range like "5-7" or single number "5" (converted to "5-5").
	// Returns error if response cannot be parsed as valid range.
	HandleParseRange TaskHandler = "parse_range"

	// HandleRawString returns the raw string result from the LLM without parsing.
	HandleRawString TaskHandler = "raw_string"

	// HandleEmbedding expects string input and returns an embedding vector ([]float64).
	// This is useful as last step in a text enrichment pipeline to enrich the data before embedding.
	HandleEmbedding TaskHandler = "embedding"

	// HandleRaiseError raises an error with the provided message from task input.
	// Useful for explicit error conditions in workflows.
	HandleRaiseError TaskHandler = "raise_error"

	// HandleModelExecution executes specified model on chat history input.
	// Requires DataTypeChatHistory input and ExecuteConfig configuration.
	HandleModelExecution TaskHandler = "model_execution"

	// HandleParseTransition attempts to parse transition commands (e.g., "/command").
	// Strips transition prefix if present in input.
	HandleParseTransition TaskHandler = "parse_transition"

	// HandleConvertToOpenAIChatResponse converts a chat history input to OpenAI Chat format.
	// Requires DataTypeChatHistory input and ExecuteConfig configuration.
	HandleConvertToOpenAIChatResponse TaskHandler = "convert_to_openai_chat_response"

	// HandleNoop performs no operation, passing input through unchanged.
	// Useful for data mutation, variable composition, and transition steps.
	HandleNoop TaskHandler = "noop"

	// HandleHook executes an external action via registered hook rather than calling LLM.
	// Requires Hook configuration with name and arguments.
	HandleHook TaskHandler = "hook"
)

func (t TaskHandler) String() string {
	return string(t)
}

func (d DataType) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d DataType) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(d.String())
}

func (dt *DataType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	switch strings.ToLower(s) {
	case "any":
		*dt = DataTypeAny
	case "string":
		*dt = DataTypeString
	case "bool":
		*dt = DataTypeBool
	case "int":
		*dt = DataTypeInt
	case "float":
		*dt = DataTypeFloat
	case "vector":
		*dt = DataTypeVector
	case "search_results":
		*dt = DataTypeSearchResults
	case "json":
		*dt = DataTypeJSON
	case "chat_history":
		*dt = DataTypeChatHistory
	default:
		return fmt.Errorf("unknown data type: %q", s)
	}

	return nil
}

func (dt *DataType) UnmarshalYAML(data []byte) error {
	var s string
	if err := yaml.Unmarshal(data, &s); err != nil {
		return err
	}

	switch strings.ToLower(s) {
	case "string":
		*dt = DataTypeString
	case "bool":
		*dt = DataTypeBool
	case "int":
		*dt = DataTypeInt
	case "float":
		*dt = DataTypeFloat
	case "vector":
		*dt = DataTypeVector
	case "search_results":
		*dt = DataTypeSearchResults
	case "json":
		*dt = DataTypeJSON
	case "chat_history":
		*dt = DataTypeChatHistory
	default:
		return fmt.Errorf("unknown data type: %q", s)
	}

	return nil
}

// TaskTransition defines what happens after a task completes,
// including which task to go to next and how to handle errors.
type TaskTransition struct {
	// OnFailure is the task ID to jump to in case of failure.
	OnFailure string `yaml:"on_failure" json:"on_failure" example:"error_handler"`

	// Branches defines conditional branches for successful task completion.
	Branches []TransitionBranch `yaml:"branches" json:"branches" openapi_include_type:"taskengine.TransitionBranch"`
}

// TransitionBranch defines a single possible path in the workflow,
// selected when the task's output matches the specified condition.
type TransitionBranch struct {
	// Operator defines how to compare the task's output to When.
	Operator OperatorTerm `yaml:"operator,omitempty" json:"operator,omitempty" example:"equals" openapi_include_type:"string"`

	// When specifies the condition that must be met to follow this branch.
	// Format depends on the task type:
	// - For condition_key: exact string match
	// - For parse_number: numeric comparison (using Operator)
	When string `yaml:"when" json:"when" example:"yes"`

	// Goto specifies the target task ID if this branch is taken.
	// Leave empty or use taskengine.TermEnd to end the chain.
	Goto string `yaml:"goto" json:"goto" example:"positive_response"`
}

// OperatorTerm represents logical operators used for task transition evaluation
type OperatorTerm string

const (
	OpEquals      OperatorTerm = "equals"
	OpContains    OperatorTerm = "contains"
	OpStartsWith  OperatorTerm = "starts_with"
	OpEndsWith    OperatorTerm = "ends_with"
	OpGreaterThan OperatorTerm = ">"
	OpGt          OperatorTerm = "gt"
	OpLessThan    OperatorTerm = "<"
	OpLt          OperatorTerm = "lt"
	OpInRange     OperatorTerm = "in_range"
	OpDefault     OperatorTerm = "default"
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
		string(OpGreaterThan),
		string(OpGt),
		string(OpLessThan),
		string(OpLt),
		string(OpInRange),
		string(OpDefault),
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
	case string(OpGreaterThan):
		return OpGreaterThan, nil
	case string(OpGt):
		return OpGt, nil
	case string(OpLessThan):
		return OpLessThan, nil
	case string(OpLt):
		return OpLt, nil
	case string(OpInRange):
		return OpInRange, nil
	case string(OpDefault):
		return OpDefault, nil
	default:
		return "", fmt.Errorf("unsupported operator: %s", s)
	}
}

// LLMExecutionConfig represents configuration for executing tasks using Large Language Models (LLMs).
type LLMExecutionConfig struct {
	Model       string   `yaml:"model" json:"model" example:"mistral:instruct"`
	Models      []string `yaml:"models,omitempty" json:"models,omitempty" example:"[\"gpt-4\", \"gpt-3.5-turbo\"]"`
	Provider    string   `yaml:"provider,omitempty" json:"provider,omitempty" example:"ollama"`
	Providers   []string `yaml:"providers,omitempty" json:"providers,omitempty" example:"[\"ollama\", \"openai\"]"`
	Temperature float32  `yaml:"temperature,omitempty" json:"temperature,omitempty" example:"0.7"`
}

// HookCall represents an external integration or side-effect triggered during a task.
// Hooks allow tasks to interact with external systems (e.g., "send_email", "update_db").
type HookCall struct {
	// Name is the registered hook name to invoke (e.g., "send_email").
	Name string `yaml:"name" json:"name" example:"slack_notification"`

	// Args are key-value pairs to parameterize the hook call.
	// Example: {"to": "user@example.com", "subject": "Notification"}
	Args map[string]string `yaml:"args" json:"args" example:"{\"channel\": \"#alerts\", \"message\": \"Task completed successfully\"}"`
}

// TaskDefinition represents a single step in a workflow.
// Each task has a handler that dictates how its prompt will be processed.
//
// Field validity by task type:
// | Field               | ConditionKey | ParseNumber | ParseScore | ParseRange | RawString | Hook  | Noop  |
// |---------------------|--------------|-------------|------------|------------|-----------|-------|-------|
// | ValidConditions     | Required     | -           | -          | -          | -         | -     | -     |
// | Hook                | -            | -           | -          | -          | -         | Req   | -     |
// | PromptTemplate      | Required     | Required    | Required   | Required   | Required  | -     | Opt   |
// | Print               | Optional     | Optional    | Optional   | Optional   | Optional  | Opt   | Opt   |
// | ExecuteConfig       | Optional     | Optional    | Optional   | Optional   | Optional  | -     | -     |
// | InputVar            | Optional     | Optional    | Optional   | Optional   | Optional  | Opt   | Opt   |
// | SystemInstruction   | Optional     | Optional    | Optional   | Optional   | Optional  | Opt   | Opt   |
// | Compose             | Optional     | Optional    | Optional   | Optional   | Optional  | Opt   | Opt   |
// | Transition          | Required     | Required    | Required   | Required   | Required  | Req   | Req   |
type TaskDefinition struct {
	// ID uniquely identifies the task within the chain.
	ID string `yaml:"id" json:"id" example:"validate_input"`

	// Description is a human-readable summary of what the task does.
	Description string `yaml:"description" json:"description" example:"Validates user input meets quality requirements"`

	// Handler determines how the LLM output (or hook) will be interpreted.
	Handler TaskHandler `yaml:"handler" json:"handler" example:"condition_key" openapi_include_type:"string"`

	// SystemInstruction provides additional instructions to the LLM, if applicable system level will be used.
	SystemInstruction string `yaml:"system_instruction,omitempty" json:"system_instruction,omitempty" example:"You are a quality control assistant. Respond only with 'valid' or 'invalid'."`

	// ValidConditions defines allowed values for ConditionKey tasks.
	// Required for ConditionKey tasks, ignored for all other types.
	// Example: {"yes": true, "no": true} for a yes/no condition.
	ValidConditions map[string]bool `yaml:"valid_conditions,omitempty" json:"valid_conditions,omitempty" example:"{\"valid\": true, \"invalid\": true}"`

	// ExecuteConfig defines the configuration for executing prompt or chat model tasks.
	ExecuteConfig *LLMExecutionConfig `yaml:"execute_config,omitempty" json:"execute_config,omitempty" openapi_include_type:"taskengine.LLMExecutionConfig"`

	// Hook defines an external action to run.
	// Required for Hook tasks, must be nil/omitted for all other types.
	// Example: {type: "send_email", args: {"to": "user@example.com"}}
	Hook *HookCall `yaml:"hook,omitempty" json:"hook,omitempty" openapi_include_type:"taskengine.HookCall"`

	// Print optionally formats the output for display/logging.
	// Supports template variables from previous task outputs.
	// Optional for all task types except Hook where it's rarely used.
	// Example: "The score is: {{.previous_output}}"
	Print string `yaml:"print,omitempty" json:"print,omitempty" example:"Validation result: {{.validate_input}}"`

	// PromptTemplate is the text prompt sent to the LLM.
	// It's Required and only applicable for the raw_string type.
	// Supports template variables from previous task outputs.
	// Example: "Rate the quality from 1-10: {{.input}}"
	PromptTemplate string `yaml:"prompt_template" json:"prompt_template" example:"Is this input valid? {{.input}}"`

	// InputVar is the name of the variable to use as input for the task.
	// Example: "input" for the original input.
	// Each task stores its output in a variable named with it's task id.
	InputVar string `yaml:"input_var,omitempty" json:"input_var,omitempty" example:"input"`

	// Compose merges the specified the output with the withVar side.
	// Optional. compose is applied before the input reaches the task execution,
	Compose *ComposeTask `yaml:"compose,omitempty" json:"compose,omitempty" openapi_include_type:"taskengine.ComposeTask"`

	// Transition defines what to do after this task completes.
	Transition TaskTransition `yaml:"transition" json:"transition" openapi_include_type:"taskengine.TaskTransition"`

	// Timeout optionally sets a timeout for task execution.
	// Format: "10s", "2m", "1h" etc.
	// Optional for all task types.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty" example:"30s"`

	// RetryOnFailure sets how many times to retry this task on failure.
	// Applies to all task types including Hooks.
	// Default: 0 (no retries)
	RetryOnFailure int `yaml:"retry_on_failure,omitempty" json:"retry_on_failure,omitempty" example:"2"`
}

// ComposeTask is a task that composes multiple variables into a single output.
// the composed output is stored in a variable named after the task ID with "_composed" suffix.
// and is also directly mutating the task's output.
// example:
//
// compose:
//
//	with_var: "chat2"
//	strategy: "override"
type ComposeTask struct {
	// Selects the variable to compose the current input with.
	WithVar string `yaml:"with_var,omitempty" json:"with_var,omitempty"`
	// Strategy defines how values should be merged ("override", "merge_chat_histories", "append_string_to_chat_history").
	// Optional; defaults to "override" or "merge_chat_histories" if both output and WithVar values are ChatHistory.
	// "merge_chat_histories": If both output and WithVar values are ChatHistory,
	// appends the WithVar's Messages to the output's Messages.
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}

type ChainTerms string

const (
	TermEnd = "end"
)

// TaskChainDefinition describes a sequence of tasks to execute in order,
// along with branching logic, retry policies, and model preferences.
//
// TaskChainDefinition support dynamic routing based on LLM outputs or conditions,
// and can include hooks to perform external actions (e.g., sending emails).
type TaskChainDefinition struct {
	// ID uniquely identifies the chain.
	ID string `yaml:"id" json:"id"`

	// Enables capturing user input and output.
	Debug bool `yaml:"debug" json:"debug"`

	// Description provides a human-readable summary of the chain's purpose.
	Description string `yaml:"description" json:"description"`

	// Tasks is the list of tasks to execute in sequence.
	Tasks []TaskDefinition `yaml:"tasks" json:"tasks" openapi_include_type:"taskengine.TaskDefinition"`

	// TokenLimit is the token limit for the context window (used during execution).
	TokenLimit int64 `yaml:"token_limit" json:"token_limit"`
}

type SearchResult struct {
	ID           string  `json:"id" example:"search_123456"`
	ResourceType string  `json:"type" example:"document"`
	Distance     float32 `json:"distance" example:"0.85"`
}

// ChatHistory represents a conversation history with an LLM.
type ChatHistory struct {
	// Messages is the list of messages in the conversation.
	Messages []Message `json:"messages"`
	// Model is the name of the model to use for the conversation.
	Model string `json:"model" example:"mistral:instruct"`
	// InputTokens will be filled by the engine and will hold the number of tokens used for the input.
	InputTokens int `json:"inputTokens" example:"15"`
	// OutputTokens will be filled by the engine and will hold the number of tokens used for the output.
	OutputTokens int `json:"outputTokens" example:"10"`
}

// Message represents a single message in a chat conversation.
type Message struct {
	// ID is the unique identifier for the message.
	// This field is not used by the engine. It can be filled as part of the Request, or left empty.
	// The ID is useful for tracking messages and computing differences of histories before storage.
	ID string `json:"id" example:"msg_123456"`
	// Role is the role of the message sender.
	Role string `json:"role" example:"user"`
	// Content is the content of the message.
	Content string `json:"content,omitempty" example:"What is the capital of France?"`
	// CallTools is the tool call of the message sender.
	CallTools []ToolCall `json:"callTools,omitempty"`
	// Timestamp is the time the message was sent.
	Timestamp time.Time `json:"timestamp" example:"2023-11-15T14:30:45Z"`
}

// OpenAIChatRequest represents a request compatible with OpenAI's chat API.
type OpenAIChatRequest struct {
	Model            string                     `json:"model" example:"mistral:instruct"`
	Messages         []OpenAIChatRequestMessage `json:"messages" openapi_include_type:"taskengine.OpenAIChatRequestMessage"`
	MaxTokens        int                        `json:"max_tokens,omitempty" example:"512"`
	Temperature      float64                    `json:"temperature,omitempty" example:"0.7"`
	TopP             float64                    `json:"top_p,omitempty" example:"1.0"`
	Stop             []string                   `json:"stop,omitempty" example:"[\"\\n\", \"###\"]"`
	N                int                        `json:"n,omitempty" example:"1"`
	Stream           bool                       `json:"stream,omitempty" example:"false"`
	PresencePenalty  float64                    `json:"presence_penalty,omitempty" example:"0.0"`
	FrequencyPenalty float64                    `json:"frequency_penalty,omitempty" example:"0.0"`
	User             string                     `json:"user,omitempty" example:"user_123"`
}

type OpenAIChatRequestMessage struct {
	Role    string `json:"role" example:"user"`
	Content string `json:"content" example:"Hello, how are you?"`
}

type OpenAIChatResponse struct {
	ID                string                     `json:"id" example:"chat_123"`
	Object            string                     `json:"object" example:"chat.completion"`
	Created           int64                      `json:"created" example:"1690000000"`
	Model             string                     `json:"model" example:"mistral:instruct"`
	Choices           []OpenAIChatResponseChoice `json:"choices" openapi_include_type:"taskengine.OpenAIChatResponseChoice"`
	Usage             OpenAITokenUsage           `json:"usage" openapi_include_type:"taskengine.OpenAITokenUsage"`
	SystemFingerprint string                     `json:"system_fingerprint,omitempty" example:"system_456"`
}

// OpenAIChatResponseChoice represents a single choice in an OpenAI chat response.
type OpenAIChatResponseChoice struct {
	Index        int                       `json:"index" example:"0"`
	Message      OpenAIChatResponseMessage `json:"message" openapi_include_type:"taskengine.OpenAIChatResponseMessage"`
	FinishReason string                    `json:"finish_reason" example:"stop"`
}

type OpenAITokenUsage struct {
	PromptTokens     int `json:"prompt_tokens" example:"100"`
	CompletionTokens int `json:"completion_tokens" example:"50"`
	TotalTokens      int `json:"total_tokens" example:"150"`
}

// ToolCall represents a tool call requested by the model.
type ToolCall struct {
	ID       string       `json:"id" example:"call_abc123"`
	Type     string       `json:"type" example:"function"`
	Function FunctionCall `json:"function" openapi_include_type:"taskengine.FunctionCall"`
}

// FunctionCall specifies the function name and arguments for a tool call.
type FunctionCall struct {
	Name      string `json:"name" example:"get_current_weather"`
	Arguments string `json:"arguments" example:"{\n  \"location\": \"San Francisco, CA\",\n  \"unit\": \"celsius\"\n}"`
}

type OpenAIChatResponseMessage struct {
	Role      string     `json:"role" example:"assistant"`
	Content   *string    `json:"content,omitempty" example:"I can help with that."` // Pointer to handle null content for tool calls
	ToolCalls []ToolCall `json:"tool_calls,omitempty" openapi_include_type:"taskengine.ToolCall"`
}
