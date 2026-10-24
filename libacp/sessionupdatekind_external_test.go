package libacp_test

import (
	"slices"
	"strings"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
)

var acpSessionUpdateKinds = []string{
	"agent_message_chunk",
	"agent_thought_chunk",
	"available_commands_update",
	"compaction_summary_chunk",
	"compaction_update",
	"config_option_update",
	"current_mode_update",
	"notice",
	"plan",
	"plan_removed",
	"plan_update",
	"session_info_update",
	"session_message",
	"session_message_chunk",
	"subagent_update",
	"tool_call",
	"tool_call_update",
	"usage_update",
	"user_message_chunk",
}

var undeclaredSessionUpdateKinds = map[string]string{
	"usage_stats": "the session-level usage record: the runtime's own session accounting, sent to a surface that asked this runtime for it. ACP declares usage_update, which carries a context gauge rather than the runtime's per-session record, so the two are not the same fact; a shell reading this kind is reading the runtime's own vocabulary.",
}

func TestUnit_SessionUpdateKinds_AreACPDeclaredKinds(t *testing.T) {
	var invented []string
	for _, kind := range libacp.AllSessionUpdateKinds() {
		if slices.Contains(acpSessionUpdateKinds, string(kind)) {
			continue
		}
		if _, named := undeclaredSessionUpdateKinds[string(kind)]; named {
			continue
		}
		invented = append(invented, string(kind))
	}
	if len(invented) > 0 {
		t.Fatalf("session update kinds the ACP schema does not declare, which a conforming client refuses before any handler runs: %v", invented)
	}
	for kind, reason := range undeclaredSessionUpdateKinds {
		if !slices.ContainsFunc(libacp.AllSessionUpdateKinds(), func(candidate libacp.SessionUpdateKind) bool {
			return string(candidate) == kind
		}) {
			t.Fatalf("session update kind %s is named as a deliberate exception and the library no longer declares it", kind)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("session update kind %s is named as a deliberate exception without a reason", kind)
		}
	}
}
