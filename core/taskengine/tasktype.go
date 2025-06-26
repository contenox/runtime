package taskengine

import (
	"fmt"
)

// TaskType defines the expected output format of a task.
// It determines how the LLM's response will be interpreted.
type TaskType string

const (
	// ConditionKey interprets the response as a condition key,
	// used to determine which transition to follow.
	ConditionKey TaskType = "condition_key"

	// ParseNumber expects a numeric response and parses it into an integer.
	ParseNumber TaskType = "parse_number"

	// ParseScore expects a floating-point score (e.g., quality rating).
	ParseScore TaskType = "parse_score"

	// ParseRange expects a numeric range like "5-7", or defaults to N-N for single numbers.
	ParseRange TaskType = "parse_range"

	// RawString returns the raw string result from the LLM.
	RawString TaskType = "raw_string"

	// Hook indicates this task should execute an external action rather than calling the LLM.
	Hook TaskType = "hook"
)

// TriggerType defines the type of trigger that starts a chain.
type TriggerType string

const (
	// TriggerManual means the chain must be started manually via API or UI.
	TriggerManual TriggerType = "manual"

	// TriggerKeyword starts the chain if input matches a specific keyword.
	TriggerKeyword TriggerType = "keyword"

	// TriggerSemantic starts the chain based on semantic similarity (e.g., embeddings).
	TriggerSemantic TriggerType = "embedding"

	// TriggerEvent starts the chain in response to an external event or webhook.
	TriggerEvent TriggerType = "webhook"
)

// Trigger defines how and when a chain should be started.
type Trigger struct {
	// Type specifies the trigger mode (manual, keyword, etc.).
	Type TriggerType `yaml:"type" json:"type"`

	// Description is a human-readable explanation of the trigger.
	Description string `yaml:"description" json:"description"`

	// Pattern is used for matching input in keyword or event triggers.
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
}

// TaskTransition defines what happens after a task completes,
// including which task to go to next and how to handle errors.
type TaskTransition struct {
	// OnFailure is the task ID to jump to in case of failure.
	OnFailure string `yaml:"on_failure" json:"on_failure"`

	// Branches defines conditional branches for successful task completion.
	Branches []TransitionBranch `yaml:"branches" json:"branches"`
}

// TransitionBranch defines a single possible path in the workflow,
// selected when the task's output matches the specified condition.
type TransitionBranch struct {
	// Operator defines how to compare the task's output to When.
	Operator OperatorTerm `yaml:"operator,omitempty" json:"operator,omitempty"`

	// When specifies the condition that must be met to follow this branch.
	// Format depends on the task type:
	// - For condition_key: exact string match
	// - For parse_number: numeric comparison (using Operator)
	When string `yaml:"when" json:"when"`

	// Goto specifies the target task ID if this branch is taken.
	// Leave empty or use taskengine.TermEnd to end the chain.
	Goto string `yaml:"goto" json:"goto"`
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

// HookCall represents an external integration or side-effect triggered during a task.
// Hooks allow tasks to interact with external systems (e.g., "send_email", "update_db").
type HookCall struct {
	// Type is the registered hook name to invoke (e.g., "send_email").
	Type string `yaml:"type" json:"type"`

	// Args are key-value pairs to parameterize the hook call.
	// Example: {"to": "user@example.com", "subject": "Notification"}
	Args map[string]string `yaml:"args" json:"args"`
}

// ChainTask represents a single step in a workflow.
// Each task has a type that dictates how its prompt will be processed.
//
// Field validity by task type:
// | Field            | ConditionKey | ParseNumber | ParseScore | ParseRange | RawString | Hook |
// |------------------|--------------|-------------|------------|------------|-----------|------|
// | ValidConditions  | Required     | -           | -          | -          | -         | -    |
// | Hook             | -            | -           | -          | -          | -         | Req  |
// | Template         | Required     | Required    | Required   | Required   | Required  | -    |
// | Print            | Optional     | Optional    | Optional   | Optional   | Optional  | Opt  |
// | PreferredModels  | Optional     | Optional    | Optional   | Optional   | Optional  | -    |
type ChainTask struct {
	// ID uniquely identifies the task within the chain.
	ID string `yaml:"id" json:"id"`

	// Description is a human-readable summary of what the task does.
	Description string `yaml:"description" json:"description"`

	// Type determines how the LLM output (or hook) will be interpreted.
	Type TaskType `yaml:"type" json:"type"`

	// ValidConditions defines allowed values for ConditionKey tasks.
	// Required for ConditionKey tasks, ignored for all other types.
	// Example: {"yes": true, "no": true} for a yes/no condition.
	ValidConditions map[string]bool `yaml:"valid_conditions,omitempty" json:"valid_conditions,omitempty"`

	// Hook defines an external action to run.
	// Required for Hook tasks, must be nil/omitted for all other types.
	// Example: {type: "send_email", args: {"to": "user@example.com"}}
	Hook *HookCall `yaml:"hook,omitempty" json:"hook,omitempty"`

	// Print optionally formats the output for display/logging.
	// Supports template variables from previous task outputs.
	// Optional for all task types except Hook where it's rarely used.
	// Example: "The score is: {{.previous_output}}"
	Print string `yaml:"print,omitempty" json:"print,omitempty"`

	// Template is the text prompt sent to the LLM.
	// It's Required and only applicable for the raw_string type.
	// Supports template variables from previous task outputs.
	// Example: "Rate the quality from 1-10: {{.input}}"
	Template string `yaml:"prompt_template" json:"prompt_template"`

	// Transition defines what to do after this task completes.
	Transition TaskTransition `yaml:"transition" json:"transition"`

	// Timeout optionally sets a timeout for task execution.
	// Format: "10s", "2m", "1h" etc.
	// Optional for all task types.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// RetryOnFailure sets how many times to retry this task on failure.
	// Applies to all task types including Hooks.
	// Default: 0 (no retries)
	RetryOnFailure int `yaml:"retry_on_failure,omitempty" json:"retry_on_failure,omitempty"`
}

// ChainWithTrigger is a convenience struct that combines triggers and chain definition.
type ChainWithTrigger struct {
	// Triggers defines when the chain should be started.
	Triggers []Trigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`

	// ChainDefinition contains the actual task sequence.
	ChainDefinition
}

type ChainTerms string

const (
	TermEnd = "end"
)

// ChainDefinition describes a sequence of tasks to execute in order,
// along with branching logic, retry policies, and model preferences.
//
// ChainDefinition support dynamic routing based on LLM outputs or conditions,
// and can include hooks to perform external actions (e.g., sending emails).
//
// Example usage:
//
// Define a YAML chain:
//
//	chains/article.yaml:
//	  id: article-generator
//	  description: Generates articles based on topic and length
//	  triggers:
//	    - type: manual
//	      description: Run manually via API
//	  tasks:
//	    - id: get_length
//	      type: parse_number
//	      prompt_template: "How many words should the article be?"
//	      transition:
//	        branches:
//	          - when: "default"
//	            goto: generate_article
//
//	    - id: generate_article
//	      type: raw_string
//	      prompt_template: "Write a {{ .get_length }}-word article about {{ .input }}"
//	      print: "Generated article:\n{{ .previous_output }}"
//	      transition:
//	        branches:
//	          - when: "default"
//	            goto: end
//
//	    - id: end
//	      type: raw_string
//	      prompt_template: "{{ .generate_article }}"
//
// Parse and execute it:
//
//	file, err := os.Open("chains/article.yaml")
//	if err != nil {
//	    log.Fatalf("failed to open YAML: %v", err)
//	}
//	defer file.Close()
//
//	var chainDef taskengine.ChainDefinition
//	if err := yaml.NewDecoder(file).Decode(&chainDef); err != nil {
//	    log.Fatalf("failed to parse YAML: %v", err)
//	}
//
//	exec, _ := taskengine.NewExec(ctx, modelRepo, hookProvider)
//	env, _ := taskengine.NewEnv(ctx, tracker, exec)
//	output, err := env.ExecEnv(ctx, &chainDef, userInput, taskengine.DataTypeString)
//	if err != nil {
//	    log.Fatalf("execution failed: %v", err)
//	}
//
//	fmt.Println("Final output:", output)
type ChainDefinition struct {
	// ID uniquely identifies the chain.
	ID string `yaml:"id" json:"id"`

	// Description provides a human-readable summary of the chain's purpose.
	Description string `yaml:"description" json:"description"`

	// Tasks is the list of tasks to execute in sequence.
	Tasks []ChainTask `yaml:"tasks" json:"tasks"`

	// TokenLimit is the token limit for the context window (used during execution).
	TokenLimit int64 `yaml:"token_limit" json:"token_limit"`

	// RoutingStrategy defines how transitions should be evaluated (optional).
	RoutingStrategy string `yaml:"routing_strategy" json:"routing_strategy"`
}

type SearchResult struct {
	ID           string  `json:"id"`
	ResourceType string  `json:"type"`
	Distance     float32 `json:"distance"`
}

type ChatHistory struct {
	Messages     []Message `json:"messages"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIChatRequest struct {
	Model            string                     `json:"model"`
	Messages         []OpenAIChatRequestMessage `json:"messages"`
	MaxTokens        int                        `json:"max_tokens,omitempty"`
	Temperature      float64                    `json:"temperature,omitempty"`
	TopP             float64                    `json:"top_p,omitempty"`
	Stop             []string                   `json:"stop,omitempty"`
	N                int                        `json:"n,omitempty"`
	Stream           bool                       `json:"stream,omitempty"`
	PresencePenalty  float64                    `json:"presence_penalty,omitempty"`
	FrequencyPenalty float64                    `json:"frequency_penalty,omitempty"`
	User             string                     `json:"user,omitempty"`
}

type OpenAIChatRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIChatResponse struct {
	ID                string                     `json:"id"`
	Object            string                     `json:"object"`
	Created           int64                      `json:"created"`
	Model             string                     `json:"model"`
	Choices           []OpenAIChatResponseChoice `json:"choices"`
	Usage             OpenAITokenUsage           `json:"usage"`
	SystemFingerprint string                     `json:"system_fingerprint,omitempty"`
}

type OpenAIChatResponseChoice struct {
	Index        int                      `json:"index"`
	Message      OpenAIChatRequestMessage `json:"message"`
	FinishReason string                   `json:"finish_reason"`
}

type OpenAITokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
