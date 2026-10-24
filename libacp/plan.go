package libacp

import "encoding/json"

type PlanEntryPriority string

const (
	PlanPriorityHigh   PlanEntryPriority = "high"
	PlanPriorityMedium PlanEntryPriority = "medium"
	PlanPriorityLow    PlanEntryPriority = "low"
)

type PlanEntryStatus string

const (
	PlanStatusPending    PlanEntryStatus = "pending"
	PlanStatusInProgress PlanEntryStatus = "in_progress"
	PlanStatusCompleted  PlanEntryStatus = "completed"
)

type PlanEntry struct {
	Content  string            `json:"content"`
	Priority PlanEntryPriority `json:"priority"`
	Status   PlanEntryStatus   `json:"status"`
	Meta     json.RawMessage   `json:"_meta,omitempty"`
}

type AvailableCommandInput struct {
	Hint string `json:"hint,omitempty"`
}

type CommandCompletion struct {
	Values []string `json:"values,omitempty"`
	Hint   string   `json:"hint,omitempty"`
}

type CommandCompletionMeta struct {
	Completions map[string]CommandCompletion `json:"contenox.argumentCompletions,omitempty"`
}

type AvailableCommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Input       *AvailableCommandInput `json:"input,omitempty"`
	Meta        json.RawMessage        `json:"_meta,omitempty"`
}
