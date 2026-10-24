package contenoxcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	envServeURL   = "CONTENOX_SERVER_URL"
	envServeToken = "CONTENOX_SERVER_TOKEN"

	serveClientTimeout = 30 * time.Second
)

type serveClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func addServeClientFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("server", "",
		fmt.Sprintf("Base URL of a running 'contenox serve' (default http://%s:%s; also settable via %s)",
			defaultServeAddr, defaultServePort, envServeURL))
	cmd.PersistentFlags().String("token", "",
		fmt.Sprintf("Bearer token for the serve API, when serve was started with one configured (also settable via %s)",
			envServeToken))
}

func newServeClient(cmd *cobra.Command) (*serveClient, error) {
	base, _ := cmd.Flags().GetString("server")
	defaulted := false
	if strings.TrimSpace(base) == "" {
		base = os.Getenv(envServeURL)
	}
	if strings.TrimSpace(base) == "" {
		base = fmt.Sprintf("http://%s:%s", defaultServeAddr, defaultServePort)
		defaulted = true
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", base, err)
	}

	token, _ := cmd.Flags().GetString("token")
	if strings.TrimSpace(token) == "" {
		token = os.Getenv(envServeToken)
	}
	if strings.TrimSpace(token) == "" && defaulted {
		token = readServeTokenFile()
	}

	if defaulted {
		fmt.Fprintf(cmd.ErrOrStderr(), "serve: %s (default; set --server or %s to target another instance)\n", base, envServeURL)
	}

	return &serveClient{
		baseURL: base,
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: serveClientTimeout},
	}, nil
}

type ServeError struct {
	StatusCode int
	Message    string
}

func (e *ServeError) Error() string {
	return fmt.Sprintf("serve: %s (status %d)", e.Message, e.StatusCode)
}

type serveAPIErrorBody struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *serveClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *serveClient) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *serveClient) delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *serveClient) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api"+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w (is 'contenox serve' running?)", c.baseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response from %s: %w", c.baseURL, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		var apiErr serveAPIErrorBody
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error.Message != "" {
			msg = apiErr.Error.Message
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &ServeError{StatusCode: resp.StatusCode, Message: msg}
	}

	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", c.baseURL, err)
	}
	return nil
}
