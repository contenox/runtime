package serverapi

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultLoginMaxAttempts = 20
	defaultLoginWindow      = time.Minute
)

func AddUIAuthRoutes(mux *http.ServeMux, token string) {
	token = strings.TrimSpace(token)
	limiter := newLoginLimiter(defaultLoginMaxAttempts, defaultLoginWindow)
	mux.Handle("POST /ui/login", uiLoginHandler(token, limiter))
	mux.Handle("POST /ui/logout", uiLogoutHandler(token))
	mux.Handle("GET /ui/auth-status", uiAuthStatusHandler(token))
}

type authStatusResponse struct {
	Required      bool `json:"required"`
	Authenticated bool `json:"authenticated"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func uiLoginHandler(token string, limiter *loginLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			writeJSON(w, http.StatusOK, authStatusResponse{Required: false, Authenticated: true})
			return
		}
		if !limiter.allow(clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts"})
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Token)), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, authStatusResponse{Required: true, Authenticated: false})
			return
		}
		jwtStr, _, err := mintSessionToken(token)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
			return
		}
		setSessionCookie(w, r, jwtStr)
		writeJSON(w, http.StatusOK, authStatusResponse{Required: true, Authenticated: true})
	})
}

func uiLogoutHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clearSessionCookie(w, r)
		writeJSON(w, http.StatusOK, authStatusResponse{Required: token != "", Authenticated: false})
	})
}

func uiAuthStatusHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required := token != ""
		authenticated := !required || AuthenticateCredential(token, extractRequestToken(r))
		writeJSON(w, http.StatusOK, authStatusResponse{Required: required, Authenticated: authenticated})
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string]*loginWindow
}

type loginWindow struct {
	start time.Time
	count int
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		max:    max,
		window: window,
		now:    time.Now,
		hits:   map[string]*loginWindow{},
	}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	wc := l.hits[key]
	if wc == nil || now.Sub(wc.start) >= l.window {
		l.hits[key] = &loginWindow{start: now, count: 1}
		return true
	}
	if wc.count >= l.max {
		return false
	}
	wc.count++
	return true
}
