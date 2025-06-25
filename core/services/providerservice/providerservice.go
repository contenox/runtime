package providerservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
)

type Service interface {
	SetProviderConfig(ctx context.Context, providerType string, config *ProviderConfig) error
	GetProviderConfig(ctx context.Context, providerType string) (*ProviderConfig, error)
	DeleteProviderConfig(ctx context.Context, providerType string) error
	ListProviderConfigs(ctx context.Context) ([]*ProviderConfig, error)
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
	return "provider-service"
}

func New(dbInstance libdb.DBManager) Service {
	return &service{dbInstance: dbInstance}
}

const (
	ProviderKeyPrefix = "cloud-provider:"
	OpenaiKey         = ProviderKeyPrefix + "openai"
	GeminiKey         = ProviderKeyPrefix + "gemini"
)

type ProviderConfig struct {
	APIKey    string // TODO: Implement encryption before saving
	ModelName string
	Type      string
}

func (s *service) SetProviderConfig(ctx context.Context, providerType string, config *ProviderConfig) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	storeInstance := store.New(tx)
	if config == nil {
		return fmt.Errorf("missing config")
	}
	if providerType != "openai" && providerType != "gemini" {
		return fmt.Errorf("invalid provider type: %s", providerType)
	}
	key := ProviderKeyPrefix + providerType
	config.Type = providerType
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return storeInstance.SetKV(ctx, key, data)
}

func (s *service) GetProviderConfig(ctx context.Context, providerType string) (*ProviderConfig, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	var config ProviderConfig
	key := ProviderKeyPrefix + providerType
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

	key := ProviderKeyPrefix + providerType
	return storeInstance.DeleteKV(ctx, key)
}

func (s *service) ListProviderConfigs(ctx context.Context) ([]*ProviderConfig, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	storeInstance := store.New(tx)

	kvs, err := storeInstance.ListKVPrefix(ctx, ProviderKeyPrefix)
	if err != nil {
		return nil, err
	}

	var configs []*ProviderConfig
	for _, kv := range kvs {
		var config ProviderConfig
		if err := json.Unmarshal(kv.Value, &config); err == nil {
			configs = append(configs, &config)
		}
	}
	return configs, nil
}
