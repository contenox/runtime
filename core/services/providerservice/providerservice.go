package providerservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
)

const (
	ProviderTypeOpenAI = "openai"
	ProviderTypeGemini = "gemini"
)

type Service interface {
	SetProviderConfig(ctx context.Context, providerType string, upsert bool, config *serverops.ProviderConfig) error
	GetProviderConfig(ctx context.Context, providerType string) (*serverops.ProviderConfig, error)
	DeleteProviderConfig(ctx context.Context, providerType string) error
	ListProviderConfigs(ctx context.Context) ([]*serverops.ProviderConfig, error)
	GetServiceName() string
	GetServiceGroup() string
}

type service struct {
	dbInstance libdb.DBManager
}

// GetServiceGroup implements Service.
func (s *service) GetServiceGroup() string {
	return "providerservice"
}

// GetServiceName implements Service.
func (s *service) GetServiceName() string {
	return "providerservice"
}

func New(dbInstance libdb.DBManager) Service {
	return &service{dbInstance: dbInstance}
}

func (s *service) SetProviderConfig(ctx context.Context, providerType string, replace bool, config *serverops.ProviderConfig) error {
	// Input validation
	if providerType != ProviderTypeOpenAI && providerType != ProviderTypeGemini {
		return fmt.Errorf("invalid provider type: %s", providerType)
	}
	if config == nil {
		return fmt.Errorf("missing config")
	}
	if config.APIKey == "" {
		return fmt.Errorf("missing API key")
	}

	tx, com, r, err := s.dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer r()

	storeInstance := store.New(tx)
	key := serverops.ProviderKeyPrefix + providerType

	// Check existence if not replacing
	if !replace {
		var existing json.RawMessage
		if err := storeInstance.GetKV(ctx, key, &existing); err == nil {
			return fmt.Errorf("provider config already exists")
		} else if !errors.Is(err, libdb.ErrNotFound) {
			return fmt.Errorf("failed to check existing config: %w", err)
		}
	}

	// Prepare and store config
	config.Type = providerType
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := storeInstance.SetKV(ctx, key, data); err != nil {
		return fmt.Errorf("failed to store config: %w", err)
	}

	// Handle backend configuration
	backendURL := ""
	switch providerType {
	case ProviderTypeOpenAI:
		backendURL = "https://api.openai.com/v1"
	case ProviderTypeGemini:
		backendURL = "https://generativelanguage.googleapis.com"
	}

	// Upsert backend configuration
	backend := &store.Backend{
		ID:      providerType,
		Name:    providerType,
		BaseURL: backendURL,
		Type:    providerType,
	}

	if _, err := storeInstance.GetBackend(ctx, providerType); errors.Is(err, libdb.ErrNotFound) {
		if err := storeInstance.CreateBackend(ctx, backend); err != nil {
			return fmt.Errorf("failed to create backend: %w", err)
		}
	} else if err == nil && replace {
		// Update existing backend if replacing
		if err := storeInstance.UpdateBackend(ctx, backend); err != nil {
			return fmt.Errorf("failed to update backend: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check backend existence: %w", err)
	}

	return com(ctx)
}

func (s *service) GetProviderConfig(ctx context.Context, providerType string) (*serverops.ProviderConfig, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	var config serverops.ProviderConfig
	key := serverops.ProviderKeyPrefix + providerType
	storeInstance := store.New(tx)
	err := storeInstance.GetKV(ctx, key, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (s *service) DeleteProviderConfig(ctx context.Context, providerType string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	storeInstance := store.New(tx)

	key := serverops.ProviderKeyPrefix + providerType
	return storeInstance.DeleteKV(ctx, key)
}

func (s *service) ListProviderConfigs(ctx context.Context) ([]*serverops.ProviderConfig, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	storeInstance := store.New(tx)

	kvs, err := storeInstance.ListKVPrefix(ctx, serverops.ProviderKeyPrefix)
	if err != nil {
		return nil, err
	}

	var configs []*serverops.ProviderConfig
	for _, kv := range kvs {
		var config serverops.ProviderConfig
		if err := json.Unmarshal(kv.Value, &config); err == nil {
			configs = append(configs, &config)
		}
	}
	return configs, nil
}
