package enginesvc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/internal/setupcheck"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func hasIssueCode(issues []setupcheck.Issue, code string) bool {
	for _, iss := range issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}

func issueCodes(issues []setupcheck.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		out = append(out, iss.Code)
	}
	return out
}

func TestSystem_Build_ReadinessDefaultModel_creditsExplicitFlag(t *testing.T) {
	ctx := context.Background()
	newDB := func(t *testing.T) libdbexec.DBManager {
		t.Helper()
		path := filepath.Join(t.TempDir(), "b001.db")
		db, err := libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	baseCfg := func() enginesvc.Config {
		return enginesvc.Config{
			DefaultModel:     "phi-4-mini",
			DefaultProvider:  "llama",
			SkipBackendCycle: true,
			NoDeleteModels:   true,
		}
	}

	t.Run("without readiness override the missing_default_model issue is present", func(t *testing.T) {
		eng, err := enginesvc.Build(ctx, newDB(t), baseCfg())
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		defer eng.Stop()
		if !hasIssueCode(eng.SetupCheck.Issues, "missing_default_model") {
			t.Fatalf("baseline should report missing_default_model; issues=%v", issueCodes(eng.SetupCheck.Issues))
		}
	})

	t.Run("without a configured model the engine still builds and reports the issue", func(t *testing.T) {
		cfg := baseCfg()
		cfg.DefaultModel = ""
		cfg.DefaultProvider = ""
		eng, err := enginesvc.Build(ctx, newDB(t), cfg)
		if err != nil {
			t.Fatalf("build engine without a configured model: %v", err)
		}
		defer eng.Stop()
		if !hasIssueCode(eng.SetupCheck.Issues, "missing_default_model") {
			t.Fatalf("unconfigured engine must report missing_default_model; issues=%v", issueCodes(eng.SetupCheck.Issues))
		}
	})

	t.Run("with readiness override the model is credited and the issue is gone", func(t *testing.T) {
		cfg := baseCfg()
		cfg.ReadinessDefaultModel = "phi-4-mini"
		eng, err := enginesvc.Build(ctx, newDB(t), cfg)
		if err != nil {
			t.Fatalf("build engine: %v", err)
		}
		defer eng.Stop()

		if hasIssueCode(eng.SetupCheck.Issues, "missing_default_model") {
			t.Errorf("snapshot still reports missing_default_model; issues=%v", issueCodes(eng.SetupCheck.Issues))
		}
		if eng.SetupCheck.DefaultModel != "phi-4-mini" {
			t.Errorf("snapshot DefaultModel = %q, want phi-4-mini", eng.SetupCheck.DefaultModel)
		}

		live, err := eng.SetupStatus(ctx)
		if err != nil {
			t.Fatalf("live SetupStatus: %v", err)
		}
		if hasIssueCode(live.Issues, "missing_default_model") {
			t.Errorf("live recompute still reports missing_default_model; issues=%v", issueCodes(live.Issues))
		}
		if live.DefaultModel != "phi-4-mini" {
			t.Errorf("live DefaultModel = %q, want phi-4-mini", live.DefaultModel)
		}
	})
}

func TestSystem_Build_BackendCycleBudget_boundsAnUnresponsiveProvider(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "b002.db")
	db, err := libdbexec.NewSQLiteDBManager(ctx, path, runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		server.Close()
	})

	if err := runtimetypes.New(db.WithoutTransaction()).CreateBackend(ctx, &runtimetypes.Backend{
		ID:      "unresponsive",
		Name:    "unresponsive",
		Type:    "ollama",
		BaseURL: server.URL,
	}); err != nil {
		t.Fatalf("create backend: %v", err)
	}

	t.Run("deferred discovery leaves local reads available", func(t *testing.T) {
		buildCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		eng, err := enginesvc.Build(buildCtx, db, enginesvc.Config{DeferDiscovery: true})
		require.NoError(t, err)
		defer eng.Stop()
		require.NotContains(t, eng.State.Get(ctx), "unresponsive")

		stop, err := eng.State.StartDiscoveryWithBudget(buildCtx, time.Second)
		require.NoError(t, err)
		defer stop()
		require.NoError(t, eng.State.ReconcileIfStale(buildCtx))
		probeCtx, stopWaiting := context.WithTimeout(buildCtx, 50*time.Millisecond)
		defer stopWaiting()
		_, err = eng.SetupStatus(probeCtx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	start := time.Now()
	eng, err := enginesvc.Build(ctx, db, enginesvc.Config{
		BackendCycleBudget: 300 * time.Millisecond,
		NoDeleteModels:     true,
	})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	defer eng.Stop()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Build took %s; the budget must bound the first backend cycle", elapsed)
	}
	if _, ok := eng.State.Get(ctx)["unresponsive"]; !ok {
		t.Fatal("state has no entry for the unresponsive provider")
	}
	once.Do(func() { close(release) })
}
