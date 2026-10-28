package backendservice

import (
	"encoding/json"
	"errors"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

func TestUnit_BackendService_DeleteRemovesBackendCredential(t *testing.T) {
	ctx, db := setupBackendServiceDB(t)
	svc := New(db)
	backend := &runtimetypes.Backend{ID: uuid.NewString(), Name: "cloud", Type: "openai", BaseURL: "https://api.openai.com/v1"}
	if err := svc.Create(ctx, backend); err != nil {
		t.Fatalf("create backend: %v", err)
	}
	store := runtimetypes.New(db.WithoutTransaction())
	raw, err := json.Marshal(runtimestate.ProviderConfig{APIKey: "secret", Type: backend.Type})
	if err != nil {
		t.Fatalf("encode credential: %v", err)
	}
	key := runtimestate.BackendCredentialKey(backend.Type, backend.ID)
	if err := store.SetKV(ctx, key, raw); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	if err := svc.Delete(ctx, backend.ID); err != nil {
		t.Fatalf("delete backend: %v", err)
	}
	if _, err := svc.Get(ctx, backend.ID); !errors.Is(err, libdb.ErrNotFound) {
		t.Fatalf("deleted backend lookup = %v, want ErrNotFound", err)
	}
	var credential runtimestate.ProviderConfig
	if err := store.GetKV(ctx, key, &credential); !errors.Is(err, libdb.ErrNotFound) {
		t.Fatalf("deleted credential lookup = %v, want ErrNotFound", err)
	}
}

func TestUnit_BackendService_DeleteWithoutCredentialSucceeds(t *testing.T) {
	ctx, db := setupBackendServiceDB(t)
	svc := New(db)
	backend := &runtimetypes.Backend{ID: uuid.NewString(), Name: "local", Type: "ollama", BaseURL: "http://127.0.0.1:11434"}
	if err := svc.Create(ctx, backend); err != nil {
		t.Fatalf("create backend: %v", err)
	}
	if err := svc.Delete(ctx, backend.ID); err != nil {
		t.Fatalf("delete backend without stored credential: %v", err)
	}
	if err := svc.Delete(ctx, backend.ID); !errors.Is(err, libdb.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
