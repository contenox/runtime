package taskengine

type TaskType string

const (
	PromptToCondition TaskType = "condition"
	PromptToNumber    TaskType = "number"
	PromptToScore     TaskType = "score"
	PromptToString    TaskType = "string"
	Hook              TaskType = "hook"
)

type TriggerType string

const (
	TriggerManual   TriggerType = "manual"   // explicitly called by user/api
	TriggerKeyword  TriggerType = "keyword"  // keyword or string match
	TriggerSemantic TriggerType = "semantic" // embedding similarity match
	TriggerEvent    TriggerType = "event"    // system event, webhook, etc.
)

type Trigger struct {
	Type        TriggerType `yaml:"type" json:"type"`
	Description string      `yaml:"description" json:"description"`
	Pattern     string      `yaml:"pattern,omitempty" json:"pattern,omitempty"` // used for keyword or event name
}

type Transition struct {
	OnError string                  `yaml:"on_error" json:"onError"`
	Next    []ConditionalTransition `yaml:"next" json:"next"`
}

type ConditionalTransition struct {
	Match    string `yaml:"match" json:"match"`
	Operator string `yaml:"operator,omitempty" json:"operator,omitempty"`
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`
	ID       string `yaml:"id" json:"id"`
}

type HookCall struct {
	Name     string            `yaml:"name" json:"name"` // e.g., "send_email"
	Input    string            `yaml:"input,omitempty" json:"input,omitempty"`
	Args     map[string]string `yaml:"args,omitempty" json:"args"`         // user-defined arguments.
	Blocking bool              `yaml:"blocking,omitempty" json:"blocking"` // whether to wait for it to complete
}

type ChainTask struct {
	ID               string          `yaml:"id" json:"id"`
	Description      string          `yaml:"description" json:"description"`
	Type             TaskType        `yaml:"type" json:"type"`
	ConditionMapping map[string]bool `yaml:"condition_mapping,omitempty" json:"conditionMapping,omitempty"`
	Hook             *HookCall       `yaml:"hook,omitempty" json:"hook,omitempty"`
	PromptTemplate   string          `yaml:"prompt_template" json:"prompt_template"`
	Transition       Transition      `yaml:"transition" json:"transition"`
	Timeout          string          `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	PreferredModels  []string        `yaml:"preferred_models,omitempty" json:"preferredModels,omitempty"`
	RetryOnError     int             `yaml:"retry_on_error,omitempty" json:"retryOnError,omitempty"`
}

type ChainDefinition struct {
	ID           string      `yaml:"id" json:"id"`
	Description  string      `yaml:"description" json:"description"`
	Triggers     []Trigger   `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	Tasks        []ChainTask `yaml:"tasks" json:"tasks"`
	MaxTokenSize int64       `yaml:"max_token_size" json:"maxTokenSize"`
}
