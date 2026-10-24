package runtimestate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	libdbexec "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func AdoptSharedCredentialRows(ctx context.Context, store runtimetypes.Store) error {
	rows, err := store.ListKVPrefix(ctx, ProviderKeyPrefix, nil, runtimetypes.MAXLIMIT)
	if err != nil {
		return fmt.Errorf("list credential records: %w", err)
	}
	var backends []*runtimetypes.Backend
	for _, row := range rows {
		sharedType := strings.TrimPrefix(row.Key, ProviderKeyPrefix)
		if sharedType == "" || strings.Contains(sharedType, ":") {
			continue
		}
		if backends == nil {
			if backends, err = store.ListAllBackends(ctx); err != nil {
				return fmt.Errorf("list connections: %w", err)
			}
		}
		var shared ProviderConfig
		if err := json.Unmarshal(row.Value, &shared); err != nil {
			return fmt.Errorf("read the shared %s credential row %q: %w", sharedType, row.Key, err)
		}
		for _, backend := range backends {
			if !strings.EqualFold(backend.Type, sharedType) {
				continue
			}
			if err := adoptSharedCredential(ctx, store, backend, shared); err != nil {
				return err
			}
		}
		if err := store.DeleteKV(ctx, row.Key); err != nil && !errors.Is(err, libdbexec.ErrNotFound) {
			return fmt.Errorf("delete the shared %s credential row %q: %w", sharedType, row.Key, err)
		}
	}
	return nil
}

func adoptSharedCredential(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend, shared ProviderConfig) error {
	key := BackendCredentialKey(backend.Type, backend.ID)
	var existing ProviderConfig
	err := store.GetKV(ctx, key, &existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, libdbexec.ErrNotFound) {
		return fmt.Errorf("read the credential record of connection %q: %w", backend.Name, err)
	}
	record := ProviderConfig{Type: backend.Type}
	switch shared.Source(backend.Type) {
	case CredentialEnvironment:
		record.CredentialSource, record.APIKeyEnv = CredentialEnvironment, strings.TrimSpace(shared.APIKeyEnv)
	case CredentialStored:
		record.CredentialSource, record.APIKey = CredentialStored, shared.APIKey
	default:
		return nil
	}
	value, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode the credential record of connection %q: %w", backend.Name, err)
	}
	if err := store.SetKV(ctx, key, value); err != nil {
		return fmt.Errorf("write the credential record of connection %q: %w", backend.Name, err)
	}
	return nil
}
