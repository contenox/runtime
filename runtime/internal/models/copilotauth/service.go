package copilotauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const ProviderType = "github-copilot"

const BaseURL = "https://api.githubcopilot.com"

const (
	clientID          = "Iv1.b507a08c87ecfe98"
	githubURL         = "https://github.com"
	githubAPIURL      = "https://api.github.com"
	editorVersion     = "vscode/1.99.3"
	pluginVersion     = "copilot-chat/0.26.7"
	userAgent         = "GitHubCopilotChat/0.26.7"
	integrationID     = "vscode-chat"
	refreshBeforeExp  = 5 * time.Minute
	loginTimeout      = 15 * time.Minute
	maxResponseBytes  = 1 << 20
	revisionKeyPrefix = "copilot-oauth-revision:"
)

var ErrLoginRequired = errors.New("GitHub Copilot sign-in required; sign in again under Backends")

var ErrNotAllowed = errors.New("GitHub Copilot sign-in is an unofficial sign-in; allow unofficial sign-ins in the settings to use it")

type credential struct {
	GitHubToken  string    `json:"githubToken,omitempty"`
	CopilotToken string    `json:"copilotToken,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	APIBase      string    `json:"apiBase,omitempty"`
	Generation   string    `json:"generation"`
}

type Service struct {
	db        libdb.DBManager
	client    *http.Client
	githubURL string
	apiURL    string
}

func New(db libdb.DBManager) *Service {
	return &Service{
		db:        db,
		client:    &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		githubURL: githubURL,
		apiURL:    githubAPIURL,
	}
}

func key(id string) string { return "copilot-oauth:" + id }

func read(ctx context.Context, store runtimetypes.Store, id string) (credential, error) {
	var c credential
	err := store.GetKV(ctx, key(id), &c)
	if errors.Is(err, libdb.ErrNotFound) {
		err = nil
	}
	return c, err
}

func (s *Service) allowed(ctx context.Context) error {
	if !clikv.UnofficialSignIns(ctx, runtimetypes.New(s.db.WithoutTransaction())) {
		return ErrNotAllowed
	}
	return nil
}

func (s *Service) mutate(ctx context.Context, id string, fn func(*credential) error) error {
	exec, commit, release, err := s.db.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	store := runtimetypes.New(exec)
	if err := store.LockBackend(ctx, id); err != nil {
		return err
	}
	b, err := store.GetBackend(ctx, id)
	if err != nil {
		return err
	}
	if !strings.EqualFold(b.Type, ProviderType) {
		return fmt.Errorf("backend is not a GitHub Copilot backend")
	}
	c, err := read(ctx, store, id)
	if err != nil {
		return err
	}
	previous := c
	if err := fn(&c); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode Copilot credentials")
	}
	if err := store.SetKV(ctx, key(id), raw); err != nil {
		return err
	}
	if c.Generation != previous.Generation && (c.GitHubToken != previous.GitHubToken || c.CopilotToken != previous.CopilotToken) {
		revision, err := json.Marshal(uuid.NewString())
		if err != nil {
			return err
		}
		if err := store.SetKV(ctx, revisionKeyPrefix+id, revision); err != nil {
			return err
		}
	}
	return commit(ctx)
}

func (s *Service) Status(ctx context.Context, id string) (modelauth.Status, error) {
	c, err := read(ctx, runtimetypes.New(s.db.WithoutTransaction()), id)
	if err != nil {
		return modelauth.Status{}, err
	}
	status := modelauth.Status{State: "login_required", Generation: c.Generation}
	if c.GitHubToken != "" {
		status.State = "authenticated"
		status.ExpiresAt = c.Expiry
		if c.CopilotToken == "" || time.Until(c.Expiry) < refreshBeforeExp {
			status.State = "refresh_required"
		}
	}
	return status, nil
}

func CredentialGeneration(ctx context.Context, store runtimetypes.Store, id string) (string, error) {
	if store == nil || id == "" {
		return "", errors.New("credential generation requires a store and connection ID")
	}
	var revision string
	err := store.GetKV(ctx, revisionKeyPrefix+id, &revision)
	if errors.Is(err, libdb.ErrNotFound) {
		return "", nil
	}
	return revision, err
}

func (s *Service) Logout(ctx context.Context, id string) error {
	return s.mutate(ctx, id, func(c *credential) error { *c = credential{Generation: uuid.NewString()}; return nil })
}

func DeleteCredentials(ctx context.Context, exec libdb.Exec, id string) error {
	store := runtimetypes.New(exec)
	for _, k := range []string{key(id), revisionKeyPrefix + id} {
		if err := store.DeleteKV(ctx, k); err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return err
		}
	}
	return nil
}

func editorHeaders(h http.Header) {
	h.Set("Editor-Version", editorVersion)
	h.Set("Editor-Plugin-Version", pluginVersion)
	h.Set("User-Agent", userAgent)
	h.Set("Copilot-Integration-Id", integrationID)
}

func (s *Service) Headers(ctx context.Context, id string) (http.Header, error) {
	if err := s.allowed(ctx); err != nil {
		return nil, err
	}
	c, err := read(ctx, runtimetypes.New(s.db.WithoutTransaction()), id)
	if err != nil {
		return nil, err
	}
	if c.GitHubToken == "" {
		return nil, ErrLoginRequired
	}
	if c.CopilotToken == "" || time.Until(c.Expiry) < refreshBeforeExp {
		var refreshErr error
		err = s.mutate(ctx, id, func(current *credential) error {
			if current.GitHubToken == "" {
				return ErrLoginRequired
			}
			if current.CopilotToken != "" && time.Until(current.Expiry) >= refreshBeforeExp {
				c = *current
				return nil
			}
			token, expiry, api, err := s.exchange(ctx, current.GitHubToken)
			if errors.Is(err, ErrLoginRequired) {
				*current = credential{Generation: uuid.NewString()}
				refreshErr = err
				return nil
			}
			if err != nil {
				return err
			}
			current.CopilotToken, current.Expiry, current.APIBase = token, expiry, api
			c = *current
			return nil
		})
		if err != nil {
			return nil, err
		}
		if refreshErr != nil {
			return nil, refreshErr
		}
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.CopilotToken)
	editorHeaders(h)
	h.Set("Openai-Intent", "conversation-panel")
	return h, nil
}

func (s *Service) Login(ctx context.Context, id string, show func(modelauth.DeviceCode) error) error {
	if err := s.allowed(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	generation := uuid.NewString()
	if err := s.mutate(ctx, id, func(c *credential) error { c.Generation = generation; return nil }); err != nil {
		return err
	}
	var device struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	status, err := s.form(ctx, s.githubURL+"/login/device/code", url.Values{"client_id": {clientID}, "scope": {"read:user"}}, &device)
	if err != nil {
		return err
	}
	if status != http.StatusOK || device.DeviceCode == "" || device.UserCode == "" {
		return fmt.Errorf("GitHub device sign-in could not start (HTTP %d)", status)
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	verification := device.VerificationURI
	if verification == "" {
		verification = s.githubURL + "/login/device"
	}
	deadline, _ := ctx.Deadline()
	if device.ExpiresIn > 0 {
		if d := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second); d.Before(deadline) {
			deadline = d
		}
	}
	if err := show(modelauth.DeviceCode{URL: verification, Code: device.UserCode, ExpiresAt: deadline}); err != nil {
		return err
	}
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		var grant struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		status, err := s.form(ctx, s.githubURL+"/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {device.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &grant)
		if err != nil {
			return err
		}
		switch grant.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return fmt.Errorf("the GitHub sign-in code expired; start sign-in again")
		case "access_denied":
			return fmt.Errorf("GitHub sign-in was declined")
		}
		if status != http.StatusOK || grant.AccessToken == "" {
			return fmt.Errorf("GitHub sign-in failed (HTTP %d %s); start sign-in again", status, grant.Error)
		}
		token, expiry, api, err := s.exchange(ctx, grant.AccessToken)
		if err != nil {
			return err
		}
		return s.mutate(ctx, id, func(c *credential) error {
			if c.Generation != generation {
				return fmt.Errorf("sign-in superseded by another sign-in or sign-out; retry")
			}
			*c = credential{GitHubToken: grant.AccessToken, CopilotToken: token, Expiry: expiry, APIBase: api, Generation: uuid.NewString()}
			return nil
		})
	}
}

func (s *Service) form(ctx context.Context, endpoint string, values url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return 0, fmt.Errorf("invalid GitHub sign-in endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GitHub sign-in request failed; check connectivity or retry")
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("invalid GitHub sign-in response (HTTP %d)", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func (s *Service) exchange(ctx context.Context, githubToken string) (string, time.Time, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+"/copilot_internal/v2/token", nil)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("invalid Copilot token endpoint")
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")
	editorHeaders(req.Header)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("Copilot token request failed; check connectivity or retry")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return "", time.Time{}, "", ErrLoginRequired
	case http.StatusForbidden, http.StatusNotFound:
		return "", time.Time{}, "", fmt.Errorf("this GitHub account has no active Copilot plan, or its organization blocks Copilot here (HTTP %d)", resp.StatusCode)
	default:
		return "", time.Time{}, "", fmt.Errorf("Copilot token request failed (HTTP %d)", resp.StatusCode)
	}
	var wire struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
		Endpoints struct {
			API string `json:"api"`
		} `json:"endpoints"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&wire) != nil || wire.Token == "" || wire.ExpiresAt <= 0 {
		return "", time.Time{}, "", fmt.Errorf("invalid Copilot token response")
	}
	api := strings.TrimRight(wire.Endpoints.API, "/")
	if api == "" {
		api = BaseURL
	}
	return wire.Token, time.Unix(wire.ExpiresAt, 0), api, nil
}
