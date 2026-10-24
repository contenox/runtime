package taskengine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var allHandlers = []TaskHandler{
	HandleRaiseError,
	HandleRoute,
	HandleChatCompletion,
	HandleExecuteToolCalls,
	HandleNoop,
	HandleTools,
}

func TestUnit_IsAssistantProseHandler_CoversEveryHandler(t *testing.T) {
	prose := map[TaskHandler]bool{
		HandleChatCompletion: true,
	}
	for _, h := range allHandlers {
		require.Equal(t, prose[h], IsAssistantProseHandler(h.String()),
			"handler %q: decide explicitly whether its streamed chunks are assistant prose", h)
	}

	require.False(t, IsAssistantProseHandler("a_handler_added_tomorrow"))
	require.False(t, IsAssistantProseHandler(""))
}

func TestUnit_IsToolBearingHandler_CoversEveryHandler(t *testing.T) {
	toolBearing := map[TaskHandler]bool{
		HandleChatCompletion:   true,
		HandleExecuteToolCalls: true,
		HandleTools:            true,
		HandleRoute:            true,
	}
	for _, h := range allHandlers {
		require.Equal(t, toolBearing[h], IsToolBearingHandler(h.String()),
			"handler %q: decide explicitly whether it renders its own tool calls", h)
	}

	require.False(t, IsToolBearingHandler("a_handler_added_tomorrow"))
	require.False(t, IsToolBearingHandler(""))
}
