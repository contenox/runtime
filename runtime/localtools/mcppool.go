package localtools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/internal/winproc"
	"github.com/contenox/runtime/runtime/localtools/mcpoauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

type MCPTransport string

const (
	MCPTransportStdio MCPTransport = "stdio"
	MCPTransportSSE   MCPTransport = "sse"
	MCPTransportHTTP  MCPTransport = "http"
)

var (
	errMCPTransportSetupFailed   = errors.New("mcp transport setup failed")
	errMCPConnectFailed          = errors.New("mcp connect failed")
	errMCPReconnectFailed        = errors.New("mcp reconnect failed")
	errMCPToolReturnedError      = errors.New("mcp tool returned an error")
	errMCPOAuthConfigMissing     = errors.New("mcp oauth config missing")
	errMCPOAuthTokenStoreMissing = errors.New("mcp oauth token store missing")
	errMCPOAuthDiscoveryFailed   = errors.New("mcp oauth discovery failed")

	ErrMCPSessionUnavailable    = errors.New("mcp session unavailable")
	ErrMCPToolCallFailed        = errors.New("mcp tool call failed")
	ErrMCPListToolsFailed       = errors.New("mcp list-tools failed")
	ErrMCPOAuthNotAuthenticated = errors.New("mcp oauth not authenticated")
	ErrOAuthNotAuthenticated    = ErrMCPOAuthNotAuthenticated
)

type MCPAuthType string

const (
	MCPAuthNone   MCPAuthType = ""
	MCPAuthBearer MCPAuthType = "bearer"
	MCPAuthOAuth  MCPAuthType = "oauth"
)

type MCPAuthConfig struct {
	Type MCPAuthType

	Token string

	APIKeyFromEnv string
}

func (a *MCPAuthConfig) ResolveToken() string {
	if a == nil {
		return ""
	}
	if a.Token != "" {
		return a.Token
	}
	if a.APIKeyFromEnv != "" {
		return os.Getenv(a.APIKeyFromEnv)
	}
	return ""
}

type MCPOAuthConfig struct {
	ClientName string

	ClientID string

	ClientSecret string

	Scopes []string

	CallbackPort int

	TokenStore mcpoauth.TokenStore

	OpenBrowser func(url string) error
}

type mcpError struct {
	kind    error
	message string
	cause   error
}

func newMCPError(kind error, message string, cause error) error {
	return &mcpError{
		kind:    kind,
		message: message,
		cause:   cause,
	}
}

func (e *mcpError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause == nil {
		return e.message
	}
	return e.message + ": " + e.cause.Error()
}

func (e *mcpError) Unwrap() []error {
	if e == nil {
		return nil
	}
	errs := make([]error, 0, 2)
	if e.kind != nil {
		errs = append(errs, e.kind)
	}
	if e.cause != nil {
		errs = append(errs, e.cause)
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func (c *MCPOAuthConfig) ResolveClientName() string {
	if c.ClientName != "" {
		return c.ClientName
	}
	return "contenox"
}

func (c *MCPOAuthConfig) ResolveCallbackPort() int {
	if c.CallbackPort > 0 {
		return c.CallbackPort
	}
	return 49152
}

type MCPServerConfig struct {
	Name string

	Transport MCPTransport

	Command string
	Args    []string

	URL string

	Auth *MCPAuthConfig

	OAuth *MCPOAuthConfig

	ConnectTimeout time.Duration

	MCPSessionID string

	OnSessionID func(string)

	Tracker libtracker.ActivityTracker
}

type MCPSessionPool struct {
	mu      sync.RWMutex
	session *mcp.ClientSession
	cfg     MCPServerConfig
	tracker libtracker.ActivityTracker

	sidMu        sync.RWMutex
	mcpSessionID string
}

func NewMCPSessionPool(cfg MCPServerConfig) *MCPSessionPool {
	t := cfg.Tracker
	if t == nil {
		t = libtracker.NoopTracker{}
	}
	return &MCPSessionPool{
		cfg:          cfg,
		tracker:      t,
		mcpSessionID: cfg.MCPSessionID,
	}
}

func (p *MCPSessionPool) Connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		return nil
	}
	return p.connectLocked(ctx)
}

func (p *MCPSessionPool) connectLocked(ctx context.Context) error {
	reportErr, reportChange, end := p.tracker.Start(ctx, "connect", "mcp_server", "name", p.cfg.Name, "transport", string(p.cfg.Transport))
	defer end()

	p.sidMu.Lock()
	p.mcpSessionID = ""
	p.sidMu.Unlock()

	timeout := p.cfg.ConnectTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	transport, err := p.buildTransport()
	if err != nil {
		userErr := newMCPError(errMCPTransportSetupFailed, fmt.Sprintf("mcp %q: transport setup failed", p.cfg.Name), err)
		reportErr(userErr)
		return userErr
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "contenox",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		userErr := newMCPError(errMCPConnectFailed, fmt.Sprintf("mcp %q: connect failed", p.cfg.Name), err)
		reportErr(userErr)
		return userErr
	}

	p.session = session
	reportChange(p.cfg.Name, map[string]any{"transport": string(p.cfg.Transport), "url": p.cfg.URL})
	return nil
}

func (p *MCPSessionPool) buildTransport() (mcp.Transport, error) {
	getSessionID := func() string {
		p.sidMu.RLock()
		defer p.sidMu.RUnlock()
		return p.mcpSessionID
	}
	setSessionID := func(id string) {
		p.sidMu.Lock()
		changed := p.mcpSessionID != id
		if changed {
			p.mcpSessionID = id
		}
		p.sidMu.Unlock()
		if changed && p.cfg.OnSessionID != nil {
			p.cfg.OnSessionID(id)
		}
	}

	switch p.cfg.Transport {
	case MCPTransportStdio, "":
		if p.cfg.Command == "" {
			return nil, fmt.Errorf("stdio transport requires a command")
		}
		cmd := exec.Command(p.cfg.Command, p.cfg.Args...)
		winproc.Hide(cmd)
		return &mcp.CommandTransport{Command: cmd}, nil

	case MCPTransportSSE:
		if p.cfg.URL == "" {
			return nil, fmt.Errorf("sse transport requires a url")
		}
		var rt http.RoundTripper = http.DefaultTransport
		if p.cfg.Auth != nil && p.cfg.Auth.Type == MCPAuthOAuth {
			oauthRT, err := p.buildOAuthRoundTripper(rt)
			if err != nil {
				return nil, err
			}
			rt = oauthRT
		} else if token := p.cfg.Auth.ResolveToken(); token != "" {
			rt = &bearerRoundTripper{base: rt, token: token}
		}
		rt = &sessionRoundTripper{base: rt, getSessionID: getSessionID, setSessionID: setSessionID}
		t := &mcp.SSEClientTransport{Endpoint: p.cfg.URL}
		t.HTTPClient = &http.Client{Transport: rt}
		return t, nil

	case MCPTransportHTTP:
		if p.cfg.URL == "" {
			return nil, fmt.Errorf("http transport requires a url")
		}
		var rt http.RoundTripper = http.DefaultTransport
		if p.cfg.Auth != nil && p.cfg.Auth.Type == MCPAuthOAuth {
			oauthRT, err := p.buildOAuthRoundTripper(rt)
			if err != nil {
				return nil, err
			}
			rt = oauthRT
		} else if token := p.cfg.Auth.ResolveToken(); token != "" {
			rt = &bearerRoundTripper{base: rt, token: token}
		}
		rt = &sessionRoundTripper{base: rt, getSessionID: getSessionID, setSessionID: setSessionID}
		t := &mcp.StreamableClientTransport{Endpoint: p.cfg.URL}
		t.HTTPClient = &http.Client{Transport: rt}
		return t, nil

	default:
		return nil, fmt.Errorf("unknown MCP transport: %q", p.cfg.Transport)
	}
}

func (p *MCPSessionPool) Session(ctx context.Context) (*mcp.ClientSession, error) {
	p.mu.RLock()
	s := p.session
	p.mu.RUnlock()
	if s != nil {
		return s, nil
	}
	if err := p.Connect(ctx); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.session, nil
}

func (p *MCPSessionPool) Reconnect(ctx context.Context) error {
	reportErr, reportChange, end := p.tracker.Start(ctx, "reconnect", "mcp_server", "name", p.cfg.Name)
	defer end()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		_ = p.session.Close()
		p.session = nil
	}
	reportChange(p.cfg.Name, "reconnecting")
	if err := p.connectLocked(ctx); err != nil {
		userErr := newMCPError(errMCPReconnectFailed, fmt.Sprintf("mcp %q: reconnect failed", p.cfg.Name), err)
		reportErr(userErr)
		return userErr
	}
	return nil
}

func (p *MCPSessionPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		err := p.session.Close()
		p.session = nil
		return err
	}
	return nil
}

type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (b *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req2)
}

type sessionRoundTripper struct {
	base         http.RoundTripper
	getSessionID func() string
	setSessionID func(string)
}

func (srt *sessionRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	sid := srt.getSessionID()

	injected := false
	if sid != "" && req2.Header.Get("Mcp-Session-Id") == "" {
		req2.Header.Set("Mcp-Session-Id", sid)
		injected = true
	}

	resp, err := srt.base.RoundTrip(req2)

	if err == nil && resp != nil && resp.StatusCode == http.StatusNotFound && injected {
		if req.Body == nil || req.GetBody != nil {
			srt.setSessionID("")

			if resp.Body != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			req3 := req.Clone(req.Context())
			req3.Header.Del("Mcp-Session-Id")
			if req.Body != nil && req.GetBody != nil {
				if body, bodyErr := req.GetBody(); bodyErr == nil {
					req3.Body = body
				}
			}
			resp, err = srt.base.RoundTrip(req3)
		}
	}

	if err == nil && resp != nil {
		if newID := resp.Header.Get("Mcp-Session-Id"); newID != "" {
			srt.setSessionID(newID)
		}
	}
	return resp, err
}

func (p *MCPSessionPool) CallTool(ctx context.Context, toolName string, args map[string]any) (any, error) {
	reportErr, reportChange, end := p.tracker.Start(ctx, "call_tool", "mcp_server", "name", p.cfg.Name, "tool", toolName)
	defer end()
	if args == nil {
		args = map[string]any{}
	}
	result, err := p.callTool(ctx, toolName, args)
	if err != nil {
		if shouldReconnectAfterMCPError(err) {
			if reconnectErr := p.Reconnect(ctx); reconnectErr != nil {
				p.reportReconnectCascade(ctx, err, reconnectErr, toolName)
				userErr := newMCPError(
					ErrMCPToolCallFailed,
					fmt.Sprintf("mcp %q.%q: call failed after reconnect attempt", p.cfg.Name, toolName),
					reconnectErr,
				)
				reportErr(userErr)
				return nil, userErr
			}
			result, err = p.callTool(ctx, toolName, args)
		}
	}
	if err != nil {
		reportErr(err)
		return nil, err
	}
	reportChange(p.cfg.Name, toolName)
	return result, nil
}

func (p *MCPSessionPool) callTool(ctx context.Context, toolName string, args map[string]any) (any, error) {
	session, err := p.Session(ctx)
	if err != nil {
		return nil, newMCPError(
			ErrMCPSessionUnavailable,
			fmt.Sprintf("mcp %q.%q: session unavailable", p.cfg.Name, toolName),
			err,
		)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return nil, newMCPError(
			ErrMCPToolCallFailed,
			fmt.Sprintf("mcp %q.%q: call failed", p.cfg.Name, toolName),
			err,
		)
	}
	if result.IsError {
		toolErr := strings.TrimSpace(mcpCollectText(result.Content))
		if toolErr == "" {
			toolErr = "tool returned an empty error response"
		}
		return nil, newMCPError(
			errMCPToolReturnedError,
			fmt.Sprintf("mcp %q.%q: tool error", p.cfg.Name, toolName),
			errors.New(toolErr),
		)
	}
	if result.StructuredContent != nil {
		return result.StructuredContent, nil
	}
	return mcpCollectText(result.Content), nil
}

func (p *MCPSessionPool) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	reportErr, reportChange, end := p.tracker.Start(ctx, "list_tools", "mcp_server", "name", p.cfg.Name)
	defer end()
	tools, err := p.listTools(ctx)
	if err != nil {
		if shouldReconnectAfterMCPError(err) {
			if reconnectErr := p.Reconnect(ctx); reconnectErr != nil {
				p.reportReconnectCascade(ctx, err, reconnectErr, "")
				userErr := newMCPError(
					ErrMCPListToolsFailed,
					fmt.Sprintf("mcp %q: list-tools failed after reconnect attempt", p.cfg.Name),
					reconnectErr,
				)
				reportErr(userErr)
				return nil, userErr
			}
			tools, err = p.listTools(ctx)
		}
	}
	if err != nil {
		reportErr(err)
		return nil, err
	}
	reportChange(p.cfg.Name, len(tools))
	return tools, nil
}

func (p *MCPSessionPool) listTools(ctx context.Context) ([]*mcp.Tool, error) {
	session, err := p.Session(ctx)
	if err != nil {
		return nil, newMCPError(
			ErrMCPSessionUnavailable,
			fmt.Sprintf("mcp %q: list-tools session unavailable", p.cfg.Name),
			err,
		)
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, newMCPError(
			ErrMCPListToolsFailed,
			fmt.Sprintf("mcp %q: list-tools failed", p.cfg.Name),
			err,
		)
	}
	return result.Tools, nil
}

func mcpCollectText(contents []mcp.Content) string {
	var sb []byte
	for _, c := range contents {
		if tc, ok := c.(*mcp.TextContent); ok {
			if len(sb) > 0 {
				sb = append(sb, '\n')
			}
			sb = append(sb, tc.Text...)
		}
	}
	return string(sb)
}

func (p *MCPSessionPool) reportReconnectCascade(ctx context.Context, originalErr, reconnectErr error, toolName string) {
	kv := []any{
		"name", p.cfg.Name,
		"original_error", originalErr.Error(),
		"reconnect_error", reconnectErr.Error(),
	}
	if toolName != "" {
		kv = append(kv, "tool", toolName)
	}
	_, reportChange, end := p.tracker.Start(ctx, "mcp_reconnect_cascade", "mcp_server", kv...)
	defer end()
	reportChange(p.cfg.Name, map[string]any{
		"original_error":  originalErr.Error(),
		"reconnect_error": reconnectErr.Error(),
		"tool":            toolName,
	})
}

func isAppError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errMCPToolReturnedError) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid params") ||
		strings.Contains(msg, "tool error") ||
		strings.Contains(msg, "unexpected additional properties") ||
		strings.Contains(msg, "missing properties") ||
		strings.Contains(msg, "method not found") ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func shouldReconnectAfterMCPError(err error) bool {
	if err == nil {
		return false
	}
	if isAppError(err) {
		return false
	}
	return !errors.Is(err, ErrMCPSessionUnavailable)
}

func (p *MCPSessionPool) buildOAuthRoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	reportErr, reportChange, end := p.tracker.Start(
		context.Background(), "build_oauth_transport", "mcp_server",
		"name", p.cfg.Name,
	)
	defer end()
	ctx := context.Background()

	cfg := p.cfg.OAuth
	if cfg == nil {
		err := newMCPError(errMCPOAuthConfigMissing, "mcp oauth: config missing", nil)
		reportErr(err)
		return nil, err
	}
	if cfg.TokenStore == nil {
		err := newMCPError(errMCPOAuthTokenStoreMissing, "mcp oauth: token store missing", nil)
		reportErr(err)
		return nil, err
	}

	_, discoverReport, discoverEnd := p.tracker.Start(ctx, "oauth_discover", "mcp_server", "name", p.cfg.Name)
	meta, err := mcpoauth.DiscoverAuthServer(ctx, p.cfg.URL)
	if err != nil {
		discoverEnd()
		userErr := newMCPError(errMCPOAuthDiscoveryFailed, "mcp oauth: discover auth server", err)
		reportErr(userErr)
		return nil, userErr
	}
	discoverReport("auth_endpoint", map[string]any{
		"authorization_endpoint": meta.AuthorizationEndpoint,
		"token_endpoint":         meta.TokenEndpoint,
		"registration_endpoint":  meta.RegistrationEndpoint,
	})
	discoverEnd()

	clientID := cfg.ClientID
	if clientID == "" {
		reg, _ := cfg.TokenStore.GetClientRegistration(ctx, p.cfg.Name)
		if reg != nil {
			clientID = reg.ClientID
		}
	}

	stored, _ := cfg.TokenStore.GetOAuthToken(ctx, p.cfg.Name)

	if stored == nil || stored.AccessToken == "" {
		err := newMCPError(
			ErrMCPOAuthNotAuthenticated,
			fmt.Sprintf("mcp %q: authentication required; run 'contenox mcp auth %s'", p.cfg.Name, p.cfg.Name),
			nil,
		)
		reportErr(err)
		return nil, err
	}

	reportChange("token_loaded", map[string]any{
		"client_id":   clientID,
		"token_valid": stored.Valid(),
		"has_refresh": stored.RefreshToken != "",
		"expiry":      stored.Expiry.String(),
	})

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", cfg.ResolveCallbackPort())

	o2cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       cfg.Scopes,
		RedirectURL:  redirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:  meta.AuthorizationEndpoint,
			TokenURL: meta.TokenEndpoint,
		},
	}

	src := oauth2.ReuseTokenSource(stored, o2cfg.TokenSource(ctx, stored))

	persistingSrc := &persistingTokenSource{
		inner:   src,
		store:   cfg.TokenStore,
		name:    p.cfg.Name,
		o2cfg:   o2cfg,
		baseCtx: ctx,
	}

	logged := &loggingRoundTripper{
		base:    base,
		tracker: p.tracker,
		name:    p.cfg.Name,
	}

	return &oauth2.Transport{Source: persistingSrc, Base: logged}, nil
}

type loggingRoundTripper struct {
	base    http.RoundTripper
	tracker libtracker.ActivityTracker
	name    string
}

func (l *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := l.base.RoundTrip(req)
	if err != nil {
		reportErr, _, end := l.tracker.Start(req.Context(), "mcp_http_error", "mcp_server",
			"name", l.name, "url", req.URL.String(), "method", req.Method)
		defer end()
		reportErr(err)
		return resp, err
	}
	if resp != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(strings.NewReader(string(body)))

		detail := map[string]any{
			"url":    req.URL.String(),
			"method": req.Method,
			"status": resp.StatusCode,
			"body":   strings.TrimSpace(string(body)),
		}
		if resp.StatusCode >= 500 {
			reportErr, _, end := l.tracker.Start(req.Context(), "mcp_http_error", "mcp_server", "name", l.name)
			defer end()
			reportErr(fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, req.URL.Host, strings.TrimSpace(string(body))))
		} else {
			_, reportChange, end := l.tracker.Start(req.Context(), "mcp_http_response", "mcp_server", "name", l.name)
			defer end()
			reportChange("4xx", detail)
		}
	}
	return resp, err
}

func RunOAuthFlow(ctx context.Context, o2cfg *oauth2.Config, oauthCfg *MCPOAuthConfig, meta *mcpoauth.ServerMetadata) (*oauth2.Token, error) {
	port := 0
	if oauthCfg != nil {
		port = oauthCfg.ResolveCallbackPort()
	}
	ln, redirectURI, callbackCh, err := mcpoauth.StartCallbackServer(port)
	if err != nil {
		return nil, fmt.Errorf("start callback: %w", err)
	}
	_ = ln
	o2cfg.RedirectURL = redirectURI

	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()

	authURL := o2cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))

	openFn := openBrowserDefault
	if oauthCfg != nil && oauthCfg.OpenBrowser != nil {
		openFn = oauthCfg.OpenBrowser
	}

	clientName := "MCP server"
	if oauthCfg != nil && strings.TrimSpace(oauthCfg.ResolveClientName()) != "" {
		clientName = oauthCfg.ResolveClientName()
	}
	fmt.Printf("\nOpening browser for %s authorization...\nIf the browser doesn't open, visit:\n\n  %s\n\n", clientName, authURL)
	_ = openFn(authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-callbackCh:
		if result.Error != "" {
			return nil, fmt.Errorf("authorization denied: %s — %s", result.Error, result.ErrorDescription)
		}
		if result.State != state {
			return nil, fmt.Errorf("state mismatch: CSRF check failed")
		}
		tok, err := o2cfg.Exchange(ctx, result.Code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, fmt.Errorf("token exchange: %w", err)
		}
		return tok, nil
	}
}

type persistingTokenSource struct {
	inner   oauth2.TokenSource
	store   mcpoauth.TokenStore
	name    string
	o2cfg   *oauth2.Config
	baseCtx context.Context
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.inner.Token()
	if err != nil {
		return nil, err
	}
	_ = s.store.SetOAuthToken(s.baseCtx, s.name, tok)
	return tok, nil
}

func openBrowserDefault(rawURL string) error {
	var cmd *exec.Cmd
	switch {
	case commandExists("xdg-open"):
		cmd = exec.Command("xdg-open", rawURL)
	case commandExists("open"):
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("cmd", "/c", "start", rawURL)
	}
	winproc.Hide(cmd)
	return cmd.Start()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
