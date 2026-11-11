package eventlog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const HopEnvVar = "CONTENOX_EVENT_HOP"

type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

type DualPublisher struct {
	log          runtimetypes.EventStore
	next         Publisher
	source       string
	workspaceID  string
	subjectField string
	tracker      libtracker.ActivityTracker
	trigger      Trigger

	inheritedHop int
}

type DualPublisherOption func(*DualPublisher)

func WithSubjectField(field string) DualPublisherOption {
	return func(p *DualPublisher) { p.subjectField = field }
}

func WithPublisherTrigger(t Trigger) DualPublisherOption {
	return func(p *DualPublisher) {
		if t != nil {
			p.trigger = t
		}
	}
}

func NewDualPublisher(log runtimetypes.EventStore, next Publisher, source, workspaceID string, tracker libtracker.ActivityTracker, opts ...DualPublisherOption) *DualPublisher {
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	p := &DualPublisher{log: log, next: next, source: source, workspaceID: workspaceID, tracker: tracker, inheritedHop: hopFromEnv()}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func SpawnEnv(ctx context.Context) map[string]string {
	hop := runtimetypes.EventHopFromContext(ctx)
	if hop <= 0 {
		return nil
	}
	return map[string]string{HopEnvVar: strconv.Itoa(hop)}
}

func InheritHop(ctx context.Context) context.Context {
	if runtimetypes.EventHopFromContext(ctx) > 0 {
		return ctx
	}
	return runtimetypes.WithEventHop(ctx, hopFromEnv())
}

func hopFromEnv() int {
	hop, err := strconv.Atoi(strings.TrimSpace(os.Getenv(HopEnvVar)))
	if err != nil || hop <= 0 {
		return 0
	}
	return hop
}

func (p *DualPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	event := &runtimetypes.Event{
		WorkspaceID: p.workspaceID,
		Type:        subject,
		Source:      p.source,
		Subject:     p.extractSubject(data),
		Data:        json.RawMessage(data),
	}

	if runtimetypes.EventHopFromContext(ctx) == 0 {
		event.Hop = p.inheritedHop
	}
	if err := p.log.AppendEvent(ctx, event); err != nil {
		reportErr, _, end := p.tracker.Start(ctx, "append", "event_log", "type", subject)
		reportErr(fmt.Errorf("eventlog: dual-write append failed; live publish proceeds: %w", err))
		end()
	} else {

		defer fireTrigger(ctx, p.trigger, event)
	}
	if p.next == nil {
		return nil
	}
	return p.next.Publish(ctx, subject, data)
}

func (p *DualPublisher) extractSubject(data []byte) string {
	if p.subjectField == "" {
		return ""
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	var subject string
	if raw, ok := probe[p.subjectField]; ok {
		_ = json.Unmarshal(raw, &subject)
	}
	return subject
}

var _ Publisher = (*DualPublisher)(nil)
