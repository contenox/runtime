package mcpoauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectedResourceSelectsSeparateIssuer(t *testing.T) {
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server/team" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q}`, issuer.URL+"/team", issuer.URL+"/login", issuer.URL+"/token")
	}))
	defer issuer.Close()
	var hub *httptest.Server
	hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+hub.URL+`/discovery"`)
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/discovery" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, hub.URL+"/mcp", issuer.URL+"/team")
	}))
	defer hub.Close()
	meta, err := DiscoverAuthServer(context.Background(), hub.URL+"/mcp")
	if err != nil || meta.TokenEndpoint != issuer.URL+"/token" {
		t.Fatalf("unexpected issuer discovery: %v %v", meta, err)
	}
}
