package localtools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestServiceAccountObtainsAndReusesToken(t *testing.T) {
	tokenCalls := 0
	apiCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			user, secret, ok := r.BasicAuth()
			if !ok || user != "reports" || secret != "fixture" || r.FormValue("grant_type") != "client_credentials" {
				t.Error("incorrect token exchange")
			}
			tokenCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access","token_type":"Bearer","expires_in":3600}`))
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Error("missing access token")
			}
			apiCalls++
		}
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	rt, err := serviceAccountTransport(&MCPOAuthConfig{ClientID: "reports", ClientSecret: "fixture", TokenURL: server.URL + "/token"}, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Transport: rt}
	for range 2 {
		resp, err := client.Get(server.URL + "/mcp")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if tokenCalls != 1 || apiCalls != 2 {
		t.Fatalf("token calls %d, authenticated requests %d", tokenCalls, apiCalls)
	}
}

func TestMCPRemoteHeadersStayOnConfiguredOrigin(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	for _, tc := range []struct{ key, value string }{{"X-API-Key", "fixture-key"}, {"Authorization", "Basic dXNlcjpwYXNz"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(tc.key) != tc.value {
				t.Error("expected configured header")
			}
			if r.URL.Path == "/redirect" {
				http.Redirect(w, r, other.URL, 302)
			}
		}))
		base, _ := url.Parse(srv.URL)
		client := http.Client{Transport: &mcpOriginTransport{endpoint: base, base: &headerRoundTripper{base: http.DefaultTransport, headers: map[string]string{tc.key: tc.value}}}}
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if _, err := client.Get(srv.URL + "/redirect"); err == nil || leaked {
			t.Fatal("cross-origin redirect accepted")
		}
		srv.Close()
	}
}

func TestUnit_MCPError_PreservesKindAndCause(t *testing.T) {
	err := newMCPError(ErrMCPSessionUnavailable, `mcp "demo": session unavailable`, context.DeadlineExceeded)
	if !errors.Is(err, ErrMCPSessionUnavailable) {
		t.Fatalf("errors.Is: got %v, want ErrMCPSessionUnavailable", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is: got %v, want context.DeadlineExceeded", err)
	}
}

func TestUnit_ShouldReconnectAfterMCPError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "session unavailable",
			err:  newMCPError(ErrMCPSessionUnavailable, `mcp "demo": session unavailable`, errors.New("dial tcp")),
			want: false,
		},
		{
			name: "tool error",
			err:  newMCPError(errMCPToolReturnedError, `mcp "demo"."ping": tool error`, errors.New("bad arguments")),
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "list tools transport failure",
			err:  newMCPError(ErrMCPListToolsFailed, `mcp "demo": list-tools failed`, errors.New("connection reset by peer")),
			want: true,
		},
	}

	for _, tt := range tests {
		if got := shouldReconnectAfterMCPError(tt.err); got != tt.want {
			t.Fatalf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestUnit_ErrOAuthNotAuthenticatedAlias(t *testing.T) {
	if ErrOAuthNotAuthenticated != ErrMCPOAuthNotAuthenticated {
		t.Fatalf("alias mismatch: got %v want %v", ErrOAuthNotAuthenticated, ErrMCPOAuthNotAuthenticated)
	}
}
