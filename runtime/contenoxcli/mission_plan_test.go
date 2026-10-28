package contenoxcli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/missionservice"
)

func TestUnit_RenderMissionPlan_RevisionZero(t *testing.T) {
	var out bytes.Buffer
	renderMissionPlan(&out, &missionservice.Mission{ID: "m-1"})
	text := out.String()
	if !strings.Contains(text, "no plan yet") {
		t.Fatalf("revision 0 must say so plainly, got %q", text)
	}
}

func TestUnit_RenderMissionPlan_PrintsEntriesAndHistory(t *testing.T) {
	mission := &missionservice.Mission{
		ID: "m-1",
		Plan: missionservice.Plan{
			Revision:    2,
			Explanation: "reordered after the review",
			Entries: []missionservice.PlanEntry{
				{Content: "write the parser", Status: missionservice.PlanEntryCompleted, Priority: missionservice.PlanEntryPriorityHigh},
				{Content: "ship it", Status: missionservice.PlanEntryInProgress, Priority: missionservice.PlanEntryPriorityMedium},
				{Content: "update docs", Status: missionservice.PlanEntryPending, Priority: missionservice.PlanEntryPriorityLow},
			},
		},
		PlanRevisions: []missionservice.PlanRevisionSummary{
			{Revision: 1, Added: 3, Removed: 0, Pending: 3},
			{Revision: 2, Added: 1, Removed: 2, Pending: 1, InProgress: 1, Completed: 1, Explanation: "reordered after the review"},
		},
	}

	var out bytes.Buffer
	renderMissionPlan(&out, mission)
	text := out.String()
	for _, want := range []string{
		"plan revision 2",
		"reordered after the review",
		"STATUS",
		"PRIORITY",
		"write the parser",
		"ship it",
		"update docs",
		"completed",
		"in_progress",
		"pending",
		"rev 1: +3/-0",
		"rev 2: +1/-2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}
