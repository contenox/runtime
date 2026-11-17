package llama

import (
	"errors"
	"testing"
)

func TestUnit_ReleaseEmbeddings_InvokesHookAndPropagatesError(t *testing.T) {
	embedReleaseMu.RLock()
	previous := embedReleaseFunc
	embedReleaseMu.RUnlock()
	t.Cleanup(func() { SetEmbedReleaseFunc(previous) })
	want := errors.New("release failed")
	calls := 0
	SetEmbedReleaseFunc(func() error {
		calls++
		return want
	})
	if err := ReleaseEmbeddings(); !errors.Is(err, want) {
		t.Fatalf("ReleaseEmbeddings error = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("release calls = %d, want 1", calls)
	}
	SetEmbedReleaseFunc(nil)
	if err := ReleaseEmbeddings(); err != nil {
		t.Fatalf("ReleaseEmbeddings without backend: %v", err)
	}
}
