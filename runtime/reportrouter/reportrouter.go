package reportrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/contenox/runtime/libacp"
	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/operatorinbox"
)

type SessionDeliverer interface {
	DeliverToSession(ctx context.Context, sessionID libacp.SessionID, n libacp.SessionNotification) error
}

type InboxWriter interface {
	Add(ctx context.Context, item *operatorinbox.Item) error
}

type Subscriber interface {
	Stream(ctx context.Context, subject string, ch chan<- []byte) (libbus.Subscription, error)
}

type Deps struct {
	Bus             Subscriber
	Sessions        SessionDeliverer
	Inbox           InboxWriter
	Tracker         libtracker.ActivityTracker
	AgentSupervisor AgentSupervisor
}

type AgentSupervisor interface {
	OfferToSupervisingAgent(ctx context.Context, ev missionservice.AttentionAskedEvent) error
}

type Router struct {
	deps Deps
}

func New(deps Deps) (*Router, error) {
	if deps.Bus == nil {
		return nil, fmt.Errorf("reportrouter: Bus is required")
	}
	if deps.Sessions == nil {
		return nil, fmt.Errorf("reportrouter: Sessions is required")
	}
	if deps.Inbox == nil {
		return nil, fmt.Errorf("reportrouter: Inbox is required")
	}
	if deps.Tracker == nil {
		deps.Tracker = libtracker.NoopTracker{}
	}
	return &Router{deps: deps}, nil
}

const streamBuffer = 64

func (r *Router) Start(ctx context.Context) (func(), error) {
	ch := make(chan []byte, streamBuffer)
	sub, err := r.deps.Bus.Stream(ctx, missionservice.ReportAddedSubject, ch)
	if err != nil {
		return nil, fmt.Errorf("reportrouter: subscribe %q: %w", missionservice.ReportAddedSubject, err)
	}
	askCh := make(chan []byte, streamBuffer)
	askSub, err := r.deps.Bus.Stream(ctx, missionservice.AttentionAskedSubject, askCh)
	if err != nil {
		_ = sub.Unsubscribe()
		return nil, fmt.Errorf("reportrouter: subscribe %q: %w", missionservice.AttentionAskedSubject, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.loop(runCtx, ch)
	}()
	go func() {
		defer wg.Done()
		r.askLoop(runCtx, askCh)
	}()
	return func() {
		cancel()
		_ = sub.Unsubscribe()
		_ = askSub.Unsubscribe()
		wg.Wait()
	}, nil
}

func (r *Router) askLoop(ctx context.Context, ch <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			r.handleAsk(ctx, data)
		}
	}
}

func (r *Router) handleAsk(ctx context.Context, data []byte) {
	var ev missionservice.AttentionAskedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		reportErr, _, end := r.deps.Tracker.Start(ctx, "fleet", "route_attention_ask")
		reportErr(fmt.Errorf("reportrouter: decode attention-asked event: %w", err))
		end()
		return
	}
	r.routeAsk(ctx, ev)
}

func (r *Router) routeAsk(ctx context.Context, ev missionservice.AttentionAskedEvent) {
	reportErr, reportChange, end := r.deps.Tracker.Start(ctx, "fleet", "route_attention_ask",
		"mission_id", ev.MissionID, "ask_id", ev.AskID)
	defer end()

	if ev.ParentSessionID == "" {
		reportChange("routed", "queue_operator_fired")
		return
	}
	reportChange("parent_session_id", ev.ParentSessionID)
	if err := r.deps.Sessions.DeliverToSession(ctx, libacp.SessionID(ev.ParentSessionID), buildAskNotification(ev)); err != nil {
		reportChange("routed", "queue_parent_not_live")
		reportErr(err)
		return
	}
	reportChange("routed", "session")

	if r.deps.AgentSupervisor != nil {
		if err := r.deps.AgentSupervisor.OfferToSupervisingAgent(ctx, ev); err != nil {
			reportErr(err)
		}
	}
}

type askUpdateMeta struct {
	Ask *askAttribution `json:"contenox.missionAsk,omitempty"`
}

type askAttribution struct {
	MissionID string `json:"missionId"`
	AskID     string `json:"askId"`
	AgentName string `json:"agentName,omitempty"`
	Intent    string `json:"intent,omitempty"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
}

func buildAskNotification(ev missionservice.AttentionAskedEvent) libacp.SessionNotification {
	update := libacp.NewAgentMessageChunk(askText(ev))
	update.MessageID = "mission-ask-" + ev.AskID
	meta := askUpdateMeta{Ask: &askAttribution{
		MissionID: ev.MissionID,
		AskID:     ev.AskID,
		AgentName: ev.AgentName,
		Intent:    ev.Intent,
		Summary:   ev.Summary,
		Detail:    ev.Detail,
	}}
	if raw, err := json.Marshal(meta); err == nil {
		update.Meta = raw
	}
	return libacp.SessionNotification{
		SessionID: libacp.SessionID(ev.ParentSessionID),
		Update:    update,
	}
}

func askText(ev missionservice.AttentionAskedEvent) string {
	unit := strings.TrimSpace(ev.AgentName)
	if unit == "" {
		unit = "a mission unit"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "unit %s is waiting on you: %s", unit, strings.TrimSpace(ev.Summary))
	if detail := strings.TrimSpace(ev.Detail); detail != "" {
		b.WriteString("\n\n")
		b.WriteString(detail)
	}
	return b.String()
}

func (r *Router) loop(ctx context.Context, ch <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			r.handle(ctx, data)
		}
	}
}

func (r *Router) handle(ctx context.Context, data []byte) {
	var ev missionservice.ReportAddedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		reportErr, _, end := r.deps.Tracker.Start(ctx, "fleet", "route_report")
		reportErr(fmt.Errorf("reportrouter: decode event: %w", err))
		end()
		return
	}
	r.route(ctx, ev)
}

func (r *Router) route(ctx context.Context, ev missionservice.ReportAddedEvent) {
	reportErr, reportChange, end := r.deps.Tracker.Start(ctx, "fleet", "route_report",
		"mission_id", ev.MissionID, "report_id", ev.Report.ID, "report_kind", string(ev.Report.Kind))
	defer end()

	if ev.ParentSessionID != "" {
		reportChange("parent_session_id", ev.ParentSessionID)
		n := buildReportNotification(ev)
		if err := r.deps.Sessions.DeliverToSession(ctx, libacp.SessionID(ev.ParentSessionID), n); err == nil {
			reportChange("routed", "session")
			return
		}
		reportChange("routed", "inbox_parent_gone")
		if err := r.toInbox(ctx, ev, operatorinbox.ReasonParentGone); err != nil {
			reportErr(err)
		}
		return
	}

	reportChange("routed", "inbox_operator_fired")
	if err := r.toInbox(ctx, ev, operatorinbox.ReasonOperatorFired); err != nil {
		reportErr(err)
	}
}

func (r *Router) toInbox(ctx context.Context, ev missionservice.ReportAddedEvent, reason operatorinbox.Reason) error {
	item := &operatorinbox.Item{
		MissionID:       ev.MissionID,
		AgentName:       ev.AgentName,
		Intent:          ev.Intent,
		ParentSessionID: ev.ParentSessionID,
		Reason:          reason,
		Report:          ev.Report,
	}
	if err := r.deps.Inbox.Add(ctx, item); err != nil {
		return fmt.Errorf("reportrouter: write inbox item for mission %q: %w", ev.MissionID, err)
	}
	return nil
}

type reportUpdateMeta struct {
	Report *reportAttribution `json:"contenox.missionReport,omitempty"`
}

type reportAttribution struct {
	MissionID string `json:"missionId"`
	ReportID  string `json:"reportId"`
	Kind      string `json:"kind"`
	AgentName string `json:"agentName,omitempty"`
}

func buildReportNotification(ev missionservice.ReportAddedEvent) libacp.SessionNotification {
	update := libacp.NewAgentMessageChunk(reportText(ev))
	update.MessageID = "mission-report-" + ev.Report.ID
	meta := reportUpdateMeta{Report: &reportAttribution{
		MissionID: ev.MissionID,
		ReportID:  ev.Report.ID,
		Kind:      string(ev.Report.Kind),
		AgentName: ev.AgentName,
	}}
	if raw, err := json.Marshal(meta); err == nil {
		update.Meta = raw
	}
	return libacp.SessionNotification{
		SessionID: libacp.SessionID(ev.ParentSessionID),
		Update:    update,
	}
}

func reportText(ev missionservice.ReportAddedEvent) string {
	unit := strings.TrimSpace(ev.AgentName)
	if unit == "" {
		unit = "a mission unit"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "unit %s reported (%s): %s", unit, ev.Report.Kind, ev.Report.Summary)
	if d := strings.TrimSpace(ev.Report.Detail); d != "" {
		b.WriteString("\n")
		b.WriteString(d)
	}
	if len(ev.Report.Refs) > 0 {
		b.WriteString("\nrefs: ")
		b.WriteString(strings.Join(ev.Report.Refs, ", "))
	}
	return b.String()
}
