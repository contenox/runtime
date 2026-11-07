package backendapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/internal/backendapi"
	"github.com/contenox/runtime/runtime/internal/missionapi"
	"github.com/contenox/runtime/runtime/missionservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func TestCreateBackendDuplicateReturnsSanitizedConflict(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "backendapi.db"), runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mux := http.NewServeMux()
	backendapi.AddBackendRoutes(mux, backendservice.New(db), &stubStateService{})

	body := map[string]string{
		"name":    "first",
		"type":    "ollama",
		"baseUrl": "http://127.0.0.1:11434",
	}
	postJSON(t, mux, "/backends", body, http.StatusCreated)

	body["name"] = "second"
	rr := postJSON(t, mux, "/backends", body, http.StatusConflict)

	var got struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error.Type != "invalid_request_error" || got.Error.Code != "conflict" {
		t.Fatalf("error type/code = %q/%q, want invalid_request_error/conflict", got.Error.Type, got.Error.Code)
	}
	if !strings.Contains(got.Error.Message, `backend already exists for type "ollama" and base URL "http://127.0.0.1:11434"`) {
		t.Fatalf("unexpected message: %q", got.Error.Message)
	}
	for _, leaked := range []string{"libdb:", "UNIQUE constraint", "llm_backends", "2067"} {
		if strings.Contains(got.Error.Message, leaked) {
			t.Fatalf("response leaked %q: %q", leaked, got.Error.Message)
		}
	}
}

func TestListPaginationErrorsAgreeAcrossRoutes(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "listparams.db"), runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mux := http.NewServeMux()
	backendapi.AddBackendRoutes(mux, backendservice.New(db), &stubStateService{})
	missionapi.AddRoutes(mux, missionservice.New(db))

	for _, query := range []string{"cursor=garbage", "limit=not-a-number", "limit=0", "limit=-1"} {
		statuses := map[string]int{}
		for _, path := range []string{"/backends", "/missions"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
			statuses[path] = rr.Code
		}
		if statuses["/backends"] != statuses["/missions"] {
			t.Fatalf("?%s: GET /backends = %d but GET /missions = %d; the two must agree",
				query, statuses["/backends"], statuses["/missions"])
		}
		if statuses["/backends"] != http.StatusBadRequest {
			t.Fatalf("?%s: status = %d, want %d — a malformed parameter is not a missing resource",
				query, statuses["/backends"], http.StatusBadRequest)
		}
	}

	for _, path := range []string{"/backends", "/missions"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path+"?limit=5&cursor=2024-03-01T12:00:00Z", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s with valid pagination = %d, want %d: %s", path, rr.Code, http.StatusOK, rr.Body.String())
		}
	}
}

func postJSON(t *testing.T, mux *http.ServeMux, path string, body any, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("POST %s status = %d, want %d: %s", path, rr.Code, wantStatus, rr.Body.String())
	}
	return rr
}

func TestBackendsRedactTheVertexProjectInDisplayURL(t *testing.T) {
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "displayurl.db"), runtimetypes.SchemaSQLite)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mux := http.NewServeMux()
	backendapi.AddBackendRoutes(mux, backendservice.New(db), &stubStateService{})

	vertexURL := "https://us-central1-aiplatform.googleapis.com/v1/projects/secret-project-123/locations/us-central1"
	postJSON(t, mux, "/backends", map[string]string{"name": "vtx", "type": "vertex-google", "baseUrl": vertexURL}, http.StatusCreated)
	postJSON(t, mux, "/backends", map[string]string{"name": "oll", "type": "ollama", "baseUrl": "http://127.0.0.1:11434"}, http.StatusCreated)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/backends", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /backends = %d: %s", rr.Code, rr.Body.String())
	}
	var list []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		BaseURL    string `json:"baseUrl"`
		DisplayURL string `json:"displayUrl"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]int{}
	for i, b := range list {
		byName[b.Name] = i
	}
	vtx, oll := list[byName["vtx"]], list[byName["oll"]]
	if vtx.BaseURL != vertexURL {
		t.Fatalf("baseUrl must stay editable, got %q", vtx.BaseURL)
	}
	if want := "https://us-central1-aiplatform.googleapis.com/v1/projects/…/locations/us-central1"; vtx.DisplayURL != want {
		t.Fatalf("displayUrl = %q, want %q", vtx.DisplayURL, want)
	}
	if oll.DisplayURL != oll.BaseURL {
		t.Fatalf("displayUrl = %q, want the base URL", oll.DisplayURL)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/backends/"+vtx.ID, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /backends/{id} = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"displayUrl":"`+vertexURL) || !strings.Contains(rr.Body.String(), "projects/…/locations") {
		t.Fatalf("detail displayUrl not redacted: %s", rr.Body.String())
	}
}
