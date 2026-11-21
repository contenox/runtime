package workspacetriggers

import (
	"errors"
	"fmt"
	"github.com/contenox/runtime/runtime/workspaceevents"
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
	"event.fired":  true,
	"fs.created":   true,
	"fs.modified":  true,
	"fs.deleted":   true,
	"fs.moved":     true,
	"editor.saved": true,
}

func AllowedEvents() []string {
	return []string{"fs.created", "fs.modified", "fs.deleted", "fs.moved", "editor.saved", "event.fired"}
}

type Trigger struct {
	Workflow         string    `json:"workflow,omitempty"`
	WorkflowRevision string    `json:"workflowRevision,omitempty"`
	CreatedBy        string    `json:"createdBy,omitempty"`
	OnDone           string    `json:"onDone,omitempty"`
	Target           string    `json:"target,omitempty"`
	SessionID        string    `json:"sessionId,omitempty"`
	SessionMode      string    `json:"sessionMode,omitempty"`
	ID               string    `json:"id"`
	Root             string    `json:"root"`
	Name             string    `json:"name"`
	Events           []string  `json:"events"`
	TopicGlob        string    `json:"topicGlob,omitempty"`
	PathGlob         string    `json:"pathGlob"`
	DebounceMs       int       `json:"debounceMs"`
	AgentName        string    `json:"agentName"`
	HITLPolicyName   string    `json:"hitlPolicyName"`
	Prompt           string    `json:"prompt"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
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
	t.TopicGlob = strings.TrimSpace(t.TopicGlob)
	switch {
	case t.OnDone != "" && (!workspaceevents.ValidTopic(t.OnDone) || (t.Target != "" && t.Target != "mission")):
		return fmt.Errorf("%w: onDone requires a mission and a valid completion topic", ErrInvalidTrigger)
	case t.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidTrigger)
	case len(t.Name) > MaxNameLength:
		return fmt.Errorf("%w: name exceeds %d characters", ErrInvalidTrigger, MaxNameLength)
	case t.Target != "" && t.Target != "mission" && t.Target != "session" && t.Target != "notify" && t.Target != "workflow":
		return fmt.Errorf("%w: choose mission, session, notify or workflow", ErrInvalidTrigger)
	case t.Target == "workflow" && (t.Workflow == "" || len(t.WorkflowRevision) != 64):
		return fmt.Errorf("%w: choose a saved workflow revision", ErrInvalidTrigger)
	case t.Target == "session" && (t.SessionID == "" || (t.SessionMode != "continue" && t.SessionMode != "new")):
		return fmt.Errorf("%w: choose a live session and its new or continue mode", ErrInvalidTrigger)
	case (t.Target == "" || t.Target == "mission") && t.AgentName == "":
		return fmt.Errorf("%w: agentName is required: a trigger starts a mission with a named agent", ErrInvalidTrigger)
	case t.Target != "workflow" && t.Target != "notify" && t.HITLPolicyName == "":
		return fmt.Errorf("%w: hitlPolicyName is required: a triggered mission must name its envelope", ErrInvalidTrigger)
	case t.Prompt == "" && t.Target != "workflow":
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
	if seen["event.fired"] {
		if len(events) != 1 || t.PathGlob != "" || t.TopicGlob == "" {
			return fmt.Errorf("%w: a topic subscription requires topicGlob and only event.fired", ErrInvalidTrigger)
		}
		if _, err := compileGlob(t.TopicGlob); err != nil {
			return err
		}
	} else if t.TopicGlob != "" {
		return fmt.Errorf("%w: topicGlob requires event.fired", ErrInvalidTrigger)
	}
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
	TopicEvent *workspaceevents.Event
	Trigger    Trigger
	Root       string
	Event      string
	Paths      []string
	More       int
}

const maxIntentPathBytes = 2000

func (r Run) Intent() string {
	if e := r.TopicEvent; e != nil {
		payload := strconv.Quote(string(e.Data))
		if len(payload) > maxIntentPathBytes {
			payload = fmt.Sprintf("see event %d in the workspace log", e.NID)
		}
		return fmt.Sprintf("%s Trigger %q received topic %q (event %d). Event data: %s", r.Trigger.Prompt, r.Trigger.Name, e.Subject, e.NID, payload)
	}
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
