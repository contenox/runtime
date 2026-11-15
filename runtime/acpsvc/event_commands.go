package acpsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/eventtools"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
)

var eventCommands = map[string]bool{"event": true, "events": true, "subscriptions": true, "subscribe": true, "schedules": true, "waiting": true}

func (t *Transport) handleEventOperation(ctx context.Context, sess *sessionEntry, name, args string) (string, error) {
	if t.deps.EventTools == nil {
		return "", fmt.Errorf("workspace events are unavailable")
	}
	ctx = context.WithValue(ctx, runtimetypes.SessionIDContextKey, sess.InternalSessionID)
	ctx = eventtools.WithOperator(ctx, sess.InternalSessionID)
	fields := strings.Fields(args)
	cursor := ""
	if len(fields) >= 2 && fields[len(fields)-2] == "--cursor" {
		cursor = fields[len(fields)-1]
		fields = fields[:len(fields)-2]
	}
	op := ""
	input := map[string]any{}
	switch name {
	case "events":
		op = "event_list"
		if len(fields) > 0 {
			if fields[0] == "recent" {
				op = "event_recent"
				if len(fields) > 1 {
					input["minutes"] = strings.TrimSuffix(fields[1], "m")
				}
			} else if strings.HasPrefix(fields[0], "#") {
				op = "event_get"
				input["nid"] = strings.TrimPrefix(fields[0], "#")
			} else {
				op = "event_describe"
				input["topic"] = fields[0]
			}
		}
	case "subscriptions":
		op = "subscription_list"
	case "waiting":
		op = "waiting_list"
	case "subscribe":
		if len(fields) < 4 || (fields[0] != "watch" && fields[0] != "topic") || (fields[2] != "inbox" && len(fields) < 5) {
			return "", fmt.Errorf("use /subscribe topic <pattern> <agent|this-session|new-session|inbox> <policy> <prompt>, or replace topic with watch")
		}
		op = "subscription_create"
		if fields[0] == "watch" {
			input["pathGlob"] = fields[1]
		} else {
			input["topicGlob"] = fields[1]
		}
		input["name"] = fields[1] + " → " + fields[2]
		input["hitlPolicyName"] = fields[3]
		input["prompt"] = strings.Join(fields[4:], " ")
		switch fields[2] {
		case "inbox":
			input["target"] = "notify"
			delete(input, "hitlPolicyName")
			input["prompt"] = strings.Join(fields[3:], " ")
		case "this-session", "new-session":
			input["target"] = "session"
			input["sessionMode"] = "continue"
			if fields[2] == "new-session" {
				input["sessionMode"] = "new"
			}
		default:
			input["agentName"] = fields[2]
		}
	case "schedules":
		op = "schedule_list"
		if len(fields) > 0 {
			switch fields[0] {
			case "delete":
				if len(fields) != 2 {
					return "", fmt.Errorf("use /schedules delete <id>")
				}
				op = "schedule_delete"
				input["id"] = fields[1]
			case "pause", "resume":
				if len(fields) != 2 {
					return "", fmt.Errorf("use /schedules pause|resume <id>")
				}
				op = "schedule_update"
				input["id"] = fields[1]
				input["enabled"] = strconv.FormatBool(fields[0] == "resume")
			case "new":
				if len(fields) < 4 {
					return "", fmt.Errorf("use /schedules new <topic> <at|every|cron> <time or expression>")
				}
				switch fields[2] {
				case "at", "every", "cron":
				default:
					return "", fmt.Errorf("choose at, every or cron")
				}
				op = "schedule_create"
				input["topic"] = fields[1]
				input[fields[2]] = strings.Join(fields[3:], " ")
			default:
				return "", fmt.Errorf("use /schedules, /schedules new, pause, resume or delete")
			}
		}
	}
	if cursor != "" {
		input["cursor"] = cursor
	}
	result, _, err := t.deps.EventTools.Exec(ctx, time.Now(), input, false, &taskengine.ToolsCall{ToolName: op})
	if err != nil {
		return "", err
	}
	if op == "subscription_create" {
		return fmt.Sprintf("Subscribed %s to %s.", fields[2], fields[1]), nil
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	return string(raw), err
}
