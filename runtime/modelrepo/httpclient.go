package modelrepo

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

var SharedHTTPClient = &http.Client{}

const DefaultCallTimeout = 10 * time.Minute

func NonStreamingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DefaultCallTimeout)
}

const httpRetryMaxAttempts = 3

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == 529 || code >= 500
}

func RetryAfterDelay(h http.Header, fallback time.Duration) time.Duration {
	if ms := h.Get("Retry-After-Ms"); ms != "" {
		if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return fallback
}

func DoWithRetry(ctx context.Context, client *http.Client, build func() (*http.Request, error)) (*http.Response, error) {
	if client == nil {
		client = SharedHTTPClient
	}
	for attempt := 1; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if attempt >= httpRetryMaxAttempts || !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		wait := RetryAfterDelay(resp.Header, time.Duration(attempt)*time.Second)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}
