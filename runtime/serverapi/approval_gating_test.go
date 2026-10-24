package serverapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func approvalGatingMux(t *testing.T, withHITL bool) *http.ServeMux {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "serverapi.db"), runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	state, err := runtimestate.New(ctx, db, libbus.NewInMem())
	if err != nil {
		t.Fatalf("state: %v", err)
	}

	deps := Dependencies{DB: db, State: state}
	if withHITL {
		store := runtimetypes.New(db.WithoutTransaction())
		deps.HITL = hitlservice.New(hitlservice.NewFSPolicySource(t.TempDir()), runtimetypes.LocalTenantID, store, libtracker.NoopTracker{})
	}

	mux := http.NewServeMux()
	if _, err := New(ctx, mux, "test-node", "local", &Config{}, deps); err != nil {
		t.Fatalf("serverapi.New: %v", err)
	}
	return mux
}

func TestServe_ApprovalRoutesGatedOnHITL(t *testing.T) {
	withHITL := approvalGatingMux(t, true)
	rr := httptest.NewRecorder()
	withHITL.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/approvals", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("with HITL: /approvals = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}

	withoutHITL := approvalGatingMux(t, false)
	rr = httptest.NewRecorder()
	withoutHITL.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/approvals", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("without HITL: /approvals = %d, want 404", rr.Code)
	}
}
