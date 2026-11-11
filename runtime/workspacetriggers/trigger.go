package workspacetriggers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidTrigger = errors.New("invalid trigger")
	ErrNotFound       = errors.New("trigger not found")
	ErrNameTaken      = errors.New("a trigger with this name already exists")
)

const (
	MinDebounce     = 250 * time.Millisecond
	MaxDebounce     = 10 * time.Minute
	DefaultDebounce = 2 * time.Second
	MaxNameLength   = 64
	MaxPromptLength = 8000
)

var allowedEvents = map[string]bool{
	"fs.created":   true,
	"fs.modified":  true,
	"fs.deleted":   true,
	"fs.moved":     true,
	"editor.saved": true,
}

func AllowedEvents() []string {
	return []string{"fs.created", "fs.modified", "fs.deleted", "fs.moved", "editor.saved"}
}

type Trigger struct {
	ID             string    `json:"id"`
	Root           string    `json:"root"`
	Name           string    `json:"name"`
	Events         []string  `json:"events"`
	PathGlob       string    `json:"pathGlob"`
	DebounceMs     int       `json:"debounceMs"`
	AgentName      string    `json:"agentName"`
	HITLPolicyName string    `json:"hitlPolicyName"`
	Prompt         string    `json:"prompt"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Firing struct {
	TriggerID   string    `json:"triggerId"`
	TriggerName string    `json:"triggerName"`
	NID         int64     `json:"nid"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	MissionID   string    `json:"missionId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (t *Trigger) Normalize() error {
	t.Name = strings.TrimSpace(t.Name)
	t.AgentName = strings.TrimSpace(t.AgentName)
	t.HITLPolicyName = strings.TrimSpace(t.HITLPolicyName)
	t.Prompt = strings.TrimSpace(t.Prompt)
	t.PathGlob = strings.TrimSpace(t.PathGlob)
	switch {
	case t.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidTrigger)
	case len(t.Name) > MaxNameLength:
		return fmt.Errorf("%w: name exceeds %d characters", ErrInvalidTrigger, MaxNameLength)
	case t.AgentName == "":
		return fmt.Errorf("%w: agentName is required: a trigger starts a mission with a named agent", ErrInvalidTrigger)
	case t.HITLPolicyName == "":
		return fmt.Errorf("%w: hitlPolicyName is required: a triggered mission must name its envelope", ErrInvalidTrigger)
	case t.Prompt == "":
		return fmt.Errorf("%w: prompt is required: it is what the mission is asked to do", ErrInvalidTrigger)
	case strings.ContainsAny(t.Prompt, "\r\n"):
		return fmt.Errorf("%w: prompt must be a single line, like a mission intent", ErrInvalidTrigger)
	case strings.ContainsAny(t.Name, "\r\n"):
		return fmt.Errorf("%w: name must be a single line", ErrInvalidTrigger)
	case len(t.Prompt) > MaxPromptLength:
		return fmt.Errorf("%w: prompt exceeds %d characters", ErrInvalidTrigger, MaxPromptLength)
	case len(t.Events) == 0:
		return fmt.Errorf("%w: events is required, one or more of %s", ErrInvalidTrigger, strings.Join(AllowedEvents(), ", "))
	}
	seen := map[string]bool{}
	events := make([]string, 0, len(t.Events))
	for _, e := range t.Events {
		e = strings.TrimSpace(e)
		if !allowedEvents[e] {
			return fmt.Errorf("%w: %q cannot start a trigger; use %s", ErrInvalidTrigger, e, strings.Join(AllowedEvents(), ", "))
		}
		if !seen[e] {
			seen[e] = true
			events = append(events, e)
		}
	}
	t.Events = events
	if t.DebounceMs == 0 {
		t.DebounceMs = int(DefaultDebounce / time.Millisecond)
	}
	if d := time.Duration(t.DebounceMs) * time.Millisecond; d < MinDebounce || d > MaxDebounce {
		return fmt.Errorf("%w: debounceMs must be between %d and %d", ErrInvalidTrigger, MinDebounce.Milliseconds(), MaxDebounce.Milliseconds())
	}
	if _, err := compileGlob(t.PathGlob); err != nil {
		return err
	}
	return nil
}

type Run struct {
	Trigger Trigger
	Root    string
	Event   string
	Paths   []string
	More    int
}

const maxIntentPathBytes = 2000

func (r Run) Intent() string {
	quoted := make([]string, 0, len(r.Paths))
	more, used := r.More, 0
	for _, p := range r.Paths {
		q := strconv.Quote(p)
		if used+len(q) > maxIntentPathBytes {
			more++
			continue
		}
		used += len(q) + 2
		quoted = append(quoted, q)
	}
	var b strings.Builder
	b.WriteString(r.Trigger.Prompt)
	b.WriteString(fmt.Sprintf(" (Started by the workspace trigger %s after %s on: %s", strconv.Quote(r.Trigger.Name), r.Event, strings.Join(quoted, ", ")))
	if more > 0 {
		b.WriteString(fmt.Sprintf(", and %d more", more))
	}
	b.WriteString(".)")
	return b.String()
}
