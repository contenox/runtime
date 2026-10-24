package llmretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/internal/llmresolver"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type ErrorClass string

const (
	ClassNone        ErrorClass = ""
	ClassRateLimit   ErrorClass = "rate_limit"
	ClassServerError ErrorClass = "server_error"
	ClassTimeout     ErrorClass = "timeout"
	ClassAuth        ErrorClass = "auth"
	ClassCapacity    ErrorClass = "capacity"
	ClassCanceled    ErrorClass = "canceled"
	ClassPermanent   ErrorClass = "permanent"
)

func (c ErrorClass) IsRetryable() bool {
	switch c {
	case ClassRateLimit, ClassServerError, ClassTimeout:
		return true
	default:
		return false
	}
}

func NoRetry(cause error) error {
	if cause == nil {
		return nil
	}
	return &noRetryError{cause: cause}
}

type noRetryError struct {
	cause error
}

func (e *noRetryError) Error() string { return e.cause.Error() }

func (e *noRetryError) Unwrap() error { return e.cause }

func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ClassNone
	}
	var veto *noRetryError
	if errors.As(err, &veto) {
		return ClassPermanent
	}
	if errors.Is(err, context.Canceled) {
		return ClassCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassTimeout
	}

	if class, ok := classOfSentinel(err); ok {
		return class
	}
	if modelrepo.IsRetryableStreamFailure(err) {
		return ClassServerError
	}

	if errors.Is(err, llmresolver.ErrNoSatisfactoryModel) || errors.Is(err, llmresolver.ErrNoAvailableModels) {
		return ClassPermanent
	}
	var httpErr *modelrepo.HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode == 429 || httpErr.StatusCode == 529:
			return ClassRateLimit
		case httpErr.StatusCode == 401 || httpErr.StatusCode == 403:
			return ClassAuth
		case httpErr.StatusCode >= 500:
			return ClassServerError
		default:
			return ClassPermanent
		}
	}

	s := strings.ToLower(err.Error())
	switch {
	case containsAny(s, "429", "too many requests", "rate limit", "rate-limit", "529", "overloaded"):
		return ClassRateLimit
	case containsAny(s, "401", "403", "unauthorized", "forbidden", "invalid api key", "authentication"):
		return ClassAuth
	case containsAny(s, "context length", "context window", "maximum context", "exceeds context", "tokens exceed", "token count "):
		return ClassCapacity
	case containsAny(s, "500", "502", "503", "504", "internal server error", "bad gateway", "service unavailable", "gateway timeout",
		"empty content from model", "empty content from vertex", "empty candidate parts"):
		return ClassServerError
	case containsAny(s, "i/o timeout", "deadline exceeded", "timed out"):
		return ClassTimeout
	}
	return ClassPermanent
}

func classOfSentinel(err error) (ErrorClass, bool) {
	switch {
	case errors.Is(err, modelrepo.ErrRateLimited):
		return ClassRateLimit, true
	case errors.Is(err, modelrepo.ErrModelAccessDenied):
		return ClassAuth, true
	case errors.Is(err, modelrepo.ErrContextLengthExceeded):
		return ClassCapacity, true
	case errors.Is(err, modelrepo.ErrModelNotFoundOnBackend):
		return ClassPermanent, true
	default:
		return ClassNone, false
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d *Duration) UnmarshalText(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*d = 0
			return nil
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration must be a string or number: %w", err)
	}
	*d = Duration(n)
	return nil
}

type RetryPolicy struct {
	MaxAttempts      int      `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty" toml:"max_attempts,omitempty"`
	InitialBackoff   Duration `yaml:"initial_backoff,omitempty" json:"initial_backoff,omitempty" toml:"initial_backoff,omitempty"`
	MaxBackoff       Duration `yaml:"max_backoff,omitempty" json:"max_backoff,omitempty" toml:"max_backoff,omitempty"`
	Jitter           float64  `yaml:"jitter,omitempty" json:"jitter,omitempty" toml:"jitter,omitempty"`
	RateLimitMinWait Duration `yaml:"rate_limit_min_wait,omitempty" json:"rate_limit_min_wait,omitempty" toml:"rate_limit_min_wait,omitempty"`
	FallbackModelID  string   `yaml:"fallback_model_id,omitempty" json:"fallback_model_id,omitempty" toml:"fallback_model_id,omitempty"`
	FallbackAfter    int      `yaml:"fallback_after,omitempty" json:"fallback_after,omitempty" toml:"fallback_after,omitempty"`
}

type Outcome struct {
	Attempts       int
	UsedFallback   bool
	LastErrorClass ErrorClass
	Elapsed        time.Duration
}

func Do(ctx context.Context, p RetryPolicy, primaryModel string, call func(modelID string) (any, error)) (any, Outcome, error) {
	start := time.Now()
	out := Outcome{}
	attempts := p.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	model := primaryModel
	consecutive := 0
	var lastErr error
	for i := 1; i <= attempts; i++ {
		if err := ctx.Err(); err != nil {
			out.LastErrorClass = ClassifyError(err)
			out.Elapsed = time.Since(start)
			return nil, out, err
		}
		if !out.UsedFallback && p.FallbackModelID != "" && p.FallbackAfter > 0 && consecutive >= p.FallbackAfter {
			model = p.FallbackModelID
			out.UsedFallback = true
			consecutive = 0
		}
		result, err := call(model)
		out.Attempts = i
		if err == nil {
			out.LastErrorClass = ClassNone
			out.Elapsed = time.Since(start)
			return result, out, nil
		}
		class := ClassifyError(err)
		out.LastErrorClass = class
		lastErr = err
		if !class.IsRetryable() {
			out.Elapsed = time.Since(start)
			return nil, out, err
		}
		if i == attempts {
			break
		}
		consecutive++
		wait := backoffFor(p, i, class)
		if wait > 0 {
			select {
			case <-ctx.Done():
				out.LastErrorClass = ClassifyError(ctx.Err())
				out.Elapsed = time.Since(start)
				return nil, out, ctx.Err()
			case <-time.After(wait):
			}
		}
	}
	out.Elapsed = time.Since(start)
	if lastErr != nil {
		return nil, out, lastErr
	}
	return nil, out, fmt.Errorf("llmretry: exhausted attempts with no error captured")
}

func backoffFor(p RetryPolicy, attempt int, class ErrorClass) time.Duration {
	base := p.InitialBackoff.D()
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	maxBackoff := p.MaxBackoff.D()
	wait := base
	for k := 1; k < attempt; k++ {
		wait *= 2
		if maxBackoff > 0 && wait > maxBackoff {
			wait = maxBackoff
			break
		}
	}
	if class == ClassRateLimit && p.RateLimitMinWait.D() > wait {
		wait = p.RateLimitMinWait.D()
	}
	if p.Jitter > 0 {
		wait += time.Duration(rand.Float64() * p.Jitter * float64(wait))
	}
	return wait
}
