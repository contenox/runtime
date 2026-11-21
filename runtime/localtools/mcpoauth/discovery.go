package mcpoauth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

var resourceMetadataParameter = regexp.MustCompile(`(?i)(?:[, ]|^)resource_metadata\s*=\s*"([^"]+)"`)

func safeMetadataURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()))
}

// Use an unauthenticated HEAD for the challenge so an SSE endpoint cannot
// leave the settings page waiting on an open event stream.
func protectedResource(ctx context.Context, endpoint *url.URL) *oauthex.ProtectedResourceMetadata {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 4 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint.String(), nil)
	if err != nil {
		return nil
	}
	var locations []string
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		for _, header := range resp.Header.Values("WWW-Authenticate") {
			if match := resourceMetadataParameter.FindStringSubmatch(header); len(match) > 1 {
				locations = append(locations, match[1])
			}
		}
	}
	base := endpoint.Scheme + "://" + endpoint.Host + "/.well-known/oauth-protected-resource"
	locations = append(locations, base+strings.TrimRight(endpoint.EscapedPath(), "/"), base)
	for _, location := range locations {
		if !safeMetadataURL(location) {
			continue
		}
		if resource, err := fetchJSON[oauthex.ProtectedResourceMetadata](ctx, location); err == nil && resource.Resource == endpoint.String() && len(resource.AuthorizationServers) > 0 {
			return resource
		}
	}
	return nil
}

func issuerMetadata(ctx context.Context, issuer string) (*ServerMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil || !safeMetadataURL(issuer) || u.RawQuery != "" {
		return nil, errors.New("invalid authorization server issuer")
	}
	base := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.EscapedPath(), "/")
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for _, location := range []string{base + "/.well-known/oauth-authorization-server" + path, base + "/.well-known/openid-configuration" + path, strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"} {
		meta, err := fetchJSON[ServerMetadata](ctx, location)
		if err == nil && meta.Issuer == issuer && safeMetadataURL(meta.AuthorizationEndpoint) && safeMetadataURL(meta.TokenEndpoint) && (meta.RegistrationEndpoint == "" || safeMetadataURL(meta.RegistrationEndpoint)) {
			return meta, nil
		}
	}
	return nil, errors.New("the advertised OAuth issuer did not return valid authorization server metadata")
}
