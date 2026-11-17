package residency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/runtime/internal/modeld/residency"
)

type driveFakeExecutor struct {
	caps     residency.Capabilities
	evicted  []residency.Range
	evictErr error
}

func (f *driveFakeExecutor) Capabilities() residency.Capabilities                  { return f.caps }
func (f *driveFakeExecutor) AdmitRange(_ context.Context, _ residency.Range) error { return nil }
func (f *driveFakeExecutor) EvictRange(_ context.Context, r residency.Range) error {
	if f.evictErr != nil {
		return f.evictErr
	}
	f.evicted = append(f.evicted, r)
	return nil
}

func TestUnit_EvictColdRanges_TailFirstAndFiltersDegenerate(t *testing.T) {
	plan := residency.Plan{EvictCold: []residency.Block{
		{Range: residency.Range{Start: 4, End: 8}},
		{Range: residency.Range{Start: 16, End: 20}},
		{Range: residency.Range{Start: 10, End: 10}}, // degenerate, dropped
		{Range: residency.Range{Start: 0, End: 4}},
	}}
	got := residency.EvictColdRanges(plan)
	want := []residency.Range{{Start: 16, End: 20}, {Start: 4, End: 8}, {Start: 0, End: 4}}
	if len(got) != len(want) {
		t.Fatalf("got %d ranges, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range[%d] = %+v, want %+v (tail-first order)", i, got[i], want[i])
		}
	}
}

func TestUnit_Drive_EvictsAllColdRangesTailFirst(t *testing.T) {
	exec := &driveFakeExecutor{}
	plan := residency.Plan{EvictCold: []residency.Block{
		{Range: residency.Range{Start: 0, End: 4}},
		{Range: residency.Range{Start: 8, End: 12}},
	}}
	if err := residency.Drive(context.Background(), exec, plan); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	want := []residency.Range{{Start: 8, End: 12}, {Start: 0, End: 4}}
	if len(exec.evicted) != len(want) {
		t.Fatalf("evicted %+v, want %+v", exec.evicted, want)
	}
	for i := range want {
		if exec.evicted[i] != want[i] {
			t.Fatalf("evicted[%d] = %+v, want %+v", i, exec.evicted[i], want[i])
		}
	}
}

func TestUnit_Drive_NoColdIsNoop(t *testing.T) {
	exec := &driveFakeExecutor{}
	if err := residency.Drive(context.Background(), exec, residency.Plan{}); err != nil {
		t.Fatalf("Drive empty: %v", err)
	}
	if len(exec.evicted) != 0 {
		t.Fatalf("expected no evictions, got %+v", exec.evicted)
	}
}

func TestUnit_Drive_PropagatesEvictError(t *testing.T) {
	sentinel := errors.New("boom")
	exec := &driveFakeExecutor{evictErr: sentinel}
	err := residency.Drive(context.Background(), exec, residency.Plan{EvictCold: []residency.Block{{Range: residency.Range{Start: 0, End: 4}}}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Drive error = %v, want %v", err, sentinel)
	}
}
