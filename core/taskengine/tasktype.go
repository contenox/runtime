package taskengine

// TaskType defines the expected output format of a task.
// It determines how the LLM's response will be interpreted.
type TaskType string

const (
	// PromptToCondition interprets the response as a condition key,
	// used to determine which transition to follow.
	PromptToCondition TaskType = "condition"

	// PromptToNumber expects a numeric response and parses it into an integer.
	PromptToNumber TaskType = "number"

	// PromptToScore expects a floating-point score (e.g., quality rating).
	PromptToScore TaskType = "score"

	// PromptToRange expects a numeric range like "5-7", or defaults to N-N for single numbers.
	PromptToRange TaskType = "range"

	// PromptToString returns the raw string result from the LLM.
	PromptToString TaskType = "string"

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
	TriggerSemantic TriggerType = "semantic"

	// TriggerEvent starts the chain in response to an external event or webhook.
	TriggerEvent TriggerType = "event"
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

// Transition defines what happens after a task completes,
// including which task to go to next and how to handle errors.
type Transition struct {
	// OnError is the task ID to jump to in case of failure.
	OnError string `yaml:"on_error" json:"onError"`

	// Next defines conditional branches for successful task completion.
	Next []ConditionalTransition `yaml:"next" json:"next"`
}

// ConditionalTransition defines a conditional branch in a workflow,
// used to decide the next task based on output value.
type ConditionalTransition struct {
	// Operator is optional logic (e.g., "==", ">") for comparison.
	Operator string `yaml:"operator,omitempty" json:"operator,omitempty"`

	// Value is the expected value that triggers this transition.
	Value string `yaml:"value,omitempty" json:"value,omitempty"`

	// ID is the target task ID to transition to if the condition is met.
	ID string `yaml:"id" json:"id"`
}

// HookCall represents an external integration or side-effect triggered during a task.
// Hooks allow tasks to interact with external systems (e.g., send email, update DB).
type HookCall struct {
	// Type is the registered hook name to invoke (e.g., "send_email").
	Type string `yaml:"name" json:"name"`

	// Args are key-value pairs to parameterize the hook call.
	Args map[string]string `yaml:"args,omitempty" json:"args"`

	// Blocking determines whether the execution should wait for the hook to complete.
	Blocking bool `yaml:"blocking,omitempty" json:"blocking"`
}

// ChainTask represents a single step in a workflow.
// Each task has a type that dictates how its prompt will be processed.
type ChainTask struct {
	// ID uniquely identifies the task within the chain.
	ID string `yaml:"id" json:"id"`

	// Description is a human-readable summary of what the task does.
	Description string `yaml:"description" json:"description"`

	// Type determines how the LLM output (or hook) will be interpreted.
	Type TaskType `yaml:"type" json:"type"`

	// ConditionMapping defines valid values for condition tasks (PromptToCondition).
	ConditionMapping map[string]bool `yaml:"condition_mapping,omitempty" json:"conditionMapping,omitempty"`

	// Hook defines an external action to run (only for Hook tasks).
	Hook *HookCall `yaml:"hook,omitempty" json:"hook,omitempty"`

	// Print optionally formats the output for display/logging.
	Print string `yaml:"print,omitempty" json:"print,omitempty"`

	// PromptTemplate is the text prompt (with optional template variables) sent to the LLM.
	PromptTemplate string `yaml:"prompt_template" json:"prompt_template"`

	// Transition defines what to do after this task completes.
	Transition Transition `yaml:"transition" json:"transition"`

	// Timeout optionally sets a timeout for task execution (e.g., "10s", "2m").
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// PreferredModels optionally lists preferred LLM models to use for this task.
	PreferredModels []string `yaml:"preferred_models,omitempty" json:"preferredModels,omitempty"`

	// RetryOnError sets how many times to retry this task on failure.
	RetryOnError int `yaml:"retry_on_error,omitempty" json:"retryOnError,omitempty"`
}

// ChainWithTrigger is a convenience struct that combines triggers and chain definition.
type ChainWithTrigger struct {
	// Triggers defines when the chain should be started.
	Triggers []Trigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`

	// ChainDefinition contains the actual task sequence.
	ChainDefinition
}

// ChainDefinition describes a sequence of tasks to execute in order,
// along with branching logic, retry policies, and model preferences.
//
// Chains support dynamic routing based on LLM outputs or conditions,
// and can include hooks to perform external actions (e.g., sending emails).
type ChainDefinition struct {
	// ID uniquely identifies the chain.
	ID string `yaml:"id" json:"id"`

	// Description provides a human-readable summary of the chain's purpose.
	Description string `yaml:"description" json:"description"`

	// Triggers define how the chain is started (manual, event, etc.).
	Triggers []Trigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`

	// Tasks is the list of tasks to execute in sequence.
	Tasks []ChainTask `yaml:"tasks" json:"tasks"`

	// MaxTokenSize is the token limit for the context window (used during execution).
	MaxTokenSize int64 `yaml:"max_token_size" json:"maxTokenSize"`

	// RoutingStrategy defines how transitions should be evaluated (optional).
	RoutingStrategy string `yaml:"routing_strategy" json:"routingStrategy"`
}

type SearchResult struct {
	ID           string  `json:"id"`
	ResourceType string  `json:"type"`
	Distance     float32 `json:"distance"`
}
