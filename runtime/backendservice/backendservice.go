package backendservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

var ErrInvalidBackend = errors.New("invalid backend data")

type Service interface {
	Create(ctx context.Context, backend *runtimetypes.Backend) error
	Get(ctx context.Context, id string) (*runtimetypes.Backend, error)
	Update(ctx context.Context, backend *runtimetypes.Backend) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*runtimetypes.Backend, error)
}

type service struct {
	dbInstance libdb.DBManager
}

type duplicateBackendError struct {
	typ     string
	baseURL string
	cause   error
}

func (e duplicateBackendError) Error() string {
	return fmt.Sprintf("backend already exists for type %q and base URL %q", e.typ, e.baseURL)
}

func (e duplicateBackendError) Unwrap() error {
	return e.cause
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
}

func (s *service) Create(ctx context.Context, backend *runtimetypes.Backend) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := validate(backend); err != nil {
		return err
	}
	storeInstance := runtimetypes.New(tx)
	count, err := storeInstance.EstimateBackendCount(ctx)
	if err != nil {
		return err
	}
	if err := storeInstance.EnforceMaxRowCount(ctx, count); err != nil {
		return fmt.Errorf("too many rows in the system (current %d): %w", count, err)
	}

	if duplicate, err := sameConnectionExists(ctx, storeInstance, backend, ""); err != nil {
		return err
	} else if duplicate {
		return duplicateBackendError{typ: backend.Type, baseURL: backend.BaseURL, cause: libdb.ErrUniqueViolation}
	}

	if err := storeInstance.CreateBackend(ctx, backend); err != nil {
		return sanitizeBackendStoreError(backend, err)
	}
	return nil
}

func (s *service) Get(ctx context.Context, id string) (*runtimetypes.Backend, error) {
	tx := s.dbInstance.WithoutTransaction()
	return runtimetypes.New(tx).GetBackend(ctx, id)
}

func (s *service) Update(ctx context.Context, backend *runtimetypes.Backend) error {
	if err := validate(backend); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	storeInstance := runtimetypes.New(tx)
	if duplicate, err := sameConnectionExists(ctx, storeInstance, backend, backend.ID); err != nil {
		return err
	} else if duplicate {
		return duplicateBackendError{typ: backend.Type, baseURL: backend.BaseURL, cause: libdb.ErrUniqueViolation}
	}
	if err := storeInstance.UpdateBackend(ctx, backend); err != nil {
		return sanitizeBackendStoreError(backend, err)
	}
	return nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx, commit, release, err := s.dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	store := runtimetypes.New(tx)
	if err := store.LockBackend(ctx, id); err != nil {
		return err
	}
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return err
	}
	if err := store.DeleteKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID)); err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return err
	}
	if err := modelauth.DeleteCredentials(ctx, tx, id); err != nil {
		return err
	}
	if err := store.DeleteBackend(ctx, id); err != nil {
		return err
	}
	return commit(ctx)
}

func (s *service) List(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*runtimetypes.Backend, error) {
	tx := s.dbInstance.WithoutTransaction()
	return runtimetypes.New(tx).ListBackends(ctx, createdAtCursor, limit)
}

func validate(backend *runtimetypes.Backend) error {
	if backend.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidBackend)
	}
	if backend.BaseURL == "" {
		return fmt.Errorf("%w: baseURL is required", ErrInvalidBackend)
	}
	switch modelrepo.CanonicalBackendType(backend.Type) {
	case "ollama", "vllm", "openai", "openrouter", "openai-codex", "anthropic", "mistral", "deepseek", "bedrock", "gemini", "vertex-google", "scripted-test":
	default:
		return fmt.Errorf("%w: Type must be ollama, vllm, openrouter, openai, openai-codex, anthropic, mistral, deepseek, bedrock, gemini, vertex-google, or scripted-test (test only)", ErrInvalidBackend)
	}

	return nil
}

func sameConnectionExists(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend, excludeID string) (bool, error) {
	count, err := store.CountBackendsByTypeAndURL(
		ctx,
		modelrepo.CanonicalBackendType(backend.Type),
		strings.TrimSpace(backend.BaseURL),
		excludeID,
	)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func sanitizeBackendStoreError(backend *runtimetypes.Backend, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, libdb.ErrUniqueViolation) && backend != nil {
		return duplicateBackendError{
			typ:     backend.Type,
			baseURL: backend.BaseURL,
			cause:   err,
		}
	}
	return err
}

// Validate applies the field rules Create and Update enforce, without the
// one-backend-per-type-and-URL rule. Callers that deliberately allow several
// connections to one endpoint (each with its own credential) use it before
// writing through the store.
func Validate(backend *runtimetypes.Backend) error {
	return validate(backend)
}
