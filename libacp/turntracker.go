package libacp

import (
	"fmt"
	"strings"
)

type TurnTracker struct {
	sawDisplayable bool
	toolUpdates    int
}

func (t *TurnTracker) Observe(n SessionNotification) {
	switch n.Update.SessionUpdate {
	case SessionUpdateAgentMessageChunk:
		if isDisplayableContent(n.Update.Content) {
			t.sawDisplayable = true
		}
	case SessionUpdateToolCall, SessionUpdateToolCallUpdate:
		t.toolUpdates++
	}
}

func (t *TurnTracker) SawDisplayableOutput() bool { return t.sawDisplayable }

func (t *TurnTracker) ToolUpdateCount() int { return t.toolUpdates }

func (t *TurnTracker) Err(stop StopReason) error {
	if t.sawDisplayable {
		return nil
	}
	reason := string(stop)
	if reason == "" {
		reason = "unknown"
	}
	if t.toolUpdates > 0 {
		return fmt.Errorf("%w (stopReason=%s, toolUpdates=%d)", ErrNoDisplayableOutput, reason, t.toolUpdates)
	}
	return fmt.Errorf("%w (stopReason=%s)", ErrNoDisplayableOutput, reason)
}

func (t *TurnTracker) Reset() { *t = TurnTracker{} }

func isDisplayableContent(c *ContentBlock) bool {
	if c == nil {
		return false
	}
	if c.Type == string(ContentKindText) {
		return strings.TrimSpace(c.Text) != ""
	}
	return true
}
