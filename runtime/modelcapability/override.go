package modelcapability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const KeyPrefix = "model-capability:"

type Override struct {
	CompletionMode *string `json:"completionMode,omitempty"`
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	CanThink       *bool   `json:"canThink,omitempty"`
	CanVision      *bool   `json:"canVision,omitempty"`
}

type storedOverride struct {
	CompletionMode *string `json:"completionMode,omitempty"`
	CanThink       *bool   `json:"canThink"`
	CanVision      *bool   `json:"canVision,omitempty"`
}

type Service struct {
	store runtimetypes.Store
}

func New(store runtimetypes.Store) Service {
	return Service{store: store}
}

func Key(provider, model string) (string, string, string, error) {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.TrimSpace(model)
	if p == "" {
		return "", "", "", fmt.Errorf("provider is required")
	}
	if m == "" {
		return "", "", "", fmt.Errorf("model is required")
	}
	return KeyPrefix + p + ":" + m, p, m, nil
}

func (s Service) SetThink(ctx context.Context, provider, model string, canThink bool) (*Override, error) {
	return s.set(ctx, provider, model, func(v *storedOverride) { v.CanThink = &canThink })
}

func (s Service) SetVision(ctx context.Context, provider, model string, canVision bool) (*Override, error) {
	return s.set(ctx, provider, model, func(v *storedOverride) { v.CanVision = &canVision })
}

func (s Service) SetCompletion(ctx context.Context, provider, model, mode string) (*Override, error) {
	if mode != "" && mode != "instruct" && mode != "prompt" {
		return nil, fmt.Errorf("unsupported code-completion mode %q", mode)
	}
	return s.set(ctx, provider, model, func(value *storedOverride) { value.CompletionMode = &mode })
}

func (s Service) set(ctx context.Context, provider, model string, apply func(*storedOverride)) (*Override, error) {
	if s.store == nil {
		return nil, fmt.Errorf("store is required")
	}
	key, p, m, err := Key(provider, model)
	if err != nil {
		return nil, err
	}
	v, _, err := s.read(ctx, key, alternateKey(p, m))
	if err != nil {
		return nil, err
	}
	apply(&v)
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetKV(ctx, key, json.RawMessage(data)); err != nil {
		return nil, err
	}
	return &Override{Provider: p, Model: m, CanThink: v.CanThink, CanVision: v.CanVision, CompletionMode: v.CompletionMode}, nil
}

func (s Service) Get(ctx context.Context, provider, model string) (*Override, bool, error) {
	if s.store == nil {
		return nil, false, fmt.Errorf("store is required")
	}
	key, p, m, err := Key(provider, model)
	if err != nil {
		return nil, false, err
	}
	v, found, err := s.read(ctx, key, alternateKey(p, m))
	if err != nil || !found {
		return nil, false, err
	}
	return &Override{Provider: p, Model: m, CanThink: v.CanThink, CanVision: v.CanVision, CompletionMode: v.CompletionMode}, true, nil
}

func (s Service) Unset(ctx context.Context, provider, model string) (bool, error) {
	if s.store == nil {
		return false, fmt.Errorf("store is required")
	}
	key, p, m, err := Key(provider, model)
	if err != nil {
		return false, err
	}
	removed := false
	for _, candidate := range []string{key, alternateKey(p, m)} {
		if err := s.store.DeleteKV(ctx, candidate); err != nil {
			if errors.Is(err, libdb.ErrNotFound) {
				continue
			}
			return false, err
		}
		removed = true
	}
	return removed, nil
}

func (s Service) read(ctx context.Context, keys ...string) (storedOverride, bool, error) {
	for _, key := range keys {
		var v storedOverride
		err := s.store.GetKV(ctx, key, &v)
		if err == nil {
			return v, true, nil
		}
		if !errors.Is(err, libdb.ErrNotFound) {
			return storedOverride{}, false, err
		}
	}
	return storedOverride{}, false, nil
}

func alternateKey(provider, model string) string {
	if bare := modelrepo.ModelID(model); bare != model {
		return KeyPrefix + provider + ":" + bare
	}
	return KeyPrefix + provider + ":" + modelrepo.ModelResourcePrefix + model
}
