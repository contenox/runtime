package providerservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const (
	ProviderTypeLocal        = "local"
	ProviderTypeOllama       = "ollama"
	ProviderTypeOpenAI       = "openai"
	ProviderTypeAnthropic    = "anthropic"
	ProviderTypeMistral      = "mistral"
	ProviderTypeBedrock      = "bedrock"
	ProviderTypeGemini       = "gemini"
	ProviderTypeVLLM         = "vllm"
	ProviderTypeVertexGoogle = "vertex-google"
	ProviderTypeDeepSeek     = "deepseek"
	ProviderTypeOpenAICodex  = "openai-codex"
)

var ErrInvalidProvider = errors.New("invalid provider")

type ConfigureProviderRequest struct {
	APIKey       string
	APIKeyEnv    string
	BaseURL      string
	DefaultModel string
	Upsert       bool
	SetDefault   bool
}

type ProviderStatus struct {
	Provider             string    `json:"provider"`
	Configured           bool      `json:"configured"`
	BackendID            string    `json:"backendId,omitempty"`
	BackendName          string    `json:"backendName,omitempty"`
	BaseURL              string    `json:"baseUrl,omitempty"`
	SecretSource         string    `json:"secretSource"`
	SecretConfigured     bool      `json:"secretConfigured"`
	SecretPresent        bool      `json:"secretPresent"`
	APIKeyEnv            string    `json:"apiKeyEnv,omitempty"`
	RecommendedAPIKeyEnv string    `json:"recommendedApiKeyEnv,omitempty"`
	DefaultProvider      string    `json:"defaultProvider,omitempty"`
	DefaultModel         string    `json:"defaultModel,omitempty"`
	UpdatedAt            time.Time `json:"updatedAt,omitempty"`
	AuthState            string    `json:"authState,omitempty"`
	LoginRequired        bool      `json:"loginRequired,omitempty"`
}

type ProviderCapability struct {
	Provider             string `json:"provider"`
	DefaultBaseURL       string `json:"defaultBaseUrl,omitempty"`
	RequiresBaseURL      bool   `json:"requiresBaseUrl"`
	RequiresSecretConfig bool   `json:"requiresSecretConfig"`
	RequiresLogin        bool   `json:"requiresLogin,omitempty"`
	RecommendedAPIKeyEnv string `json:"recommendedApiKeyEnv,omitempty"`
}

type Service interface {
	Configure(ctx context.Context, providerType string, req ConfigureProviderRequest) (*ProviderStatus, error)
	GetProviderConfig(ctx context.Context, providerType string) (*ProviderStatus, error)
	DeleteProviderConfig(ctx context.Context, providerType string) error
	ListProviderConfigs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*ProviderStatus, error)
	ListSupportedProviders(ctx context.Context) ([]ProviderCapability, error)
	StartLogin(ctx context.Context, providerType string) (*LoginStart, error)
	LoginStatus(ctx context.Context, providerType string) (*LoginStatus, error)
	Logout(ctx context.Context, providerType string) error
}

type LoginStart struct {
	Provider  string    `json:"provider"`
	State     string    `json:"state"`
	URL       string    `json:"url,omitempty"`
	Code      string    `json:"code,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

type LoginStatus struct {
	Provider  string    `json:"provider"`
	State     string    `json:"state"`
	URL       string    `json:"url,omitempty"`
	Code      string    `json:"code,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type service struct {
	dbInstance  libdb.DBManager
	workspaceID string
}

type providerDefaults struct {
	Type                 string
	DefaultBaseURL       string
	RequiresBaseURL      bool
	RequiresSecretConfig bool
	RequiresLogin        bool
	RecommendedAPIKeyEnv string
}

var providerDefaultsByType = map[string]providerDefaults{
	ProviderTypeLocal:        {Type: ProviderTypeLocal, RequiresBaseURL: true},
	ProviderTypeOllama:       {Type: ProviderTypeOllama, DefaultBaseURL: "http://127.0.0.1:11434", RecommendedAPIKeyEnv: "OLLAMA_API_KEY"},
	ProviderTypeOpenAI:       {Type: ProviderTypeOpenAI, DefaultBaseURL: "https://api.openai.com/v1", RequiresSecretConfig: true, RecommendedAPIKeyEnv: "OPENAI_API_KEY"},
	ProviderTypeAnthropic:    {Type: ProviderTypeAnthropic, DefaultBaseURL: "https://api.anthropic.com", RequiresSecretConfig: true, RecommendedAPIKeyEnv: "ANTHROPIC_API_KEY"},
	ProviderTypeMistral:      {Type: ProviderTypeMistral, DefaultBaseURL: "https://api.mistral.ai/v1", RequiresSecretConfig: true, RecommendedAPIKeyEnv: "MISTRAL_API_KEY"},
	ProviderTypeBedrock:      {Type: ProviderTypeBedrock, RequiresBaseURL: true},
	ProviderTypeGemini:       {Type: ProviderTypeGemini, DefaultBaseURL: "https://generativelanguage.googleapis.com", RequiresSecretConfig: true, RecommendedAPIKeyEnv: "GEMINI_API_KEY"},
	ProviderTypeVLLM:         {Type: ProviderTypeVLLM, RequiresBaseURL: true},
	ProviderTypeVertexGoogle: {Type: ProviderTypeVertexGoogle, RequiresBaseURL: true, RecommendedAPIKeyEnv: "GOOGLE_APPLICATION_CREDENTIALS"},
	ProviderTypeDeepSeek:     {Type: ProviderTypeDeepSeek, DefaultBaseURL: "https://api.deepseek.com", RequiresSecretConfig: true, RecommendedAPIKeyEnv: "DEEPSEEK_API_KEY"},
	ProviderTypeOpenAICodex:  {Type: ProviderTypeOpenAICodex, DefaultBaseURL: modelauth.BaseURL, RequiresLogin: true},
}

func supportedProviders() []ProviderCapability {
	out := make([]ProviderCapability, 0, len(providerDefaultsByType))
	for _, defaults := range providerDefaultsByType {
		out = append(out, ProviderCapability{
			Provider:             defaults.Type,
			DefaultBaseURL:       defaults.DefaultBaseURL,
			RequiresBaseURL:      defaults.RequiresBaseURL,
			RequiresSecretConfig: defaults.RequiresSecretConfig,
			RequiresLogin:        defaults.RequiresLogin,
			RecommendedAPIKeyEnv: defaults.RecommendedAPIKeyEnv,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Provider < out[j].Provider
	})
	return out
}

func New(dbInstance libdb.DBManager, workspaceID string) Service {
	return &service{dbInstance: dbInstance, workspaceID: workspaceID}
}

func (s *service) ListSupportedProviders(context.Context) ([]ProviderCapability, error) {
	return supportedProviders(), nil
}

func (s *service) Configure(ctx context.Context, providerType string, req ConfigureProviderRequest) (*ProviderStatus, error) {
	providerType, defaults, err := normalizeProvider(providerType)
	if err != nil {
		return nil, err
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.APIKeyEnv = strings.TrimSpace(req.APIKeyEnv)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.DefaultModel = strings.TrimSpace(req.DefaultModel)

	if req.APIKey != "" && req.APIKeyEnv != "" {
		return nil, fmt.Errorf("%w: provide apiKey or apiKeyEnv, not both", ErrInvalidProvider)
	}

	baseURL := req.BaseURL
	if baseURL == "" {
		baseURL = defaults.DefaultBaseURL
	}
	if defaults.RequiresBaseURL && baseURL == "" {
		return nil, fmt.Errorf("%w: baseUrl is required for %s", ErrInvalidProvider, providerType)
	}
	if defaults.RequiresLogin {
		if req.APIKey != "" || req.APIKeyEnv != "" {
			return nil, fmt.Errorf("%w: %s signs in with a device code; an API key does not apply", ErrInvalidProvider, providerType)
		}
		baseURL = defaults.DefaultBaseURL
	}

	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	existingBackend, backendErr := store.GetBackendByName(ctx, providerType)
	existingConfig, configErr := s.readProviderConfig(ctx, providerType)
	alreadyConfigured := backendErr == nil || configErr == nil
	if alreadyConfigured && !req.Upsert {
		return nil, fmt.Errorf("%w: provider %s is already configured; set upsert=true to update", ErrInvalidProvider, providerType)
	}
	if backendErr != nil && !errors.Is(backendErr, libdb.ErrNotFound) {
		return nil, fmt.Errorf("check backend: %w", backendErr)
	}
	if configErr != nil && !errors.Is(configErr, libdb.ErrNotFound) {
		return nil, fmt.Errorf("check provider config: %w", configErr)
	}

	cfg := existingConfig
	if cfg == nil {
		cfg = &runtimestate.ProviderConfig{Type: providerType}
	}
	if req.APIKey != "" {
		cfg.APIKey = req.APIKey
		cfg.APIKeyEnv = ""
	}
	if req.APIKeyEnv != "" {
		cfg.APIKey = ""
		cfg.APIKeyEnv = req.APIKeyEnv
	}
	cfg.Type = providerType

	if defaults.RequiresSecretConfig && cfg.APIKey == "" && cfg.APIKeyEnv == "" {
		return nil, fmt.Errorf("%w: apiKey or apiKeyEnv is required for %s", ErrInvalidProvider, providerType)
	}

	if cfg.APIKey != "" || cfg.APIKeyEnv != "" {
		if err := s.writeProviderConfig(ctx, store, providerType, *cfg); err != nil {
			return nil, err
		}
	}

	backend := &runtimetypes.Backend{
		ID:      uuid.NewString(),
		Name:    providerType,
		Type:    providerType,
		BaseURL: baseURL,
	}
	backendSvc := backendservice.New(s.dbInstance)
	if existingBackend != nil {
		backend.ID = existingBackend.ID
		if err := backendSvc.Update(ctx, backend); err != nil {
			return nil, fmt.Errorf("update backend: %w", err)
		}
	} else {
		if err := backendSvc.Create(ctx, backend); err != nil {
			return nil, fmt.Errorf("create backend: %w", err)
		}
	}

	if cfg.APIKey != "" || cfg.APIKeyEnv != "" {
		if err := s.writeBackendCredential(ctx, store, providerType, backend.ID, *cfg); err != nil {
			return nil, err
		}
	}

	if req.SetDefault {
		if err := clikv.WriteConfig(ctx, store, s.workspaceID, "default-provider", providerType); err != nil {
			return nil, fmt.Errorf("set default-provider: %w", err)
		}
		if req.DefaultModel != "" {
			if err := clikv.WriteConfig(ctx, store, s.workspaceID, "default-model", req.DefaultModel); err != nil {
				return nil, fmt.Errorf("set default-model: %w", err)
			}
		}
		if strings.TrimSpace(clikv.Read(ctx, store, "default-oracle-chain")) == "" {
			_ = clikv.WriteConfig(ctx, store, s.workspaceID, "default-oracle-chain", "chain-oracle-default.json")
		}
		if strings.TrimSpace(clikv.Read(ctx, store, "default-oracle-policy")) == "" {
			_ = clikv.WriteConfig(ctx, store, s.workspaceID, "default-oracle-policy", "hitl-policy-oracle.json")
		}
	}

	return s.GetProviderConfig(ctx, providerType)
}

func (s *service) GetProviderConfig(ctx context.Context, providerType string) (*ProviderStatus, error) {
	providerType, defaults, err := normalizeProvider(providerType)
	if err != nil {
		return nil, err
	}
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	backend, backendErr := store.GetBackendByName(ctx, providerType)
	cfg, configErr := s.readProviderConfig(ctx, providerType)
	if errors.Is(backendErr, libdb.ErrNotFound) && errors.Is(configErr, libdb.ErrNotFound) {
		return nil, libdb.ErrNotFound
	}
	if backendErr != nil && !errors.Is(backendErr, libdb.ErrNotFound) {
		return nil, backendErr
	}
	if configErr != nil && !errors.Is(configErr, libdb.ErrNotFound) {
		return nil, configErr
	}
	return s.statusFrom(ctx, store, defaults, backend, cfg), nil
}

func (s *service) DeleteProviderConfig(ctx context.Context, providerType string) error {
	providerType, _, err := normalizeProvider(providerType)
	if err != nil {
		return err
	}
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	return store.DeleteKV(ctx, providerConfigStorageKey(providerType))
}

func (s *service) ListProviderConfigs(ctx context.Context, createdAtCursor *time.Time, limit int) ([]*ProviderStatus, error) {
	if limit <= 0 || limit > runtimetypes.MAXLIMIT {
		limit = runtimetypes.MAXLIMIT
	}
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	seen := map[string]struct{}{}
	statuses := []*ProviderStatus{}

	kvs, err := store.ListKVPrefix(ctx, runtimestate.ProviderKeyPrefix, createdAtCursor, limit)
	if err != nil {
		return nil, err
	}
	for _, kv := range kvs {
		var cfg runtimestate.ProviderConfig
		if err := json.Unmarshal(kv.Value, &cfg); err != nil {
			continue
		}
		providerType := strings.ToLower(strings.TrimSpace(cfg.Type))
		if providerType == "" {
			providerType = strings.TrimPrefix(kv.Key, runtimestate.ProviderKeyPrefix)
		}
		providerType, defaults, err := normalizeProvider(providerType)
		if err != nil {
			continue
		}
		backend, backendErr := store.GetBackendByName(ctx, providerType)
		if backendErr != nil && !errors.Is(backendErr, libdb.ErrNotFound) {
			return nil, backendErr
		}
		status := s.statusFrom(ctx, store, defaults, backend, &cfg)
		status.UpdatedAt = kv.UpdatedAt
		statuses = append(statuses, status)
		seen[providerType] = struct{}{}
	}

	backends, err := store.ListBackends(ctx, nil, limit)
	if err != nil {
		return nil, err
	}
	for _, backend := range backends {
		providerType, defaults, err := normalizeProvider(backend.Type)
		if err != nil {
			continue
		}
		if _, ok := seen[providerType]; ok {
			continue
		}
		cfg, configErr := s.readProviderConfig(ctx, providerType)
		if configErr != nil && !errors.Is(configErr, libdb.ErrNotFound) {
			return nil, configErr
		}
		statuses = append(statuses, s.statusFrom(ctx, store, defaults, backend, cfg))
		seen[providerType] = struct{}{}
	}
	return statuses, nil
}

func (s *service) writeProviderConfig(ctx context.Context, store runtimetypes.Store, providerType string, cfg runtimestate.ProviderConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal provider config: %w", err)
	}
	if err := store.SetKV(ctx, providerConfigStorageKey(providerType), data); err != nil {
		return fmt.Errorf("store provider config: %w", err)
	}
	return nil
}

func (s *service) writeBackendCredential(ctx context.Context, store runtimetypes.Store, providerType, backendID string, cfg runtimestate.ProviderConfig) error {
	record := runtimestate.ProviderConfig{Type: providerType}
	switch {
	case strings.TrimSpace(cfg.APIKeyEnv) != "":
		record.CredentialSource = runtimestate.CredentialEnvironment
		record.APIKeyEnv = strings.TrimSpace(cfg.APIKeyEnv)
	case strings.TrimSpace(cfg.APIKey) != "":
		record.CredentialSource = runtimestate.CredentialStored
		record.APIKey = cfg.APIKey
	default:
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal backend credential: %w", err)
	}
	if err := store.SetKV(ctx, runtimestate.BackendCredentialKey(providerType, backendID), data); err != nil {
		return fmt.Errorf("store backend credential: %w", err)
	}
	return nil
}

func (s *service) readProviderConfig(ctx context.Context, providerType string) (*runtimestate.ProviderConfig, error) {
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	var cfg runtimestate.ProviderConfig
	if err := store.GetKV(ctx, providerConfigStorageKey(providerType), &cfg); err != nil {
		return nil, err
	}
	if cfg.Type == "" {
		cfg.Type = providerType
	}
	return &cfg, nil
}

func (s *service) statusFrom(ctx context.Context, store runtimetypes.Store, defaults providerDefaults, backend *runtimetypes.Backend, cfg *runtimestate.ProviderConfig) *ProviderStatus {
	status := &ProviderStatus{
		Provider:             defaults.Type,
		SecretSource:         "none",
		RecommendedAPIKeyEnv: defaults.RecommendedAPIKeyEnv,
		DefaultProvider:      clikv.Read(ctx, store, "default-provider"),
		DefaultModel:         clikv.Read(ctx, store, "default-model"),
	}
	if backend != nil {
		status.BackendID = backend.ID
		status.BackendName = backend.Name
		status.BaseURL = backend.BaseURL
		status.UpdatedAt = backend.UpdatedAt
	}
	if backend != nil {
		var record runtimestate.ProviderConfig
		if err := store.GetKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), &record); err == nil {
			cfg = &record
		}
	}
	if cfg != nil {
		switch {
		case strings.TrimSpace(cfg.APIKey) != "":
			status.SecretSource = "literal"
			status.SecretConfigured = true
			status.SecretPresent = true
		case strings.TrimSpace(cfg.APIKeyEnv) != "":
			status.SecretSource = "env"
			status.SecretConfigured = true
			status.APIKeyEnv = strings.TrimSpace(cfg.APIKeyEnv)
			status.SecretPresent = os.Getenv(status.APIKeyEnv) != ""
		}
	}
	hasBackend := backend != nil
	status.Configured = hasBackend && (!defaults.RequiresSecretConfig || status.SecretConfigured)
	if defaults.RequiresLogin && backend != nil {
		if authState, err := modelauth.New(s.dbInstance).Status(ctx, backend.ID); err == nil {
			status.AuthState = authState.State
			status.LoginRequired = authState.State == "login_required" || authState.State == "refresh_required"
		}
	}
	return status
}

func providerConfigStorageKey(providerType string) string {
	switch providerType {
	case ProviderTypeVLLM:
		return runtimestate.OpenaiKey
	default:
		return runtimestate.ProviderKeyPrefix + providerType
	}
}

func normalizeProvider(providerType string) (string, providerDefaults, error) {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "vertex" {
		providerType = ProviderTypeVertexGoogle
	}
	defaults, ok := providerDefaultsByType[providerType]
	if !ok {
		return "", providerDefaults{}, fmt.Errorf("%w: %s", ErrInvalidProvider, providerType)
	}
	return providerType, defaults, nil
}

type loginAttempt struct {
	provider string
	device   modelauth.DeviceCode
	done     bool
	err      error
	codeCh   chan modelauth.DeviceCode
	doneCh   chan struct{}
	cancel   context.CancelFunc
}

var (
	loginMu       sync.Mutex
	loginAttempts = map[string]*loginAttempt{}
)

func (s *service) StartLogin(ctx context.Context, providerType string) (*LoginStart, error) {
	providerType, defaults, err := normalizeProvider(providerType)
	if err != nil {
		return nil, err
	}
	if !defaults.RequiresLogin {
		return nil, fmt.Errorf("%w: %s does not use a subscription sign-in", ErrInvalidProvider, providerType)
	}

	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	backend, err := store.GetBackendByName(ctx, providerType)
	if errors.Is(err, libdb.ErrNotFound) {
		if _, err := s.Configure(ctx, providerType, ConfigureProviderRequest{Upsert: true, SetDefault: false}); err != nil {
			return nil, err
		}
		backend, err = store.GetBackendByName(ctx, providerType)
	}
	if err != nil {
		return nil, fmt.Errorf("read backend %q: %w", providerType, err)
	}

	auth := modelauth.New(s.dbInstance)
	if state, err := auth.Status(ctx, backend.ID); err == nil && state.State == "authenticated" {
		return &LoginStart{Provider: providerType, State: state.State, ExpiresAt: state.ExpiresAt}, nil
	}

	loginMu.Lock()
	if attempt := loginAttempts[backend.ID]; attempt != nil && !attempt.done {
		device := attempt.device
		loginMu.Unlock()
		if device.Code != "" {
			return &LoginStart{Provider: providerType, State: "login_pending", URL: device.URL, Code: device.Code, ExpiresAt: device.ExpiresAt}, nil
		}
		return waitLoginStart(ctx, providerType, attempt)
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	attempt := &loginAttempt{
		provider: providerType,
		codeCh:   make(chan modelauth.DeviceCode, 1),
		doneCh:   make(chan struct{}),
		cancel:   cancel,
	}
	loginAttempts[backend.ID] = attempt
	loginMu.Unlock()

	go func() {
		err := auth.Login(loginCtx, backend.ID, func(code modelauth.DeviceCode) error {
			loginMu.Lock()
			attempt.device = code
			loginMu.Unlock()
			select {
			case attempt.codeCh <- code:
			default:
			}
			return nil
		})
		loginMu.Lock()
		attempt.err = err
		attempt.done = true
		loginMu.Unlock()
		close(attempt.doneCh)
		if err == nil {
			s.invalidateBackendCache(backend.ID)
		}
	}()

	return waitLoginStart(ctx, providerType, attempt)
}

func waitLoginStart(ctx context.Context, providerType string, attempt *loginAttempt) (*LoginStart, error) {
	select {
	case code := <-attempt.codeCh:
		return &LoginStart{Provider: providerType, State: "login_pending", URL: code.URL, Code: code.Code, ExpiresAt: code.ExpiresAt}, nil
	case <-attempt.doneCh:
		loginMu.Lock()
		err := attempt.err
		loginMu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("start sign-in: %w", err)
		}
		return &LoginStart{Provider: providerType, State: "authenticated"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%w: timed out waiting for the device code", ErrInvalidProvider)
	}
}

func (s *service) LoginStatus(ctx context.Context, providerType string) (*LoginStatus, error) {
	providerType, defaults, err := normalizeProvider(providerType)
	if err != nil {
		return nil, err
	}
	if !defaults.RequiresLogin {
		return nil, fmt.Errorf("%w: %s does not use a subscription sign-in", ErrInvalidProvider, providerType)
	}
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	backend, err := store.GetBackendByName(ctx, providerType)
	if errors.Is(err, libdb.ErrNotFound) {
		return &LoginStatus{Provider: providerType, State: "not_configured"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read backend %q: %w", providerType, err)
	}
	state, err := modelauth.New(s.dbInstance).Status(ctx, backend.ID)
	if err != nil {
		return nil, err
	}
	out := &LoginStatus{Provider: providerType, State: state.State, ExpiresAt: state.ExpiresAt}

	loginMu.Lock()
	if attempt := loginAttempts[backend.ID]; attempt != nil {
		if !attempt.done && attempt.device.Code != "" && out.State != "authenticated" {
			out.State = "login_pending"
			out.URL = attempt.device.URL
			out.Code = attempt.device.Code
			out.ExpiresAt = attempt.device.ExpiresAt
		}
		if attempt.done && attempt.err != nil {
			out.Error = attempt.err.Error()
		}
	}
	loginMu.Unlock()
	return out, nil
}

func (s *service) Logout(ctx context.Context, providerType string) error {
	providerType, defaults, err := normalizeProvider(providerType)
	if err != nil {
		return err
	}
	if !defaults.RequiresLogin {
		return fmt.Errorf("%w: %s does not use a subscription sign-in", ErrInvalidProvider, providerType)
	}
	store := runtimetypes.New(s.dbInstance.WithoutTransaction())
	backend, err := store.GetBackendByName(ctx, providerType)
	if err != nil {
		return fmt.Errorf("read backend %q: %w", providerType, err)
	}

	loginMu.Lock()
	if attempt := loginAttempts[backend.ID]; attempt != nil {
		attempt.cancel()
		delete(loginAttempts, backend.ID)
	}
	loginMu.Unlock()

	if err := modelauth.New(s.dbInstance).Logout(ctx, backend.ID); err != nil {
		return err
	}
	s.invalidateBackendCache(backend.ID)
	return nil
}

func (s *service) invalidateBackendCache(backendID string) {
	_ = runtimestate.InvalidateModelCache(context.Background(), libkvstore.NewSQLiteManager(s.dbInstance), backendID)
}
