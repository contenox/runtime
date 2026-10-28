package modelauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const ProviderType = "openai-codex"

const BaseURL = "https://chatgpt.com/backend-api/codex"

const clientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const authURL = "https://auth.openai.com"

var ErrLoginRequired = errors.New("ChatGPT login required; run contenox-runtime backend login <name>")

type DeviceCode struct {
	URL       string
	Code      string
	ExpiresAt time.Time
}

type Status struct {
	State      string    `json:"state"`
	ExpiresAt  time.Time `json:"expiresAt,omitempty"`
	Generation string    `json:"-"`
}

type credential struct {
	Token      *oauth2.Token `json:"token,omitempty"`
	Account    string        `json:"account,omitempty"`
	Generation string        `json:"generation"`
}

type Service struct {
	db      libdb.DBManager
	client  *http.Client
	authURL string
}

func New(db libdb.DBManager) *Service {
	return &Service{db: db, client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, authURL: authURL}
}

func key(id string) string { return "model-oauth:" + id }

func read(ctx context.Context, store runtimetypes.Store, id string) (credential, error) {
	var c credential
	err := store.GetKV(ctx, key(id), &c)
	if errors.Is(err, libdb.ErrNotFound) {
		err = nil
	}
	return c, err
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
	if !strings.EqualFold(b.Type, ProviderType) || b.BaseURL != BaseURL {
		return fmt.Errorf("backend is not a ChatGPT subscription backend")
	}
	c, err := read(ctx, store, id)
	if err != nil {
		return err
	}
	previousToken, previousAccount, previousGeneration := c.Token, c.Account, c.Generation
	if err := fn(&c); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode model credentials")
	}
	if err := store.SetKV(ctx, key(id), raw); err != nil {
		return err
	}
	if c.Generation != previousGeneration && (c.Token != previousToken || c.Account != previousAccount) {
		revision, err := json.Marshal(uuid.NewString())
		if err != nil {
			return err
		}
		if err := store.SetKV(ctx, "model-oauth-revision:"+id, revision); err != nil {
			return err
		}
	}
	return commit(ctx)
}

func (s *Service) Status(ctx context.Context, id string) (Status, error) {
	c, err := read(ctx, runtimetypes.New(s.db.WithoutTransaction()), id)
	if err != nil {
		return Status{}, err
	}
	status := Status{State: "login_required", Generation: c.Generation}
	if c.Token != nil && c.Token.AccessToken != "" {
		status.State = "authenticated"
		status.ExpiresAt = c.Token.Expiry
		if time.Until(c.Token.Expiry) < 5*time.Minute {
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
	err := store.GetKV(ctx, "model-oauth-revision:"+id, &revision)
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
	for _, credentialKey := range []string{key(id), "model-oauth-revision:" + id} {
		if err := store.DeleteKV(ctx, credentialKey); err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Service) Headers(ctx context.Context, id string) (http.Header, error) {
	c, err := read(ctx, runtimetypes.New(s.db.WithoutTransaction()), id)
	if err != nil {
		return nil, err
	}
	if c.Token == nil {
		return nil, ErrLoginRequired
	}
	if time.Until(c.Token.Expiry) < 5*time.Minute {
		var refreshErr error
		var attempted *credential
		err = s.mutate(ctx, id, func(current *credential) error {
			if current.Token == nil {
				return ErrLoginRequired
			}
			if time.Until(current.Token.Expiry) >= 5*time.Minute {
				c = *current
				return nil
			}
			prior := *current
			attempted = &prior
			t, account, err := s.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current.Token.RefreshToken}, "client_id": {clientID}})
			if err != nil {
				*current = credential{Generation: uuid.NewString()}
				refreshErr = ErrLoginRequired
				return nil
			}
			if t.RefreshToken == "" {
				t.RefreshToken = current.Token.RefreshToken
			}
			if account != current.Account {
				*current = credential{Generation: uuid.NewString()}
				refreshErr = ErrLoginRequired
				return nil
			}
			current.Token = t
			c = *current
			return nil
		})
		if err != nil {
			if attempted != nil {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				cleanupErr := s.mutate(cleanupCtx, id, func(current *credential) error {
					if current.Generation == attempted.Generation && current.Token != nil && current.Token.AccessToken == attempted.Token.AccessToken && current.Token.Expiry.Equal(attempted.Token.Expiry) {
						*current = credential{Generation: uuid.NewString()}
					}
					return nil
				})
				if cleanupErr != nil {
					return nil, fmt.Errorf("%w; could not clear an uncertain refresh", ErrLoginRequired)
				}
				return nil, fmt.Errorf("%w: %w", ErrLoginRequired, err)
			}
			return nil, err
		}
		if refreshErr != nil {
			return nil, refreshErr
		}
	}
	return http.Header{"Authorization": {"Bearer " + c.Token.AccessToken}, "Chatgpt-Account-Id": {c.Account}}, nil
}

func (s *Service) Login(ctx context.Context, id string, show func(DeviceCode) error) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	generation := uuid.NewString()
	if err := s.mutate(ctx, id, func(c *credential) error { c.Generation = generation; return nil }); err != nil {
		return err
	}
	var device struct {
		ID       string          `json:"device_auth_id"`
		Code     string          `json:"user_code"`
		Interval json.RawMessage `json:"interval"`
	}
	status, err := s.jsonRequest(ctx, "/api/accounts/deviceauth/usercode", map[string]string{"client_id": clientID}, &device)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("device login failed (HTTP %d); enable device-code login in ChatGPT Security settings or ask your workspace administrator", status)
	}
	seconds, err := strconv.ParseFloat(strings.Trim(string(device.Interval), "\""), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 900 || device.ID == "" || device.Code == "" {
		return fmt.Errorf("invalid device authorization response")
	}
	interval := time.Duration(seconds * float64(time.Second))
	if interval < time.Second {
		interval = time.Second
	}
	deadline, _ := ctx.Deadline()
	if err := show(DeviceCode{URL: authURL + "/codex/device", Code: device.Code, ExpiresAt: deadline}); err != nil {
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
			Code     string          `json:"authorization_code"`
			Verifier string          `json:"code_verifier"`
			Error    json.RawMessage `json:"error"`
		}
		status, err := s.jsonRequest(ctx, "/api/accounts/deviceauth/token", map[string]string{"device_auth_id": device.ID, "user_code": device.Code}, &grant)
		if err != nil {
			return err
		}
		if status == 403 || status == 404 {
			continue
		}
		if status != http.StatusOK {
			var code string
			if json.Unmarshal(grant.Error, &code) != nil {
				var object struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(grant.Error, &object)
				code = object.Code
			}
			if code == "deviceauth_authorization_pending" {
				continue
			}
			if code == "slow_down" {
				interval += 5 * time.Second
				continue
			}
			return fmt.Errorf("device authorization failed (HTTP %d); start login again", status)
		}
		if grant.Code == "" || grant.Verifier == "" {
			return fmt.Errorf("invalid device authorization grant")
		}
		token, account, err := s.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {grant.Code}, "code_verifier": {grant.Verifier}, "redirect_uri": {s.authURL + "/deviceauth/callback"}})
		if err != nil {
			return err
		}
		if token.RefreshToken == "" {
			return fmt.Errorf("authorization returned no refresh token")
		}
		return s.mutate(ctx, id, func(c *credential) error {
			if c.Generation != generation {
				return fmt.Errorf("login superseded by another login or logout; retry")
			}
			*c = credential{Token: token, Account: account, Generation: uuid.NewString()}
			return nil
		})
	}
}

func (s *Service) jsonRequest(ctx context.Context, path string, value any, out any) (int, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authURL+path, strings.NewReader(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("invalid authentication endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("device authorization request failed; check connectivity or retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 404 {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("invalid authentication response (HTTP %d)", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func (s *Service) exchange(ctx context.Context, form url.Values) (*oauth2.Token, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.authURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("invalid token endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("token exchange failed; login again")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("token exchange failed (HTTP %d); login again", resp.StatusCode)
	}
	var wire struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire) != nil || wire.Expires <= 0 || wire.Expires > 365*24*3600 {
		return nil, "", fmt.Errorf("invalid token response")
	}
	parts := strings.Split(wire.Access, ".")
	if len(parts) != 3 {
		return nil, "", fmt.Errorf("invalid access token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "", fmt.Errorf("invalid access token claims")
	}
	var claims struct {
		Auth struct {
			Account string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Auth.Account == "" || strings.ContainsAny(claims.Auth.Account, "\r\n") {
		return nil, "", fmt.Errorf("access token has no valid ChatGPT account identity")
	}
	return &oauth2.Token{AccessToken: wire.Access, RefreshToken: wire.Refresh, TokenType: "Bearer", Expiry: time.Now().Add(time.Duration(wire.Expires) * time.Second)}, claims.Auth.Account, nil
}
