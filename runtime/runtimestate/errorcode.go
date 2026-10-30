package runtimestate

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo"
)

// Stable, machine-readable classes for a backend's last discovery failure.
// The human-readable text stays in BackendRuntimeState.Error.
const (
	ErrorCodeLoginRequired    = "login_required"
	ErrorCodeUnauthorized     = "unauthorized"
	ErrorCodeUnreachable      = "unreachable"
	ErrorCodeNoModels         = "no_models"
	ErrorCodeModelUnavailable = "model_unavailable"
	ErrorCodeMisconfigured    = "misconfigured"
)

var errNoModels = errors.New("No models found in response")

// ClassifyError maps a discovery error to one of the ErrorCode* constants.
// It returns "" for a nil error and for errors that fit no class (the caller
// then has only the text).
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	var unset *EnvironmentUnsetError
	switch {
	case errors.Is(err, modelauth.ErrLoginRequired):
		return ErrorCodeLoginRequired
	case errors.Is(err, errNoModels):
		return ErrorCodeNoModels
	case errors.As(err, &unset), errors.Is(err, libdb.ErrNotFound):
		return ErrorCodeMisconfigured
	case isUnauthorized(err):
		return ErrorCodeUnauthorized
	case isUnreachable(err):
		return ErrorCodeUnreachable
	}
	return ""
}

func isUnauthorized(err error) bool {
	if errors.Is(err, modelrepo.ErrModelAccessDenied) {
		return true
	}
	var provider *modelrepo.ProviderError
	if errors.As(err, &provider) && (provider.StatusCode == 401 || provider.StatusCode == 403) {
		return true
	}
	var httpErr *modelrepo.HTTPError
	if errors.As(err, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403) {
		return true
	}
	// Catalog clients that format the status into plain text
	// ("... catalog returned 401: ...").
	msg := err.Error()
	return strings.Contains(msg, " returned 401") || strings.Contains(msg, " returned 403")
}

func isUnreachable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"connection refused", "no such host", "i/o timeout", "dial tcp", "deadline exceeded", "actively refused", "connection reset", "network is unreachable"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (s *BackendRuntimeState) setError(err error) {
	if err == nil {
		return
	}
	s.Error = err.Error()
	s.ErrorCode = ClassifyError(err)
}

// setKeyError records a failure to resolve the backend's credential; every such
// failure is a configuration problem whatever the underlying cause.
func (s *BackendRuntimeState) setKeyError(err error) {
	s.Error = apiKeyFailure(err)
	s.ErrorCode = ErrorCodeMisconfigured
}
