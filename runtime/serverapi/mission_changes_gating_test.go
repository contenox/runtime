package serverapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/missionchanges"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func missionChangesGatingMux(t *testing.T, withChanges bool) (*http.ServeMux, string) {
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

	missions := missionservice.New(db)
	m := &missionservice.Mission{Intent: "review target", AgentName: "agent", HITLPolicyName: "default"}
	if err := missions.Create(ctx, m); err != nil {
		t.Fatalf("seed mission: %v", err)
	}

	deps := Dependencies{DB: db, State: state}
	if withChanges {
		deps.MissionChanges = missionchanges.New(missions, nil)
	}

	mux := http.NewServeMux()
	if _, err := New(ctx, mux, "test-node", "local", &Config{}, deps); err != nil {
		t.Fatalf("serverapi.New: %v", err)
	}
	return mux, m.ID
}

func TestServe_MissionChangesRoutesGatedOnService(t *testing.T) {
	withChanges, id := missionChangesGatingMux(t, true)
	rr := httptest.NewRecorder()
	withChanges.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/missions/"+id+"/changes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("with changes: /missions/%s/changes = %d, want 200 (body %s)", id, rr.Code, rr.Body.String())
	}

	withoutChanges, id2 := missionChangesGatingMux(t, false)
	rr = httptest.NewRecorder()
	withoutChanges.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/missions/"+id2+"/changes", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("without changes: /missions/%s/changes = %d, want 404", id2, rr.Code)
	}
}
