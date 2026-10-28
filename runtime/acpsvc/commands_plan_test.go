package acpsvc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_ParsePlanCommand(t *testing.T) {
	goal, ok := parsePlanCommand("/plan build the parser")
	require.True(t, ok)
	require.Equal(t, "build the parser", goal)

	goal, ok = parsePlanCommand("/plan")
	require.True(t, ok)
	require.Empty(t, goal)

	_, ok = parsePlanCommand("/planning is not a command")
	require.False(t, ok)

	_, ok = parsePlanCommand("plan build it")
	require.False(t, ok)
}

func TestUnit_PlanPreambleNamesTheStartTool(t *testing.T) {
	preamble := planPreamble("ship v1")
	require.Contains(t, preamble, "GOAL: ship v1")
	require.Contains(t, preamble, "mission_start")
	require.Contains(t, preamble, "IN ORDER")
}

func TestUnit_PlanCommandIsRegistered(t *testing.T) {
	found := false
	for _, command := range allACPCommands() {
		if command.Name == planCommandName {
			found = true
			require.Contains(t, command.Description, "subagent")
		}
	}
	require.True(t, found, "/plan must be advertised")
}

func TestUnit_PlanMissingFleetExplainsWhatIsNeeded(t *testing.T) {
	err := planPreambleForMissingFleet()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "/plan"))
	require.True(t, strings.Contains(err.Error(), "fleet"))
}
