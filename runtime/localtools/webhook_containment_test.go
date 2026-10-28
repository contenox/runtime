package localtools_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_WebTools_RedirectHopReValidatesHostPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect-denied":
			http.Redirect(w, r, "http://denied.invalid/secret", http.StatusFound)
		case "/redirect-metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/redirect-ok":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = w.Write([]byte(`{"ok":true,"final":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()

	tools := newWebTools(t, &recTracker{})

	deniedCtx := ctxWithPolicy(map[string]string{"_denied_hosts": "denied.invalid"})
	_, _, err := execWeb(t, deniedCtx, tools, "web_get", map[string]any{"url": srv.URL + "/redirect-denied"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "_denied_hosts", "the denied host must be refused on the redirect hop, before it is dialed")
	require.Contains(t, err.Error(), "redirect refused by policy")

	_, _, err = execWeb(t, deniedCtx, tools, "web_get", map[string]any{"url": srv.URL + "/redirect-metadata"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "link-local / cloud-metadata", "a redirect to 169.254.169.254 must be refused on the hop")

	res, dt, err := execWeb(t, ctxWithPolicy(map[string]string{"_denied_hosts": ""}),
		tools, "web_get", map[string]any{"url": srv.URL + "/redirect-ok"})
	require.NoError(t, err)
	require.Equal(t, taskengine.DataTypeJSON, dt)
	m, ok := res.(map[string]any)
	require.True(t, ok, "expected JSON map, got %T", res)
	require.Equal(t, true, m["final"], "an allowed redirect must still be followed")
}

func TestUnit_WebTools_LinkLocalTargetRefused(t *testing.T) {
	tools := newWebTools(t, &recTracker{})
	ctx := ctxWithPolicy(map[string]string{"_denied_hosts": ""})

	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1]/",
	} {
		res, dt, err := execWeb(t, ctx, tools, "web_get", map[string]any{"url": target})
		require.NoError(t, err, "a link-local block is a soft denial, not an error: %s", target)
		require.Equal(t, taskengine.DataTypeString, dt, target)
		msg, _ := res.(string)
		require.Contains(t, msg, "link-local / cloud-metadata", target)
	}
}
