package runtimestate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_ClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"login required", modelauth.ErrLoginRequired, ErrorCodeLoginRequired},
		{"login required wrapped", fmt.Errorf("x: %w", modelauth.ErrLoginRequired), ErrorCodeLoginRequired},
		{"no models", errNoModels, ErrorCodeNoModels},
		{"env unset", &EnvironmentUnsetError{Variable: "K"}, ErrorCodeMisconfigured},
		{"credential missing", libdb.ErrNotFound, ErrorCodeMisconfigured},
		{"provider 401", &modelrepo.ProviderError{StatusCode: 401}, ErrorCodeUnauthorized},
		{"provider 403 wrapped", fmt.Errorf("catalog: %w", &modelrepo.ProviderError{StatusCode: 403}), ErrorCodeUnauthorized},
		{"http 401", &modelrepo.HTTPError{StatusCode: 401}, ErrorCodeUnauthorized},
		{"classified access denied", modelrepo.ClassifyProviderError(errors.New("boom"), 401, "", "bad key"), ErrorCodeUnauthorized},
		{"plain text 401", errors.New("anthropic catalog /v1/models returned 401: invalid x-api-key"), ErrorCodeUnauthorized},
		{"plain text 403", errors.New("Gemini catalog returned 403: denied"), ErrorCodeUnauthorized},
		{"provider 500 is unclassified", &modelrepo.ProviderError{StatusCode: 500}, ""},
		{"deadline", context.DeadlineExceeded, ErrorCodeUnreachable},
		{"refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), ErrorCodeUnreachable},
		{"dns", &net.DNSError{Err: "no such host", Name: "x"}, ErrorCodeUnreachable},
		{"op error", &net.OpError{Op: "dial", Err: errors.New("boom")}, ErrorCodeUnreachable},
		{"url error", &url.Error{Op: "Get", URL: "http://x", Err: errors.New("boom")}, ErrorCodeUnreachable},
		{"windows refused text", errors.New("connectex: No connection could be made because the target machine actively refused it"), ErrorCodeUnreachable},
		{"unknown", errors.New("something odd"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ClassifyError(tc.err))
		})
	}
}

func TestUnit_BackendRuntimeState_SetErrorKeepsTextAndAddsCode(t *testing.T) {
	st := &BackendRuntimeState{}
	st.setError(modelauth.ErrLoginRequired)
	require.Equal(t, modelauth.ErrLoginRequired.Error(), st.Error)
	require.Equal(t, ErrorCodeLoginRequired, st.ErrorCode)

	st = &BackendRuntimeState{}
	st.setKeyError(libdb.ErrNotFound)
	require.Equal(t, "API key not configured", st.Error)
	require.Equal(t, ErrorCodeMisconfigured, st.ErrorCode)
}
