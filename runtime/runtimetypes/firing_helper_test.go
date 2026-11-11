package runtimetypes_test

import (
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func mustFiringStore(t *testing.T, exec libdb.Exec, workspaceID string, opts ...runtimetypes.EventFiringStoreOption) runtimetypes.EventFiringStore {
	t.Helper()
	s, err := runtimetypes.NewEventFiringStore(exec, workspaceID, opts...)
	if err != nil {
		t.Fatalf("firing store: %v", err)
	}
	return s
}
