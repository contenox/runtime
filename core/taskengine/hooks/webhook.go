package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/contenox/contenox/core/taskengine"
)

// WebhookCaller makes HTTP requests to external services
type WebhookCaller struct {
	client         *http.Client
	defaultHeaders map[string]string
}

// NewWebhookCaller creates a new webhook caller
func NewWebhookCaller(options ...WebhookOption) *WebhookCaller {
	wh := &WebhookCaller{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		defaultHeaders: map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		},
	}

	for _, opt := range options {
		opt(wh)
	}

	return wh
}

// WebhookOption configures the WebhookCaller
type WebhookOption func(*WebhookCaller)

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) WebhookOption {
	return func(h *WebhookCaller) {
		h.client = client
	}
}

// WithDefaultHeader sets a default header
func WithDefaultHeader(key, value string) WebhookOption {
	return func(h *WebhookCaller) {
		h.defaultHeaders[key] = value
	}
}

// Exec implements the HookRepo interface
func (h *WebhookCaller) Exec(ctx context.Context, hook *taskengine.HookCall) (int, any, error) {
	// Get URL from args
	rawURL, ok := hook.Args["url"]
	if !ok {
		return taskengine.StatusError, nil, fmt.Errorf("missing 'url' argument")
	}

	// Parse URL
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return taskengine.StatusError, nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Handle query parameters
	if queryParams, ok := hook.Args["query"]; ok {
		params, err := url.ParseQuery(queryParams)
		if err != nil {
			return taskengine.StatusError, nil, fmt.Errorf("invalid query parameters: %w", err)
		}
		baseURL.RawQuery = params.Encode()
	}

	// Determine HTTP method
	method := "POST"
	if m, ok := hook.Args["method"]; ok {
		method = m
	}

	// Prepare request body
	var body io.Reader
	if hook.Input != "" {
		// If input is JSON, send as-is
		if json.Valid([]byte(hook.Input)) {
			body = bytes.NewBufferString(hook.Input)
		} else {
			// Otherwise wrap in JSON
			payload := map[string]interface{}{
				"message": hook.Input,
				"data":    hook.Args,
			}
			jsonData, err := json.Marshal(payload)
			if err != nil {
				return taskengine.StatusError, nil, fmt.Errorf("failed to marshal payload: %w", err)
			}
			body = bytes.NewBuffer(jsonData)
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, baseURL.String(), body)
	if err != nil {
		return taskengine.StatusError, nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range h.defaultHeaders {
		req.Header.Set(k, v)
	}
	if headers, ok := hook.Args["headers"]; ok {
		var headerMap map[string]string
		if err := json.Unmarshal([]byte(headers), &headerMap); err == nil {
			for k, v := range headerMap {
				req.Header.Set(k, v)
			}
		}
	}

	// Make the request
	resp, err := h.client.Do(req)
	if err != nil {
		return taskengine.StatusError, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return taskengine.StatusError, nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for success status (2xx)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var result interface{}
		if err := json.Unmarshal(respBody, &result); err == nil {
			return taskengine.StatusSuccess, result, nil
		}
		return taskengine.StatusSuccess, string(respBody), nil
	}

	return taskengine.StatusError, nil, fmt.Errorf("webhook failed with status %d: %s", resp.StatusCode, string(respBody))
}

func (h *WebhookCaller) Supports(ctx context.Context) ([]string, error) {
	return []string{"webhook"}, nil
}

var _ taskengine.HookRepo = (*WebhookCaller)(nil)
