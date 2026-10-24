package serverapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libauth"
)

func expiredSessionJWT(t *testing.T, token string) string {
	t.Helper()
	cfg := libauth.CreateTokenArgs{JWTSecret: deriveSessionSecret(token), JWTExpiry: -time.Hour}
	s, _, err := libauth.CreateToken(cfg, localOperatorIdentity, localOperatorAuthz{})
	if err != nil {
		t.Fatalf("mint expired: %v", err)
	}
	return s
}

func serveLikeMux(token string) http.Handler {
	apiMux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	apiMux.HandleFunc("GET /state", ok)
	apiMux.HandleFunc("GET /backends", ok)
	apiMux.HandleFunc("GET /mcp-servers", ok)

	rootMux := http.NewServeMux()
	AddHealthRoutes(rootMux)
	AddUIAuthRoutes(rootMux, token)
	rootMux.Handle("/api/", http.StripPrefix("/api", ProtectAPI(token, "", apiMux)))
	return rootMux
}

func cookieJWTFor(t *testing.T, token string) *http.Cookie {
	t.Helper()
	return sessionCookieFromLogin(t, token)
}

var protectedGETs = []string{"/api/state", "/api/backends", "/api/mcp-servers"}

func TestServe_TokenSet_UncredentialedGETsAre401(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)

	for _, path := range protectedGETs {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s with no credential: status = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestServe_TokenSet_CookieJWTGetsThrough(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)
	cookie := cookieJWTFor(t, testToken)

	for _, path := range protectedGETs {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("GET %s with cookie JWT: got 401, want non-401", path)
		}
	}
}

func TestServe_TokenSet_RawBearerGetsThrough(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)

	for _, path := range protectedGETs {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("GET %s with raw bearer: got 401, want non-401", path)
		}
	}
}

func TestServe_TokenSet_TamperedCookieIs401(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)
	good := cookieJWTFor(t, testToken)

	tampered := good.Value
	if len(tampered) == 0 {
		t.Fatal("empty cookie JWT")
	}
	b := []byte(tampered)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'Q'
	} else {
		b[len(b)-1] = 'A'
	}
	tampered = string(b)

	for _, path := range protectedGETs {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tampered})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s with tampered cookie: status = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestServe_TokenSet_WrongSecretJWTIs401(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)

	otherJWT, _, err := mintSessionToken("a-completely-different-token")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/state", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: otherJWT})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-secret JWT: status = %d, want 401", resp.StatusCode)
	}
}

func TestServe_TokenSet_ExpiredJWTIs401(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)

	expired := expiredSessionJWT(t, testToken)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/state", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: expired})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired JWT: status = %d, want 401", resp.StatusCode)
	}
}

func TestServe_TokenSet_OpenRoutesNeedNoCredential(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(testToken))
	t.Cleanup(srv.Close)

	cases := []struct {
		method, path string
		body         string
	}{
		{http.MethodGet, "/health", ""},
		{http.MethodGet, "/ui/auth-status", ""},
		{http.MethodPost, "/ui/login", `{"token":"` + testToken + `"}`},
	}
	for _, c := range cases {
		var req *http.Request
		if c.body != "" {
			req, _ = http.NewRequest(c.method, srv.URL+c.path, strings.NewReader(c.body))
		} else {
			req, _ = http.NewRequest(c.method, srv.URL+c.path, nil)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("%s %s must be reachable without a credential, got 401", c.method, c.path)
		}
	}
}

func TestServe_NoToken_LoopbackGETsPassWithoutCredential(t *testing.T) {
	srv := httptest.NewServer(serveLikeMux(""))
	t.Cleanup(srv.Close)

	for _, path := range protectedGETs {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("no-token GET %s: status = %d, want 200", path, resp.StatusCode)
		}
	}
}
