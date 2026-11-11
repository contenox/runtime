package localfileapi

import (
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/workspacereplace"
	"github.com/stretchr/testify/require"
)

func planOf(size int) *workspacereplace.Plan {
	return &workspacereplace.Plan{Files: []workspacereplace.FilePlan{{Path: "f", Content: make([]byte, size)}}}
}

func TestUnit_ReplaceStore_EvictsTheOldestToStayUnderTheByteCap(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newReplaceStore()
	s.now = func() time.Time { return now }
	s.maxBytes = 100

	first, _ := s.put("/r", planOf(60))
	now = now.Add(time.Second)
	second, _ := s.put("/r", planOf(30))
	now = now.Add(time.Second)
	third, _ := s.put("/r", planOf(50))

	require.Nil(t, s.take(first), "the oldest preview made room")
	require.NotNil(t, s.take(second))
	require.NotNil(t, s.take(third))
	require.Zero(t, s.bytes, "taking every preview releases every byte")
}

func TestUnit_ReplaceStore_KeepsAtMostMaxPlans(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newReplaceStore()
	s.now = func() time.Time { return now }
	s.maxPlans = 2
	a, _ := s.put("/r", planOf(1))
	now = now.Add(time.Second)
	b, _ := s.put("/r", planOf(1))
	now = now.Add(time.Second)
	c, _ := s.put("/r", planOf(1))
	require.Nil(t, s.take(a))
	require.NotNil(t, s.take(b))
	require.NotNil(t, s.take(c))
}

func TestUnit_ReplaceStore_AnExpiredPreviewIsNotKeptForARetry(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newReplaceStore()
	s.now = func() time.Time { return now }
	id, _ := s.put("/r", planOf(10))
	stored := s.take(id)
	require.NotNil(t, stored)
	now = now.Add(replacePreviewTTL + time.Second)
	s.keep(id, stored)
	require.Zero(t, s.bytes)
	require.Empty(t, s.plans)
}
