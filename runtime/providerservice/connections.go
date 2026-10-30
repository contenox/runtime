package providerservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

// Connection config states.
const (
	ConfigMissing    = "missing"
	ConfigConfigured = "configured"
	ConfigInvalid    = "invalid"
)

// Connection auth states. login_pending is reported while a device-code
// sign-in is in flight; the rest mirror the modelauth states.
const (
	AuthNotRequired     = "not_required"
	AuthAuthenticated   = "authenticated"
	AuthLoginRequired   = "login_required"
	AuthRefreshRequired = "refresh_required"
	AuthLoginPending    = "login_pending"
)

// Connection health states.
const (
	HealthUnknown     = "unknown"
	HealthChecking    = "checking"
	HealthAvailable   = "available"
	HealthUnavailable = "unavailable"
)

const maxConnectionNameLen = 128

const checkTimeout = 30 * time.Second

// Connection is a configured instance of a provider: one runtime backend plus
// its credential and sign-in. Secrets are never part of it.
type Connection struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	ProviderType     string    `json:"providerType"`
	BaseURL          string    `json:"baseUrl"`
	ConfigState      string    `json:"configState"`
	AuthState        string    `json:"authState"`
	Health           string    `json:"health"`
	ErrorCode        string    `json:"errorCode,omitempty"`
	Error            string    `json:"error,omitempty"`
	SecretSource     string    `json:"secretSource"`
	SecretConfigured bool      `json:"secretConfigured"`
	SecretPresent    bool      `json:"secretPresent"`
	APIKeyEnv        string    `json:"apiKeyEnv,omitempty"`
	ModelCount       int       `json:"modelCount"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type CreateConnectionRequest struct {
	Name      string
	APIKey    string
	APIKeyEnv string
	BaseURL   string
}

// UpdateConnectionRequest changes only the fields that are non-nil. Setting
// APIKey replaces the stored credential with a literal key, setting APIKeyEnv
// replaces it with an environment reference, ClearSecret removes it.
type UpdateConnectionRequest struct {
	Name        *string
	BaseURL     *string
	APIKey      *string
	APIKeyEnv   *string
	ClearSecret bool
}

// validateConnectionInput applies the rules shared by Configure and
// CreateConnection and returns the base URL the backend should carry.
func validateConnectionInput(providerType string, defaults providerDefaults, apiKey, apiKeyEnv, baseURL string) (string, error) {
	if apiKey != "" && apiKeyEnv != "" {
		return "", fmt.Errorf("%w: provide apiKey or apiKeyEnv, not both", ErrInvalidProvider)
	}
	if baseURL == "" {
		baseURL = defaults.DefaultBaseURL
	}
	if defaults.RequiresBaseURL && baseURL == "" {
		return "", fmt.Errorf("%w: baseUrl is required for %s", ErrInvalidProvider, providerType)
	}
	if defaults.RequiresLogin {
		if apiKey != "" || apiKeyEnv != "" {
			return "", fmt.Errorf("%w: %s signs in with a device code; an API key does not apply", ErrInvalidProvider, providerType)
		}
		baseURL = defaults.DefaultBaseURL
	}
	return baseURL, nil
}

func validateConnectionName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: name must not be empty", ErrInvalidProvider)
	case len(name) > maxConnectionNameLen:
		return fmt.Errorf("%w: name must be at most %d characters", ErrInvalidProvider, maxConnectionNameLen)
	case strings.HasPrefix(name, "connection:"):
		return fmt.Errorf("%w: name must not start with %q", ErrInvalidProvider, "connection:")
	}
	return nil
}

// nextConnectionName returns providerType, then providerType-2, -3, ... : the
// first one not in taken.
func nextConnectionName(providerType string, taken map[string]struct{}) string {
	if _, used := taken[providerType]; !used {
		return providerType
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s-%d", providerType, n)
		if _, used := taken[name]; !used {
			return name
		}
	}
}

func (s *service) store() runtimetypes.Store {
	return runtimetypes.New(s.dbInstance.WithoutTransaction())
}

func (s *service) ListConnections(ctx context.Context) ([]*Connection, error) {
	store := s.store()
	backends, err := store.ListAllBackends(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(backends, func(i, j int) bool {
		if !backends[i].CreatedAt.Equal(backends[j].CreatedAt) {
			return backends[i].CreatedAt.Before(backends[j].CreatedAt)
		}
		return backends[i].Name < backends[j].Name
	})
	snapshot := s.runtimeSnapshot(ctx)
	out := make([]*Connection, 0, len(backends))
	for _, backend := range backends {
		conn, err := s.connectionFrom(ctx, store, backend, snapshot)
		if err != nil {
			return nil, err
		}
		out = append(out, conn)
	}
	return out, nil
}

func (s *service) GetConnection(ctx context.Context, id string) (*Connection, error) {
	store := s.store()
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.connectionFrom(ctx, store, backend, s.runtimeSnapshot(ctx))
}

func (s *service) CreateConnection(ctx context.Context, providerType string, req CreateConnectionRequest) (*Connection, error) {
	providerType, defaults, err := normalizeProvider(providerType)
	if err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.APIKeyEnv = strings.TrimSpace(req.APIKeyEnv)
	req.BaseURL = strings.TrimSpace(req.BaseURL)

	baseURL, err := validateConnectionInput(providerType, defaults, req.APIKey, req.APIKeyEnv, req.BaseURL)
	if err != nil {
		return nil, err
	}
	if defaults.RequiresSecretConfig && req.APIKey == "" && req.APIKeyEnv == "" {
		return nil, fmt.Errorf("%w: apiKey or apiKeyEnv is required for %s", ErrInvalidProvider, providerType)
	}

	store := s.store()
	existing, err := store.ListAllBackends(ctx)
	if err != nil {
		return nil, err
	}
	taken := make(map[string]struct{}, len(existing))
	for _, backend := range existing {
		taken[backend.Name] = struct{}{}
	}
	name := req.Name
	if name == "" {
		name = nextConnectionName(providerType, taken)
	} else {
		if err := validateConnectionName(name); err != nil {
			return nil, err
		}
		if _, used := taken[name]; used {
			return nil, fmt.Errorf("connection %q already exists: %w", name, libdb.ErrUniqueViolation)
		}
	}

	backend := &runtimetypes.Backend{ID: uuid.NewString(), Name: name, Type: providerType, BaseURL: baseURL}
	if err := s.saveBackend(ctx, store, backend, true); err != nil {
		return nil, fmt.Errorf("create backend: %w", err)
	}
	if err := s.writeSecret(ctx, store, backend, req.APIKey, req.APIKeyEnv); err != nil {
		_ = store.DeleteBackend(ctx, backend.ID)
		return nil, err
	}
	s.afterChange(backend.ID)
	return s.GetConnection(ctx, backend.ID)
}

func (s *service) UpdateConnection(ctx context.Context, id string, req UpdateConnectionRequest) (*Connection, error) {
	store := s.store()
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	providerType, defaults, normErr := normalizeProvider(backend.Type)
	if normErr != nil {
		providerType, defaults = backend.Type, providerDefaults{Type: backend.Type}
	}

	apiKey, apiKeyEnv := trimmed(req.APIKey), trimmed(req.APIKeyEnv)
	if apiKey != "" && apiKeyEnv != "" {
		return nil, fmt.Errorf("%w: provide apiKey or apiKeyEnv, not both", ErrInvalidProvider)
	}
	if req.ClearSecret && (apiKey != "" || apiKeyEnv != "") {
		return nil, fmt.Errorf("%w: clearSecret cannot be combined with apiKey or apiKeyEnv", ErrInvalidProvider)
	}
	if defaults.RequiresLogin && (apiKey != "" || apiKeyEnv != "") {
		return nil, fmt.Errorf("%w: %s signs in with a device code; an API key does not apply", ErrInvalidProvider, providerType)
	}

	updated := *backend
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if err := validateConnectionName(name); err != nil {
			return nil, err
		}
		if name != backend.Name {
			if other, err := store.GetBackendByName(ctx, name); err == nil && other.ID != backend.ID {
				return nil, fmt.Errorf("connection %q already exists: %w", name, libdb.ErrUniqueViolation)
			} else if err != nil && !errors.Is(err, libdb.ErrNotFound) {
				return nil, err
			}
			updated.Name = name
		}
	}
	if req.BaseURL != nil {
		baseURL := strings.TrimSpace(*req.BaseURL)
		switch {
		case defaults.RequiresLogin && baseURL != "" && baseURL != defaults.DefaultBaseURL:
			return nil, fmt.Errorf("%w: %s has a fixed base URL", ErrInvalidProvider, providerType)
		case baseURL == "":
			baseURL = defaults.DefaultBaseURL
			if baseURL == "" {
				return nil, fmt.Errorf("%w: baseUrl is required for %s", ErrInvalidProvider, providerType)
			}
		}
		updated.BaseURL = baseURL
	}

	if updated.Name != backend.Name || updated.BaseURL != backend.BaseURL {
		if err := s.saveBackend(ctx, store, &updated, false); err != nil {
			return nil, fmt.Errorf("update backend: %w", err)
		}
	}
	switch {
	case req.ClearSecret:
		if err := store.DeleteKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID)); err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return nil, fmt.Errorf("clear credential: %w", err)
		}
	case apiKey != "" || apiKeyEnv != "":
		if err := s.writeSecret(ctx, store, backend, apiKey, apiKeyEnv); err != nil {
			return nil, err
		}
	}
	s.afterChange(backend.ID)
	return s.GetConnection(ctx, backend.ID)
}

func (s *service) DeleteConnection(ctx context.Context, id string) error {
	store := s.store()
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return err
	}
	cancelLoginAttempt(backend.ID)
	if err := backendservice.New(s.dbInstance).Delete(ctx, backend.ID); err != nil {
		return err
	}
	s.invalidateBackendCache(backend.ID)
	s.afterChange(backend.ID)
	return nil
}

// CheckConnection re-probes the connection now: it drops the cached model list
// and runs a discovery cycle for just this backend, waiting for it to finish
// (bounded by checkTimeout), then returns the connection with the fresh state.
// Without a runtime state it only returns the connection as it is.
func (s *service) CheckConnection(ctx context.Context, id string) (*Connection, error) {
	store := s.store()
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.state != nil {
		s.beginCheck(backend.ID)
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		err := s.state.RefreshProviderModels(cctx, "connection:"+backend.ID)
		cancel()
		s.endCheck(backend.ID)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return s.GetConnection(ctx, backend.ID)
}

func (s *service) StartConnectionLogin(ctx context.Context, id string) (*LoginStart, error) {
	backend, providerType, err := s.loginBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.startLogin(ctx, providerType, backend)
}

func (s *service) ConnectionLoginStatus(ctx context.Context, id string) (*LoginStatus, error) {
	backend, providerType, err := s.loginBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.loginStatus(ctx, providerType, backend)
}

func (s *service) CancelConnectionLogin(ctx context.Context, id string) (*LoginStatus, error) {
	backend, providerType, err := s.loginBackend(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.cancelLogin(ctx, providerType, backend)
}

func (s *service) LogoutConnection(ctx context.Context, id string) error {
	backend, _, err := s.loginBackend(ctx, id)
	if err != nil {
		return err
	}
	return s.logout(ctx, backend)
}

func (s *service) loginBackend(ctx context.Context, id string) (*runtimetypes.Backend, string, error) {
	backend, err := s.store().GetBackend(ctx, id)
	if err != nil {
		return nil, "", err
	}
	providerType, defaults, err := normalizeProvider(backend.Type)
	if err != nil {
		return nil, "", err
	}
	if !defaults.RequiresLogin {
		return nil, "", fmt.Errorf("%w: %s does not use a subscription sign-in", ErrInvalidProvider, providerType)
	}
	return backend, providerType, nil
}

// writeSecret stores the connection's own credential record under the
// per-backend key, replacing whatever source it had before.
func (s *service) writeSecret(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend, apiKey, apiKeyEnv string) error {
	return s.writeBackendCredential(ctx, store, backend.Type, backend.ID, runtimestate.ProviderConfig{APIKey: apiKey, APIKeyEnv: apiKeyEnv})
}

// afterChange drops cached discovery for the backend and, when a runtime state
// is attached, refreshes it in the background so health catches up.
func (s *service) afterChange(backendID string) {
	s.invalidateBackendCache(backendID)
	if s.state == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		_ = s.state.ScheduleProviderRefresh(ctx, "connection:"+backendID)
	}()
}

func (s *service) beginCheck(id string) {
	s.checkMu.Lock()
	s.checking[id]++
	s.checkMu.Unlock()
}

func (s *service) endCheck(id string) {
	s.checkMu.Lock()
	if s.checking[id] <= 1 {
		delete(s.checking, id)
	} else {
		s.checking[id]--
	}
	s.checkMu.Unlock()
}

func (s *service) isChecking(id string) bool {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	return s.checking[id] > 0
}

func (s *service) runtimeSnapshot(ctx context.Context) map[string]runtimestate.BackendRuntimeState {
	if s.state == nil {
		return nil
	}
	return s.state.Get(ctx)
}

// readCredential resolves the credential record shown for a connection: its
// own per-backend record first, then the legacy per-provider-type row (which
// the runtime adopts into backends that lack their own at startup).
func (s *service) readCredential(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend) (*runtimestate.ProviderConfig, error) {
	var record runtimestate.ProviderConfig
	err := store.GetKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), &record)
	if err == nil {
		return &record, nil
	}
	if !errors.Is(err, libdb.ErrNotFound) {
		return nil, err
	}
	var legacy runtimestate.ProviderConfig
	err = store.GetKV(ctx, providerConfigStorageKey(strings.ToLower(backend.Type)), &legacy)
	if errors.Is(err, libdb.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &legacy, nil
}

func (s *service) connectionFrom(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend, snapshot map[string]runtimestate.BackendRuntimeState) (*Connection, error) {
	providerType, defaults, normErr := normalizeProvider(backend.Type)
	if normErr != nil {
		providerType, defaults = strings.ToLower(backend.Type), providerDefaults{Type: strings.ToLower(backend.Type)}
	}
	conn := &Connection{
		ID:           backend.ID,
		Name:         backend.Name,
		ProviderType: providerType,
		BaseURL:      backend.BaseURL,
		SecretSource: "none",
		AuthState:    AuthNotRequired,
		Health:       HealthUnknown,
		CreatedAt:    backend.CreatedAt,
		UpdatedAt:    backend.UpdatedAt,
	}

	cfg, err := s.readCredential(ctx, store, backend)
	credentialUnreadable := err != nil
	if cfg != nil {
		switch {
		case strings.TrimSpace(cfg.APIKey) != "":
			conn.SecretSource = "literal"
			conn.SecretConfigured, conn.SecretPresent = true, true
		case strings.TrimSpace(cfg.APIKeyEnv) != "":
			conn.SecretSource = "env"
			conn.SecretConfigured = true
			conn.APIKeyEnv = strings.TrimSpace(cfg.APIKeyEnv)
			conn.SecretPresent = os.Getenv(conn.APIKeyEnv) != ""
		}
	}

	var configProblem string
	switch {
	case credentialUnreadable:
		conn.ConfigState, configProblem = ConfigInvalid, "the stored credential could not be read"
	case defaults.RequiresBaseURL && strings.TrimSpace(backend.BaseURL) == "":
		conn.ConfigState, configProblem = ConfigMissing, "base URL is required"
	case defaults.RequiresSecretConfig && !conn.SecretConfigured:
		conn.ConfigState, configProblem = ConfigMissing, "API key not configured"
	case conn.SecretSource == "env" && !conn.SecretPresent:
		conn.ConfigState = ConfigInvalid
		configProblem = (&runtimestate.EnvironmentUnsetError{Variable: conn.APIKeyEnv}).Error()
	default:
		conn.ConfigState = ConfigConfigured
	}

	if defaults.RequiresLogin {
		status, err := modelauth.New(s.dbInstance).Status(ctx, backend.ID)
		if err != nil {
			return nil, err
		}
		conn.AuthState = status.State
		loginMu.Lock()
		if attempt := loginAttempts[backend.ID]; attempt != nil && !attempt.done && status.State != AuthAuthenticated {
			conn.AuthState = AuthLoginPending
		}
		loginMu.Unlock()
	}

	if rt, ok := snapshot[backend.ID]; ok {
		conn.ModelCount = len(rt.Models)
		switch {
		case rt.Error != "":
			conn.Health = HealthUnavailable
			conn.Error, conn.ErrorCode = rt.Error, rt.ErrorCode
		case len(rt.Models) == 0:
			conn.Health = HealthUnavailable
			conn.Error, conn.ErrorCode = "no models found", runtimestate.ErrorCodeNoModels
		default:
			conn.Health = HealthAvailable
		}
	}
	if s.isChecking(backend.ID) {
		conn.Health = HealthChecking
	}

	if conn.ErrorCode == "" || conn.Health != HealthUnavailable {
		switch {
		case conn.AuthState == AuthLoginRequired:
			conn.ErrorCode, conn.Error = runtimestate.ErrorCodeLoginRequired, "sign-in required"
		case conn.ConfigState != ConfigConfigured && conn.Health != HealthAvailable:
			conn.ErrorCode, conn.Error = runtimestate.ErrorCodeMisconfigured, configProblem
		}
	}
	return conn, nil
}

func trimmed(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// saveBackend validates and writes a connection's backend row. Connections of
// one provider may share a base URL (each has its own credential), so it goes
// through the store rather than backendservice's type-and-URL uniqueness rule.
func (s *service) saveBackend(ctx context.Context, store runtimetypes.Store, backend *runtimetypes.Backend, create bool) error {
	if err := backendservice.Validate(backend); err != nil {
		return err
	}
	if !create {
		return store.UpdateBackend(ctx, backend)
	}
	count, err := store.EstimateBackendCount(ctx)
	if err != nil {
		return err
	}
	if err := store.EnforceMaxRowCount(ctx, count); err != nil {
		return fmt.Errorf("too many rows in the system (current %d): %w", count, err)
	}
	return store.CreateBackend(ctx, backend)
}
