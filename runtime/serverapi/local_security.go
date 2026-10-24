package serverapi

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/contenox/runtime/apiframework"
)

func ValidateLocalServeSecurity(addr, token string) error {
	if IsLoopbackAddress(addr) {
		return nil
	}
	if strings.TrimSpace(token) != "" {
		return nil
	}
	return fmt.Errorf("TOKEN is required when ADDR is not loopback; use ADDR=127.0.0.1 for local-only serving")
}

func IsLoopbackAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return true
	}
	addr = strings.TrimPrefix(strings.TrimSuffix(addr, "]"), "[")
	if strings.EqualFold(addr, "localhost") {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

func ProtectAPI(token, allowedOrigins string, next http.Handler) http.Handler {
	token = strings.TrimSpace(token)
	allowedOrigins = strings.TrimSpace(allowedOrigins)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			if !requireCredential(w, r, token) {
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if isCrossSiteBrowserMutation(r, allowedOrigins) {
			_ = apiframework.Error(w, r, apiframework.Forbidden("cross-origin mutating requests require TOKEN: open the cockpit on the API origin, allow it with ALLOWED_API_ORIGINS, or run serve with TOKEN"), apiframework.AuthorizeOperation)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ProtectMutatingAPI(token string, next http.Handler) http.Handler {
	return ProtectAPI(token, "", next)
}

func ProtectMutatingAPIWithAllowedOrigins(token, allowedOrigins string, next http.Handler) http.Handler {
	return ProtectAPI(token, allowedOrigins, next)
}

func requireCredential(w http.ResponseWriter, r *http.Request, token string) bool {
	if !AuthenticateCredential(token, extractRequestToken(r)) {
		_ = apiframework.Error(w, r, apiframework.ErrUnauthorized, apiframework.GetOperation)
		return false
	}
	return true
}

const sessionCookieName = "auth_token"

func extractRequestToken(r *http.Request) string {
	if t := extractBearerToken(r); t != "" {
		return t
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie != nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func extractBearerToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
		return strings.TrimSpace(apiKey[7:])
	}
	return apiKey
}

func isCrossSiteBrowserMutation(r *http.Request, allowedOrigins string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		return !originMatchesRequest(origin, r) && !originExplicitlyAllowed(origin, allowedOrigins)
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site")
}

func originMatchesRequest(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	reqScheme := "http"
	if r.TLS != nil {
		reqScheme = "https"
	}
	if !strings.EqualFold(u.Scheme, reqScheme) {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	originHost, originPort := splitHostPort(u.Host)
	requestHost, requestPort := splitHostPort(r.Host)
	return originPort == requestPort &&
		isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func splitHostPort(hostport string) (string, string) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return strings.Trim(hostport, "[]"), ""
	}
	return strings.Trim(host, "[]"), port
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func originExplicitlyAllowed(origin, allowedOrigins string) bool {
	for _, allowed := range strings.Split(allowedOrigins, ",") {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" || allowed == "*" {
			continue
		}
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	return false
}
