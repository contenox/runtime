package modelrepo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrContextLengthExceeded  = errors.New("request exceeds the model's context window")
	ErrRateLimited            = errors.New("provider rate limit or capacity limit hit")
	ErrModelNotFoundOnBackend = errors.New("backend does not serve the requested model")
	ErrModelAccessDenied      = errors.New("backend denied access to the requested model")
	ErrPaymentRequired        = errors.New("provider account requires payment")
	ErrRequestTooLarge        = errors.New("request exceeds the provider's size limit")
)

func IsBackendTerminal(err error) bool {
	return errors.Is(err, ErrModelNotFoundOnBackend) || errors.Is(err, ErrModelAccessDenied)
}

func IsRetryableStreamFailure(err error) bool {
	return errors.Is(err, ErrStreamTruncated)
}

var contextLimitMarkers = []string{
	"context_length_exceeded",
	"maximum context length",
	"prompt is too long",
	"exceed context limit",
	"exceeds context limit",
	"input is too long",
	"input length and `max_tokens` exceed",
	"input length and max_tokens exceed",
	"token count exceeds",
	"exceeds the maximum number of tokens",
	"please reduce the length",
	"context window",
	"context length",
	"kv cache",
}

func IsContextLimitMessage(s string) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	for _, m := range contextLimitMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

var rateLimitMarkers = []string{
	"resource_exhausted",
	"rate_limit_error",
	"overloaded_error",
	"throttl",
}

func isRateLimitCode(s string) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	for _, m := range rateLimitMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

var notFoundCodeMarkers = []string{"not_found"}

var notFoundMessageMarkers = []string{"not found", "does not exist"}

var accessDeniedCodeMarkers = []string{"permission_denied", "access_denied"}

var paymentRequiredCodeMarkers = []string{"billing_error", "insufficient_quota", "payment_required"}

var requestTooLargeCodeMarkers = []string{"request_too_large"}

func containsMarker(s string, markers []string) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func ClassifyProviderError(err error, httpStatus int, code, message string) error {
	if err == nil {
		return nil
	}
	var carried *HTTPError
	if errors.As(err, &carried) {
		httpStatus = carried.StatusCode
		code = carried.Code
		message = carried.Message
	} else {
		if code == "" || message == "" {
			detailCode, detailMessage := ProviderErrorDetail([]byte(message))
			if code == "" {
				code = detailCode
			}
			if bodyMessage, ok := envelopeMessage([]byte(message)); ok {
				message = bodyMessage
			} else if message == "" {
				message = detailMessage
			}
		}
	}
	cause := &ProviderError{StatusCode: httpStatus, Code: code, Message: message, err: err}
	if httpStatus == 429 || httpStatus == 529 || isRateLimitCode(code) {
		return fmt.Errorf("%w: %w", ErrRateLimited, cause)
	}
	if httpStatus == 0 || (httpStatus >= 400 && httpStatus < 500) {
		if IsContextLimitMessage(code) || IsContextLimitMessage(message) {
			return fmt.Errorf("%w: %w", ErrContextLengthExceeded, cause)
		}
	}
	if httpStatus == 402 || containsMarker(code, paymentRequiredCodeMarkers) {
		return fmt.Errorf("%w: %w", ErrPaymentRequired, cause)
	}
	if httpStatus == 413 || containsMarker(code, requestTooLargeCodeMarkers) {
		return fmt.Errorf("%w: %w", ErrRequestTooLarge, cause)
	}
	if httpStatus == 404 || containsMarker(code, notFoundCodeMarkers) ||
		(httpStatus == 0 && containsMarker(message, notFoundMessageMarkers)) {
		return fmt.Errorf("%w: %w", ErrModelNotFoundOnBackend, cause)
	}
	if httpStatus == 401 || httpStatus == 403 || containsMarker(code, accessDeniedCodeMarkers) {
		return fmt.Errorf("%w: %w", ErrModelAccessDenied, cause)
	}
	return cause
}

type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	err        error
}

func (e *ProviderError) Error() string {
	s := "provider error"
	if e.StatusCode != 0 {
		s += fmt.Sprintf(" (status %d)", e.StatusCode)
	}
	if e.Code != "" {
		s += " (" + e.Code + ")"
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.RequestID != "" {
		s += " [request " + e.RequestID + "]"
	}
	if e.err != nil {
		s += ": " + e.err.Error()
	}
	return s
}

func (e *ProviderError) Unwrap() error {
	return e.err
}

type wireErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

type wireValidationBody struct {
	Detail []struct {
		Loc  []any  `json:"loc"`
		Msg  string `json:"msg"`
		Type string `json:"type"`
	} `json:"detail"`
}

func ProviderErrorDetail(body []byte) (code, message string) {
	message = strings.TrimSpace(string(body))
	var envelope wireErrorBody
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return wireErrorCode(envelope.Error.Code, envelope.Error.Type), envelope.Error.Message
	}
	if validation, code, ok := validationEnvelope(body); ok {
		return code, validation
	}
	return "", message
}

func envelopeMessage(body []byte) (string, bool) {
	var envelope wireErrorBody
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return envelope.Error.Message, true
	}
	message, _, ok := validationEnvelope(body)
	return message, ok
}

func validationEnvelope(body []byte) (message, code string, ok bool) {
	var envelope wireValidationBody
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Detail) == 0 {
		return "", "", false
	}
	first := envelope.Detail[0]
	location := make([]string, 0, len(first.Loc))
	for _, part := range first.Loc {
		location = append(location, fmt.Sprintf("%v", part))
	}
	message = strings.TrimSpace(first.Msg)
	if len(location) > 0 {
		if message == "" {
			message = strings.Join(location, ".")
		} else {
			message = strings.Join(location, ".") + ": " + message
		}
	}
	if message == "" {
		return "", "", false
	}
	return message, strings.TrimSpace(first.Type), true
}

func wireErrorCode(code any, fallback string) string {
	switch v := code.(type) {
	case nil:
		return fallback
	case string:
		if v == "" {
			return fallback
		}
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *HTTPError) Error() string {
	s := fmt.Sprintf("provider HTTP %d", e.StatusCode)
	if e.Code != "" {
		s += " (" + e.Code + ")"
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.RequestID != "" {
		s += " [request " + e.RequestID + "]"
	}
	return s
}
