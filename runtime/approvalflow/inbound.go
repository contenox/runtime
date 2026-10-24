package approvalflow

import (
	"encoding/json"
	"strings"

	"github.com/contenox/runtime/libacp"
)

type Mapped struct {
	ToolsName string
	ToolName  string

	Args map[string]any

	Named bool

	ArgsKnown bool

	PolicyName string

	Diff string

	Title string

	ToolCallID string
}

func MapRequest(req libacp.RequestPermissionRequest) Mapped {
	m := Mapped{
		Args:       map[string]any{},
		Title:      strings.TrimSpace(req.ToolCall.Title),
		ToolCallID: req.ToolCall.ToolCallID,
	}
	if m.Title == "" {
		m.Title = req.ToolCall.ToolCallID
	}

	meta, ok := ParseMeta(req.Meta)
	if !ok {
		meta, _ = ParseMeta(req.ToolCall.Meta)
	}
	m.ToolsName = strings.TrimSpace(meta.ToolsName)
	m.ToolName = strings.TrimSpace(meta.ToolName)
	m.Named = m.ToolsName != "" && m.ToolName != ""
	m.PolicyName = strings.TrimSpace(meta.PolicyName)
	m.Diff = meta.Diff

	if args, ok := ParseArgs(req.ToolCall.RawInput); ok {
		m.Args = args
		m.ArgsKnown = true
	}
	return m
}

func ParseMeta(raw json.RawMessage) (Meta, bool) {
	if len(raw) == 0 {
		return Meta{}, false
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Meta{}, false
	}
	if meta == (Meta{}) {
		return Meta{}, false
	}
	return meta, true
}

func ParseArgs(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, false
	}
	if args == nil {
		return nil, false
	}
	return args, true
}

func SelectOption(req libacp.RequestPermissionRequest, allow bool) (string, bool) {
	wanted := [2]libacp.PermissionOptionKind{libacp.PermissionRejectOnce, libacp.PermissionRejectAlways}
	if allow {
		wanted = [2]libacp.PermissionOptionKind{libacp.PermissionAllowOnce, libacp.PermissionAllowAlways}
	}
	for _, kind := range wanted {
		for _, opt := range req.Options {
			if opt.Kind == kind && opt.OptionID != "" {
				return opt.OptionID, true
			}
		}
	}
	return "", false
}

func Answer(req libacp.RequestPermissionRequest, allow bool) libacp.RequestPermissionResponse {
	if id, ok := SelectOption(req, allow); ok {
		return libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: id},
		}
	}
	return libacp.RequestPermissionResponse{
		Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeCancelled},
	}
}
