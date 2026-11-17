package inferencesetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

// RoleRevision identifies persisted assignment and execution-setting changes for a role.
func (s *Service) RoleRevision(ctx context.Context, role string) (string, error) {
	switch role {
	case RoleChat, RoleRecovery, RoleAutocomplete, RoleNextEdit, RoleEmbedding:
		return verificationRevision(ctx, runtimetypes.New(s.db.WithoutTransaction()), role)
	default:
		return "", fmt.Errorf("unknown inference role %q", role)
	}
}

func revision(ctx context.Context, store runtimetypes.Store, key string) (runtimetypes.KVRevision, error) {
	value, err := store.GetKVRevision(ctx, key)
	if errors.Is(err, libdbexec.ErrNotFound) {
		return runtimetypes.KVRevision{}, nil
	}
	return value, err
}

func roleRevision(ctx context.Context, store runtimetypes.Store, role string) (string, error) {
	keys := roleKeys(role)
	return keyRevisions(ctx, store, keys[:])
}

func verificationRevision(ctx context.Context, store runtimetypes.Store, role string) (string, error) {
	routeKeys := roleKeys(role)
	keys := append(routeKeys[:], "inference.modeld."+role)
	keys = append(keys, "inference.inputs."+role)
	keys = append(keys, "inference.assignment."+role)
	if role == RoleAutocomplete {
		keys = append(keys, "default-autocomplete-think")
	}
	return keyRevisions(ctx, store, keys)
}

func keyRevisions(ctx context.Context, store runtimetypes.Store, keys []string) (string, error) {
	values := make([]runtimetypes.KVRevision, 0, len(keys))
	for _, key := range keys {
		value, err := revision(ctx, store, clikv.Prefix+key)
		if err != nil {
			return "", err
		}
		values = append(values, value)
	}
	return digestRevision(values)
}

func connectionRevision(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend) (string, error) {
	credential, err := store.GetKVRevision(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID))
	if errors.Is(err, libdbexec.ErrNotFound) {
		credential, err = revision(ctx, store, runtimestate.ProviderKeyPrefix+backend.Type)
	}
	if err != nil {
		return "", err
	}
	generation := ""
	if backend.Type == modelauth.ProviderType {
		generation, err = modelauth.CredentialGeneration(ctx, store, backend.ID)
		if err != nil {
			return "", err
		}
	}
	return digestRevision(struct {
		ID         string
		Revision   runtimetypes.KVRevision
		Credential runtimetypes.KVRevision
		Generation string
	}{backend.ID, runtimetypes.KVRevision{CreatedAt: backend.CreatedAt, Revision: backend.Revision}, credential, generation})
}

func digestRevision(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
