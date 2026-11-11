package contenoxcli

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

func workspaceLeaseDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "leases")
}

func newWorkspaceWatcher(events workspaceevents.Service, roots workspaceevents.Roots, leaseDir, workspaceID, nodeID string, tracker libtracker.ActivityTracker) *workspaceevents.Watcher {
	return workspaceevents.NewWatcher(events, roots, workspaceevents.WatcherConfig{
		WorkspaceID: workspaceID,
		Owner:       nodeID + ":" + strconv.Itoa(os.Getpid()),
		LeaseDir:    leaseDir,
		Tracker:     tracker,
		Skip:        localtools.WorkspaceSkip(),
		Transient:   localtools.IsAtomicTemp,
	})
}
