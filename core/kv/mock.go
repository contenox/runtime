package kv

import (
	"context"
	"errors"
)

// MockSettingsRepo is a mock implementation of SettingsRepo for testing.
type MockSettingsRepo struct {
	GetFunc func(ctx context.Context, key string, out any) error
}

var _ Repo = (*MockSettingsRepo)(nil) // Ensures it satisfies the interface

func (m *MockSettingsRepo) Get(ctx context.Context, key string, out any) error {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, key, out)
	}
	return errors.New("mock Get method not implemented")
}

func (m *MockSettingsRepo) ProcessTick(ctx context.Context) error {
	return errors.New("mock ProcessTick method not implemented")
}
