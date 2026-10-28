package acpsvc

import (
	"testing"

	"github.com/contenox/runtime/libacp"
	"github.com/stretchr/testify/require"
)

func TestUnit_MissionReportDeliveryRejectsMalformedContent(t *testing.T) {
	image := libacp.NewImageContent("data", "image/png")
	blank := libacp.NewTextContent("   ")
	text := libacp.NewTextContent("report")
	tr := &Transport{}

	for name, update := range map[string]libacp.SessionUpdate{
		"missing content":    {SessionUpdate: libacp.SessionUpdateAgentMessageChunk},
		"non-text content":   {SessionUpdate: libacp.SessionUpdateAgentMessageChunk, Content: &image},
		"blank text":         {SessionUpdate: libacp.SessionUpdateAgentMessageChunk, Content: &blank},
		"not an agent chunk": {SessionUpdate: libacp.SessionUpdateKind("tool_call"), Content: &text},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				err = tr.DeliverToContenoxSession(t.Context(), "parent", libacp.SessionNotification{SessionID: "s", Update: update})
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "mission report")
		})
	}

	require.NotPanics(t, func() {
		tr.persistDeliveredReport(t.Context(), "parent", libacp.SessionNotification{Update: libacp.SessionUpdate{SessionUpdate: libacp.SessionUpdateAgentMessageChunk}})
	})
	require.NoError(t, validateMissionReport(libacp.SessionNotification{Update: libacp.SessionUpdate{SessionUpdate: libacp.SessionUpdateAgentMessageChunk, Content: &text}}))
}
