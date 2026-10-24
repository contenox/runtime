package contenoxcli

import (
	"context"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/sessionservice"
)

const localIdentity = "local-user"

func ensureDefaultSession(ctx context.Context, db libdb.DBManager, workspaceID string) (string, error) {
	return sessionservice.New(db, workspaceID, libtracker.NoopTracker{}).EnsureDefault(ctx, localIdentity)
}
