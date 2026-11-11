package workspacetriggers

import (
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

var planEpoch = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func planTrigger(t *testing.T, mutate func(*Trigger)) compiledTrigger {
	t.Helper()
	tr := Trigger{
		ID:             "trg_tests",
		Name:           "tests changed",
		Events:         []string{"fs.modified", "fs.created"},
		PathGlob:       "**/*_test.go",
		DebounceMs:     1000,
		AgentName:      "reviewer",
		HITLPolicyName: "read_only",
		Prompt:         "Run the tests that changed.",
		Enabled:        true,
		CreatedAt:      planEpoch.Add(-time.Hour),
	}
	if mutate != nil {
		mutate(&tr)
	}
	compiled, err := compileTriggers([]Trigger{tr})
	require.NoError(t, err)
	require.Len(t, compiled, 1)
	return compiled[0]
}

func ev(nid int64, at time.Duration, typ, subject string) workspaceevents.Event {
	return workspaceevents.Event{NID: nid, Type: typ, Subject: subject, Time: planEpoch.Add(at)}
}

func planAt(now time.Duration, triggers []compiledTrigger, paused []window, events ...workspaceevents.Event) plan {
	return planPass(planInput{events: events, triggers: triggers, paused: paused, now: planEpoch.Add(now), grace: 5 * time.Second, maxRun: 30 * time.Minute, maxHop: DefaultMaxHop})
}

func TestUnit_Plan_ABurstFiresOnceAfterItGoesQuiet(t *testing.T) {
	tr := planTrigger(t, nil)
	events := []workspaceevents.Event{
		ev(10, 0, "fs.modified", "pkg/a_test.go"),
		ev(11, 200*time.Millisecond, "fs.modified", "pkg/a.go"),
		ev(12, 400*time.Millisecond, "fs.created", "pkg/b_test.go"),
		ev(13, 600*time.Millisecond, "fs.modified", "pkg/a_test.go"),
	}
	held := planAt(time.Second, []compiledTrigger{tr}, nil, events...)
	require.Empty(t, held.due, "the burst is still settling")
	require.Equal(t, int64(9), held.settledThrough, "the cursor stays before the open burst")

	quiet := planAt(1700*time.Millisecond, []compiledTrigger{tr}, nil, events...)
	require.Len(t, quiet.due, 1)
	b := quiet.due[0]
	require.Equal(t, int64(10), b.firstNID)
	require.Equal(t, int64(13), b.lastNID)
	require.Equal(t, []string{"pkg/a_test.go", "pkg/b_test.go"}, b.paths)
	require.Equal(t, "file changes", b.run("/w").Event)
	require.Equal(t, int64(13), quiet.settledThrough)
}

func TestUnit_Plan_AGapSplitsTwoBursts(t *testing.T) {
	tr := planTrigger(t, nil)
	p := planAt(10*time.Second, []compiledTrigger{tr}, nil,
		ev(1, 0, "fs.modified", "a_test.go"),
		ev(2, 3*time.Second, "fs.modified", "b_test.go"),
	)
	require.Len(t, p.due, 2)
	require.Equal(t, []string{"a_test.go"}, p.due[0].paths)
	require.Equal(t, []string{"b_test.go"}, p.due[1].paths)
}

func TestUnit_Plan_AStreamOfChangesStillFiresAtTheMaximumDelay(t *testing.T) {
	tr := planTrigger(t, nil)
	var events []workspaceevents.Event
	for i := 0; i < 12; i++ {
		events = append(events, ev(int64(i+1), time.Duration(i)*500*time.Millisecond, "fs.modified", "a_test.go"))
	}
	p := planAt(5600*time.Millisecond, []compiledTrigger{tr}, nil, events...)
	require.Len(t, p.due, 1, "a burst that never goes quiet fires after five debounce periods")
	require.Equal(t, int64(1), p.due[0].firstNID)
	require.Equal(t, int64(11), p.due[0].lastNID)
	require.Equal(t, int64(11), p.settledThrough, "the next burst has started and holds the cursor")
}

func TestUnit_Plan_NamesOneKindOfChangeAndCapsThePathList(t *testing.T) {
	tr := planTrigger(t, func(t *Trigger) { t.PathGlob = "" })
	var events []workspaceevents.Event
	for i := 0; i < maxPathsPerRun+3; i++ {
		events = append(events, ev(int64(i+1), 0, "fs.created", "f"+string(rune('a'+i))+".txt"))
	}
	p := planAt(time.Minute, []compiledTrigger{tr}, nil, events...)
	require.Len(t, p.due, 1)
	run := p.due[0].run("/w")
	require.Equal(t, "fs.created", run.Event)
	require.Len(t, run.Paths, maxPathsPerRun)
	require.Equal(t, 3, run.More)
}

func TestUnit_Plan_IgnoresWhatTheTriggerDoesNotListenTo(t *testing.T) {
	tr := planTrigger(t, nil)
	p := planAt(time.Minute, []compiledTrigger{tr}, nil,
		ev(1, 0, "fs.modified", "main.go"),
		ev(2, 0, "fs.deleted", "gone_test.go"),
		ev(3, 0, "editor.saved", "x_test.go"),
		ev(4, 0, "tool.wrote", "y_test.go"),
		ev(5, 0, "fs.modified", ""),
	)
	require.Empty(t, p.due)
	require.Equal(t, int64(5), p.settledThrough, "events no trigger wants never hold the cursor")

	disabled, err := compileTriggers([]Trigger{{ID: "trg_off", Enabled: false}})
	require.NoError(t, err)
	require.Empty(t, disabled)
}

func TestUnit_Plan_NeverFiresOnWhatATriggeredMissionDid(t *testing.T) {
	tr := planTrigger(t, nil)
	running := []window{{start: planEpoch}}
	p := planAt(time.Minute, []compiledTrigger{tr}, running, ev(1, time.Second, "fs.modified", "a_test.go"))
	require.Empty(t, p.due, "a running mission pauses its root")

	settled := []window{{start: planEpoch, end: planEpoch.Add(10 * time.Second)}}
	p = planAt(time.Minute, []compiledTrigger{tr}, settled,
		ev(1, 12*time.Second, "fs.modified", "late_test.go"),
		ev(2, 20*time.Second, "fs.modified", "after_test.go"),
	)
	require.Len(t, p.due, 1)
	require.Equal(t, []string{"after_test.go"}, p.due[0].paths, "the grace period covers writes the watcher reports late")

	p = planAt(2*time.Hour, []compiledTrigger{tr}, running, ev(1, time.Hour, "fs.modified", "a_test.go"))
	require.Len(t, p.due, 1, "a mission that never ends stops pausing after the maximum run")
}

func TestUnit_Plan_SkipsEventsFromBeforeTheTriggerExisted(t *testing.T) {
	tr := planTrigger(t, func(t *Trigger) { t.CreatedAt = planEpoch.Add(5 * time.Second) })
	p := planAt(time.Minute, []compiledTrigger{tr}, nil,
		ev(1, 0, "fs.modified", "old_test.go"),
		ev(2, 10*time.Second, "fs.modified", "new_test.go"),
	)
	require.Len(t, p.due, 1)
	require.Equal(t, []string{"new_test.go"}, p.due[0].paths)
}

func TestUnit_Plan_RefusesEventsPastTheHopLimit(t *testing.T) {
	tr := planTrigger(t, nil)
	near := ev(1, 0, "fs.modified", "near_test.go")
	near.Hop = DefaultMaxHop
	far := ev(2, 0, "fs.modified", "far_test.go")
	far.Hop = DefaultMaxHop + 1
	p := planAt(time.Minute, []compiledTrigger{tr}, nil, near, far)
	require.Len(t, p.due, 1)
	require.Equal(t, DefaultMaxHop, p.due[0].hop)
	require.Len(t, p.refused, 1)
	require.Equal(t, int64(2), p.refused[0].nid)
}

func TestUnit_Run_IntentQuotesPathsAndCapsTheirSize(t *testing.T) {
	run := Run{Trigger: Trigger{Name: "tests changed", Prompt: "Run them."}, Event: "fs.modified", Paths: []string{"a b.go", `quote"d.go`}}
	require.Equal(t, `Run them. (Started by the workspace trigger "tests changed" after fs.modified on: "a b.go", "quote\"d.go".)`, run.Intent())

	long := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		long = append(long, "very/long/path/segment/that/keeps/going/file_"+string(rune('a'+i%26))+".go")
	}
	capped := Run{Trigger: Trigger{Name: "n", Prompt: "p"}, Event: "fs.modified", Paths: long}.Intent()
	require.Less(t, len(capped), maxIntentPathBytes+200)
	require.Contains(t, capped, "more")
}

func TestUnit_Glob_MatchesProjectPaths(t *testing.T) {
	cases := []struct {
		glob string
		yes  []string
		no   []string
	}{
		{"**/*_test.go", []string{"a_test.go", "pkg/deep/b_test.go"}, []string{"a.go", "pkg/a_test.go.orig"}},
		{"*.go", []string{"main.go", "pkg/x.go"}, []string{"main.go.bak", "go"}},
		{"src/*.ts", []string{"src/a.ts"}, []string{"src/lib/a.ts", "a.ts"}},
		{"docs/**", []string{"docs/a.md", "docs/x/y.md"}, []string{"doc/a.md"}},
		{"{cmd,pkg}/**/*.go", []string{"cmd/a.go", "pkg/x/b.go"}, []string{"internal/a.go"}},
		{"", []string{"anything/at/all"}, nil},
	}
	for _, c := range cases {
		re, err := compileGlob(c.glob)
		require.NoError(t, err, c.glob)
		for _, p := range c.yes {
			require.True(t, re.MatchString(p), "%q should match %q", c.glob, p)
		}
		for _, p := range c.no {
			require.False(t, re.MatchString(p), "%q should not match %q", c.glob, p)
		}
	}
	_, err := compileGlob("/abs/*.go")
	require.ErrorIs(t, err, ErrInvalidTrigger)
	_, err = compileGlob("{a,b")
	require.ErrorIs(t, err, ErrInvalidTrigger)
}
