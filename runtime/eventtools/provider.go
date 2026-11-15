package eventtools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/contenox/runtime/runtime/workspacetriggers"
	"github.com/getkin/kin-openapi/openapi3"
)

type operatorContextKey struct{}

func WithOperator(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, operatorContextKey{}, "human:"+session)
}

type Provider struct {
	History     workspacetriggers.History
	Events      workspaceevents.Service
	Check       func(context.Context, workspacetriggers.Trigger) error
	Store       workspacetriggers.Store
	WorkspaceID string
	Cwd         func(context.Context) string
}

func (*Provider) Supports(context.Context) ([]string, error) { return []string{"events"}, nil }
func (*Provider) GetSchemasForSupportedTools(context.Context) (map[string]*openapi3.T, error) {
	return map[string]*openapi3.T{}, nil
}

func tool(name, description string, props map[string]any, required ...string) taskengine.Tool {
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return taskengine.Tool{Type: "function", Function: taskengine.FunctionTool{Name: name, Description: description, Parameters: schema}}
}
func (*Provider) GetToolsForToolsByName(_ context.Context, name string) ([]taskengine.Tool, error) {
	if name != "events" {
		return nil, fmt.Errorf("unknown tools %q", name)
	}
	field := map[string]string{"type": "string"}
	triggerProps := map[string]any{
		"name": field, "agentName": field, "hitlPolicyName": field,
		"prompt": map[string]string{"type": "string", "description": "Instructions the agent must execute when it wakes. Include the requested actions, filenames and any completion topic. For example: Read REVIEW.md and use event_fire to publish review.received with the verdict."},
		"target": field, "sessionMode": field, "onDone": field,
		"pathGlob":   map[string]string{"type": "string", "description": "Project-relative filename or glob. For a change to REVIEW.md use REVIEW.md here."},
		"debounceMs": map[string]any{"type": "integer", "minimum": 250, "maximum": 600000},
	}
	topicProps := maps.Clone(triggerProps)
	topicProps["topicGlob"] = map[string]string{"type": "string", "description": "Name or pattern of an explicitly published topic, for example review.requested. For filesystem changes use pathGlob."}
	return []taskengine.Tool{
		tool("schedule_create", "Publish a topic once at an RFC3339 time, at a repeating interval, or with a five-field cron expression. A matching subscription wakes the agent.", map[string]any{"topic": field, "at": field, "every": field, "cron": field, "timezone": field, "payload": map[string]string{"type": "object"}}, "topic"),
		tool("schedule_list", "List schedules with their topics, next fire times and enabled state.", map[string]any{"cursor": field}),
		tool("schedule_update", "Pause or resume a schedule.", map[string]any{"id": field, "enabled": map[string]string{"type": "boolean"}}, "id", "enabled"),
		tool("schedule_delete", "Delete a workspace schedule.", map[string]any{"id": field}, "id"),
		tool("watch_create", "Subscribe to a file appearing or changing. For example, wake this conversation when REVIEW.md is written with pathGlob REVIEW.md, target session, sessionMode continue and hitlPolicyName default.", triggerProps, "name", "pathGlob", "prompt"),
		tool("subscription_create", "Wake an agent when a named topic is published. Choose exactly one topicGlob or pathGlob. Use watch_create for file changes. Choose target mission (agentName), session (this conversation, sessionMode continue or new), or notify (inbox). A mission or session requires hitlPolicyName. onDone optionally publishes a topic when a mission lands.", topicProps, "name", "prompt"),
		tool("subscription_delete", "Remove a subscription in this project by its ID.", map[string]any{"id": field}, "id"),
		tool("event_fire", "Publish a named event to this project. Matching subscriptions may start work.", map[string]any{"topic": map[string]string{"type": "string"}, "payload": map[string]string{"type": "object"}}, "topic"),
		tool("event_get", "Read a workspace event and its full payload by event number.", map[string]any{"nid": map[string]any{"type": "integer", "minimum": 1}}, "nid"),
		tool("event_recent", "List recent workspace events and their payloads within the last N minutes, newest first. Use beforeNid to continue a full page.", map[string]any{"minutes": map[string]any{"type": "integer", "minimum": 1}, "beforeNid": map[string]any{"type": "integer", "minimum": 0}}),
		tool("event_list", "List published topics and their subscriber counts, including subscribed patterns that have never fired. Set subscribedOnly to see topics with enabled subscribers.", map[string]any{"cursor": field, "subscribedOnly": map[string]string{"type": "boolean"}}),
		tool("event_describe", "Show recent occurrences and matching subscriptions for one topic.", map[string]any{"topic": field, "cursor": field}, "topic"),
		tool("waiting_list", "List active firings that have not yet settled.", map[string]any{"cursor": field}),
		tool("subscription_list", "List subscriptions in this project, including the topic or file pattern each listens for and whether it is enabled.", map[string]any{"cursor": field}),
	}, nil
}
func (p *Provider) Exec(ctx context.Context, _ time.Time, input any, _ bool, call *taskengine.ToolsCall) (any, taskengine.DataType, error) {
	if call == nil {
		return nil, taskengine.DataTypeAny, fmt.Errorf("missing event tool call")
	}
	cwd := p.Cwd(ctx)
	if cwd == "" {
		return nil, taskengine.DataTypeAny, fmt.Errorf("open a project to use events")
	}
	root, err := p.Events.GrantedRoot(cwd)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	args, _ := input.(map[string]any)
	arg := func(name string) string {
		if value, ok := args[name]; ok {
			return fmt.Sprint(value)
		}
		return call.Args[name]
	}
	creator := "agent"
	if id, ok := ctx.Value(runtimetypes.SessionIDContextKey).(string); ok && id != "" {
		creator += ":" + id
	}
	if origin, ok := ctx.Value(operatorContextKey{}).(string); ok {
		creator = origin
	}
	switch call.ToolName {
	case "event_fire":
		ctx = eventlog.InheritHop(ctx)
		raw := json.RawMessage(call.Args["payload"])
		if payload, ok := args["payload"]; ok {
			raw, err = json.Marshal(payload)
			if err != nil {
				return nil, taskengine.DataTypeAny, err
			}
		}
		event, err := p.Events.Append(ctx, root, workspaceevents.Input{Type: workspaceevents.TypeEventFired, Source: workspaceevents.SourceAgent, Subject: arg("topic"), Data: raw})
		return event, taskengine.DataTypeAny, err
	case "event_get":
		nid, err := strconv.ParseInt(arg("nid"), 10, 64)
		if err != nil || nid < 1 {
			return nil, taskengine.DataTypeAny, fmt.Errorf("expected a positive event number")
		}
		rows, err := p.Events.Since(ctx, root, nid-1, 1)
		if err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		if len(rows) == 0 || rows[0].NID != nid {
			return nil, taskengine.DataTypeAny, fmt.Errorf("event %d not found in this project", nid)
		}
		return rows[0], taskengine.DataTypeAny, nil
	case "event_recent":
		minutes := 3
		if raw := strings.TrimSpace(arg("minutes")); raw != "" {
			minutes, err = strconv.Atoi(raw)
			if err != nil || minutes < 1 || minutes > 43200 {
				return nil, taskengine.DataTypeAny, fmt.Errorf("minutes must be between 1 and 43200")
			}
		}
		var before int64
		if raw := arg("beforeNid"); raw != "" {
			before, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || before < 0 {
				return nil, taskengine.DataTypeAny, fmt.Errorf("beforeNid must be a non-negative event number")
			}
		}
		events, err := p.Events.Recent(ctx, root, before, 100, runtimetypes.EventQuery{After: time.Now().Add(-time.Duration(minutes) * time.Minute)})
		return events, taskengine.DataTypeAny, err
	case "schedule_list":
		rows, next, err := (workspaceevents.Schedules{DB: p.Store.DB}).Page(ctx, workspaceevents.Scope(p.WorkspaceID, root), arg("cursor"), 100)
		return map[string]any{"schedules": rows, "nextCursor": next}, taskengine.DataTypeAny, err
	case "schedule_update":
		enabled, err := strconv.ParseBool(arg("enabled"))
		if err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		err = (workspaceevents.Schedules{DB: p.Store.DB}).Change(ctx, workspaceevents.Scope(p.WorkspaceID, root), arg("id"), &enabled)
		return arg("id"), taskengine.DataTypeAny, err
	case "schedule_delete":
		err := (workspaceevents.Schedules{DB: p.Store.DB}).Change(ctx, workspaceevents.Scope(p.WorkspaceID, root), arg("id"), nil)
		return arg("id"), taskengine.DataTypeAny, err
	case "schedule_create":
		schedule := workspaceevents.Schedule{CreatedBy: creator, Topic: arg("topic"), Every: arg("every"), Cron: arg("cron"), Timezone: arg("timezone"), Payload: json.RawMessage(call.Args["payload"])}
		if v, ok := args["payload"]; ok {
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, taskengine.DataTypeAny, err
			}
			schedule.Payload = raw
		}
		if at := arg("at"); at != "" {
			instant, err := time.Parse(time.RFC3339, at)
			if err != nil {
				return nil, taskengine.DataTypeAny, err
			}
			schedule.At = &instant
		}
		row, err := (workspaceevents.Schedules{DB: p.Store.DB}).Create(ctx, workspaceevents.Scope(p.WorkspaceID, root), schedule, time.Now())
		return row, taskengine.DataTypeAny, err
	case "watch_create", "subscription_create":
		if call.ToolName == "watch_create" && (arg("topicGlob") != "" || arg("pathGlob") == "") {
			return nil, taskengine.DataTypeAny, fmt.Errorf("watch_create requires a pathGlob")
		}
		tr := workspacetriggers.Trigger{CreatedBy: creator, Name: arg("name"), Root: root, AgentName: arg("agentName"), HITLPolicyName: arg("hitlPolicyName"), TopicGlob: arg("topicGlob"), PathGlob: arg("pathGlob"), Prompt: arg("prompt"), Enabled: true}
		if raw := arg("debounceMs"); raw != "" {
			ms, e := strconv.Atoi(raw)
			if e != nil {
				return nil, taskengine.DataTypeAny, e
			}
			tr.DebounceMs = ms
		}
		tr.OnDone = arg("onDone")
		tr.Target = arg("target")
		if tr.Target == "session" {
			tr.SessionID, _ = ctx.Value(runtimetypes.SessionIDContextKey).(string)
			tr.SessionMode = arg("sessionMode")
			if tr.SessionMode == "" {
				tr.SessionMode = "continue"
			}
		}
		if tr.TopicGlob != "" {
			tr.Events = []string{workspaceevents.TypeEventFired}
		} else if tr.PathGlob != "" {
			tr.Events = []string{workspaceevents.TypeFSCreated, workspaceevents.TypeFSModified, workspaceevents.TypeFSMoved}
		} else {
			return nil, taskengine.DataTypeAny, fmt.Errorf("choose a topicGlob or pathGlob")
		}
		if err := tr.Normalize(); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		if p.Check == nil {
			return nil, taskengine.DataTypeAny, fmt.Errorf("subscription target validation unavailable")
		}
		if err := p.Check(ctx, tr); err != nil {
			return nil, taskengine.DataTypeAny, err
		}
		err := p.Store.Create(ctx, workspaceevents.Scope(p.WorkspaceID, root), &tr)
		return tr, taskengine.DataTypeAny, err
	case "subscription_delete":
		err := p.Store.Delete(ctx, workspaceevents.Scope(p.WorkspaceID, root), arg("id"))
		return arg("id"), taskengine.DataTypeAny, err
	case "waiting_list":
		rows, err := p.History.WaitingPage(ctx, workspaceevents.Scope(p.WorkspaceID, root), arg("cursor"), 100)
		return rows, taskengine.DataTypeAny, err
	case "event_list", "event_describe":
		value, err := p.topics(ctx, root, call.ToolName, arg("topic"), arg("subscribedOnly") == "true", arg("cursor"))
		return value, taskengine.DataTypeAny, err
	case "subscription_list":
		triggers, next, err := p.Store.Page(ctx, workspaceevents.Scope(p.WorkspaceID, root), arg("cursor"), 100)
		return map[string]any{"subscriptions": triggers, "nextCursor": next}, taskengine.DataTypeAny, err
	default:
		return nil, taskengine.DataTypeAny, fmt.Errorf("unknown event tool %q", call.ToolName)
	}
}

type topicSummary struct {
	Topic       string    `json:"topic"`
	Count       int       `json:"count"`
	Last        time.Time `json:"last,omitempty"`
	Subscribers int       `json:"subscribers"`
}

func (p *Provider) topics(ctx context.Context, root, name, topic string, subscribedOnly bool, cursor string) (any, error) {
	if name == "event_describe" {
		recent, err := p.Events.Recent(ctx, root, 0, 10, runtimetypes.EventQuery{Type: workspaceevents.TypeEventFired, Subject: topic})
		if err != nil {
			return nil, err
		}
		matches, next, err := p.Store.MatchPage(ctx, workspaceevents.Scope(p.WorkspaceID, root), workspaceevents.TypeEventFired, topic, cursor, 100)
		return map[string]any{"topic": topic, "recent": recent, "subscriptions": matches, "nextCursor": next}, err
	}
	scope := workspaceevents.Scope(p.WorkspaceID, root)
	stats, err := runtimetypes.EventSubjects(ctx, p.Store.DB, scope, cursor, 101)
	if err != nil {
		return nil, err
	}
	out := make([]topicSummary, 0, 101)
	for _, stat := range stats {
		out = append(out, topicSummary{Topic: stat.Topic, Count: stat.Count, Last: stat.Last})
	}
	err = p.Store.EachPage(ctx, scope, func(rules []workspacetriggers.Trigger) error {
		for _, rule := range rules {
			if !rule.Enabled || rule.TopicGlob <= cursor {
				continue
			}
			i := sort.Search(len(out), func(i int) bool { return out[i].Topic >= rule.TopicGlob })
			if i == 101 || (i < len(out) && out[i].Topic == rule.TopicGlob) {
				continue
			}
			if len(out) < 101 {
				out = append(out, topicSummary{})
			}
			copy(out[i+1:], out[i:])
			out[i] = topicSummary{Topic: rule.TopicGlob}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	next := ""
	if len(out) > 100 {
		next = out[99].Topic
		out = out[:100]
	}
	subscribers := map[string]int{}
	for _, item := range out {
		subscribers[item.Topic] = 0
	}
	err = p.Store.EachPage(ctx, scope, func(rules []workspacetriggers.Trigger) error {
		return workspacetriggers.CountTopicSubscribers(rules, subscribers)
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Subscribers = subscribers[out[i].Topic]
	}
	if subscribedOnly {
		filtered := out[:0]
		for _, item := range out {
			if item.Subscribers > 0 {
				filtered = append(filtered, item)
			}
		}
		out = filtered
	}
	return map[string]any{"topics": out, "nextCursor": next}, nil
}
