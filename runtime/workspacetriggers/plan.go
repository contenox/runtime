package workspacetriggers

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/contenox/runtime/runtime/workspaceevents"
)

const maxPathsPerRun = 20

const maxDelayFactor = 5

type compiledTrigger struct {
	Trigger
	glob     *regexp.Regexp
	events   map[string]bool
	debounce time.Duration
}

func compileTriggers(list []Trigger) ([]compiledTrigger, error) {
	out := make([]compiledTrigger, 0, len(list))
	var bad []error
	for _, t := range list {
		if !t.Enabled {
			continue
		}
		glob, err := compileGlob(t.PathGlob)
		if err != nil {
			bad = append(bad, fmt.Errorf("trigger %s: %w", t.Name, err))
			continue
		}
		events := map[string]bool{}
		for _, e := range t.Events {
			events[e] = true
		}
		out = append(out, compiledTrigger{Trigger: t, glob: glob, events: events, debounce: time.Duration(t.DebounceMs) * time.Millisecond})
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("workspacetriggers: stored triggers cannot be matched: %v", bad)
	}
	return out, nil
}

func (t compiledTrigger) maxDelay() time.Duration {
	return maxDelayFactor * t.debounce
}

type window struct {
	start time.Time
	end   time.Time
}

type burst struct {
	trigger  Trigger
	firstNID int64
	lastNID  int64
	first    time.Time
	last     time.Time
	hop      int
	event    string
	paths    []string
	seen     map[string]bool
	more     int
}

func newBurst(t compiledTrigger, e workspaceevents.Event) *burst {
	b := &burst{trigger: t.Trigger, firstNID: e.NID, first: e.Time, event: e.Type, seen: map[string]bool{}}
	b.add(e)
	return b
}

func (b *burst) add(e workspaceevents.Event) {
	b.lastNID = e.NID
	b.last = e.Time
	if e.Hop > b.hop {
		b.hop = e.Hop
	}
	if b.event != e.Type {
		b.event = "changes"
	}
	if b.seen[e.Subject] {
		return
	}
	b.seen[e.Subject] = true
	if len(b.paths) < maxPathsPerRun {
		b.paths = append(b.paths, e.Subject)
		return
	}
	b.more++
}

func (b *burst) run(root string) Run {
	event := b.event
	if event == "changes" {
		event = "file changes"
	}
	return Run{Trigger: b.trigger, Root: root, Event: event, Paths: b.paths, More: b.more}
}

type refusal struct {
	trigger Trigger
	nid     int64
	hop     int
}

type planInput struct {
	events   []workspaceevents.Event
	triggers []compiledTrigger
	paused   []window
	now      time.Time
	grace    time.Duration
	maxRun   time.Duration
	maxHop   int
}

type plan struct {
	due            []*burst
	refused        []refusal
	settledThrough int64
}

func pausedAt(windows []window, at time.Time, grace, maxRun time.Duration) bool {
	for _, w := range windows {
		if at.Before(w.start) {
			continue
		}
		if w.end.IsZero() {
			if at.Sub(w.start) < maxRun {
				return true
			}
			continue
		}
		if at.Before(w.end.Add(grace)) {
			return true
		}
	}
	return false
}

func planPass(in planInput) plan {
	var out plan
	open := map[string]*burst{}
	order := []string{}
	for _, e := range in.events {
		out.settledThrough = e.NID
		if e.Subject == "" || !allowedEvents[e.Type] || pausedAt(in.paused, e.Time, in.grace, in.maxRun) {
			continue
		}
		for _, t := range in.triggers {
			if !t.events[e.Type] || !t.glob.MatchString(e.Subject) || e.Time.Before(t.CreatedAt) {
				continue
			}
			if e.Hop > in.maxHop {
				out.refused = append(out.refused, refusal{trigger: t.Trigger, nid: e.NID, hop: e.Hop})
				continue
			}
			b := open[t.ID]
			if b != nil && (e.Time.Sub(b.last) > t.debounce || e.Time.Sub(b.first) > t.maxDelay()) {
				out.due = append(out.due, b)
				b = nil
			}
			if b == nil {
				if _, seen := open[t.ID]; !seen {
					order = append(order, t.ID)
				}
				open[t.ID] = newBurst(t, e)
				continue
			}
			b.add(e)
		}
	}
	byID := map[string]compiledTrigger{}
	for _, t := range in.triggers {
		byID[t.ID] = t
	}
	var held int64
	for _, id := range order {
		b := open[id]
		t := byID[id]
		if in.now.Sub(b.last) >= t.debounce || in.now.Sub(b.first) >= t.maxDelay() {
			out.due = append(out.due, b)
			continue
		}
		if held == 0 || b.firstNID < held {
			held = b.firstNID
		}
	}
	if held > 0 && held-1 < out.settledThrough {
		out.settledThrough = held - 1
	}
	sort.Slice(out.due, func(i, j int) bool { return out.due[i].lastNID < out.due[j].lastNID })
	return out
}
