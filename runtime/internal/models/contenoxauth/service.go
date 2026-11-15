package contenoxauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

const ProviderType = "contenox"
const BaseURL = "https://events.contenox.com"

var ErrLoginRequired = errors.New("Contenox sign-in required; sign in under Backends")

func Origin() string {
	v := os.Getenv("CONTENOX_LOCAL_ACCOUNT_ORIGIN")
	u, err := url.Parse(v)
	if err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" {
		return u.String()
	}
	return BaseURL
}

type credential struct {
	Token, Generation, Origin string
	Expiry                    time.Time
}
type Service struct {
	db     libdb.DBManager
	client *http.Client
}

func New(db libdb.DBManager) *Service {
	return &Service{db: db, client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func key(id string) string { return "contenox-login:" + id }
func read(ctx context.Context, store runtimetypes.Store, id string) (credential, error) {
	var c credential
	err := store.GetKV(ctx, key(id), &c)
	if errors.Is(err, libdb.ErrNotFound) {
		return credential{}, nil
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
	backend, err := store.GetBackend(ctx, id)
	if err != nil {
		return err
	}
	if !strings.EqualFold(backend.Type, ProviderType) {
		return errors.New("unexpected connection type")
	}
	c, err := read(ctx, store, id)
	if err != nil {
		return err
	}
	if err = fn(&c); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = store.SetKV(ctx, key(id), raw); err != nil {
		return err
	}
	return commit(ctx)
}
func CredentialGeneration(ctx context.Context, store runtimetypes.Store, id string) (string, error) {
	c, err := read(ctx, store, id)
	return c.Generation, err
}
func DeleteCredentials(ctx context.Context, exec libdb.Exec, id string) error {
	err := runtimetypes.New(exec).DeleteKV(ctx, key(id))
	if errors.Is(err, libdb.ErrNotFound) {
		return nil
	}
	return err
}
func (s *Service) Status(ctx context.Context, id string) (modelauth.Status, error) {
	c, err := read(ctx, runtimetypes.New(s.db.WithoutTransaction()), id)
	status := modelauth.Status{State: "login_required", Generation: c.Generation, ExpiresAt: c.Expiry}
	if c.Origin == Origin() && c.Token != "" && c.Expiry.After(time.Now()) {
		status.State = "authenticated"
	}
	return status, err
}
func (s *Service) Headers(ctx context.Context, id string) (http.Header, error) {
	c, err := read(ctx, runtimetypes.New(s.db.WithoutTransaction()), id)
	if err != nil {
		return nil, err
	}
	if c.Origin != Origin() || c.Token == "" || !c.Expiry.After(time.Now()) {
		return nil, ErrLoginRequired
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.Token)
	return h, nil
}
func (s *Service) request(ctx context.Context, method, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, Origin()+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("Contenox account returned HTTP %d", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(out)
}
func (s *Service) Logout(ctx context.Context, id string) error {
	return s.mutate(ctx, id, func(c *credential) error {
		if c.Token != "" && c.Origin == Origin() && c.Expiry.After(time.Now()) {
			if err := s.request(ctx, "DELETE", "/v1/model-logins/current", c.Token, nil); err != nil {
				return err
			}
		}
		*c = credential{Generation: uuid.NewString()}
		return nil
	})
}
func (s *Service) Login(ctx context.Context, id string, show func(modelauth.DeviceCode) error) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	gen := uuid.NewString()
	if err := s.mutate(ctx, id, func(c *credential) error { c.Generation = gen; return nil }); err != nil {
		return err
	}
	var start struct {
		Code, Poll, URL string
		Expires         time.Time `json:"expires_at"`
	}
	if err := s.request(ctx, "POST", "/v1/model-logins", "", &start); err != nil {
		return err
	}
	if !strings.HasPrefix(start.URL, Origin()+"/signin?request=") || len(start.Code) != 9 || len(start.Poll) != 43 || !start.Expires.After(time.Now()) {
		return errors.New("invalid Contenox device response")
	}
	if err := show(modelauth.DeviceCode{URL: start.URL, Code: start.Code, ExpiresAt: start.Expires}); err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		var grant struct {
			Token, Status string
			Expiry        time.Time `json:"expires_at"`
		}
		if err := s.request(ctx, "GET", "/v1/model-logins/approval", start.Poll, &grant); err != nil {
			return err
		}
		if grant.Status == "waiting" {
			continue
		}
		if len(grant.Token) != 43 || !grant.Expiry.After(time.Now()) {
			return errors.New("invalid Contenox authorization")
		}
		return s.mutate(ctx, id, func(c *credential) error {
			if c.Generation != gen {
				return errors.New("sign-in superseded")
			}
			*c = credential{Token: grant.Token, Expiry: grant.Expiry, Generation: uuid.NewString(), Origin: Origin()}
			return nil
		})
	}
}

type Subscription struct {
	Plan            string    `json:"plan"`
	PeriodEnd       time.Time `json:"renews_at"`
	UsedPercent     int       `json:"used_percent"`
	ReservedPercent int       `json:"reserved_percent"`
	AccountURL      string    `json:"account_url"`
}

func (s *Service) Subscription(ctx context.Context, id string) (Subscription, error) {
	h, err := s.Headers(ctx, id)
	if err != nil {
		return Subscription{}, err
	}
	var details Subscription
	err = s.request(ctx, "GET", "/v1/subscription", strings.TrimPrefix(h.Get("Authorization"), "Bearer "), &details)
	details.AccountURL = Origin() + "/"
	return details, err
}
