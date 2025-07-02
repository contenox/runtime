package providerservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
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
	tx, com, r, err := s.dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer r()
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
	key := serverops.ProviderKeyPrefix + providerType
	config.Type = providerType
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	dbOp := func(ctx context.Context, key string, value json.RawMessage) error {
		return storeInstance.SetKV(ctx, key, value)
	}
	if replace {
		dbOp = func(ctx context.Context, key string, value json.RawMessage) error {
			var lConfig *serverops.ProviderConfig
			err := storeInstance.GetKV(ctx, key, lConfig)
			if errors.Is(err, libdb.ErrNotFound) {
				return storeInstance.SetKV(ctx, key, value)
			}
			if err != nil {
				return err
			}
			return storeInstance.UpdateKV(ctx, key, value)
		}
	}
	err = dbOp(ctx, key, data)
	if err != nil {
		return err
	}
	backendUrl := ""
	if providerType == "openai" {
		backendUrl = "https://api.openai.com/v1"
	}
	if providerType == "gemini" {
		backendUrl = "https://generativelanguage.googleapis.com"
	}
	err = storeInstance.CreateBackend(ctx, &store.Backend{
		ID:      providerType,
		Name:    providerType,
		BaseURL: backendUrl,
		Type:    providerType,
	})
	if err != nil {
		return err
	}
	err = com(ctx)
	if err != nil {
		return err
	}
	return nil
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
