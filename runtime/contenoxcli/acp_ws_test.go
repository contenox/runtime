package contenoxcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/acpsvc"
	"github.com/contenox/runtime/runtime/serverapi"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func acpWSDial(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	if token != "" {
		wsURL += "?token=" + token
	}
	ws, err := websocket.Dial(wsURL, "", srv.URL+"/")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func acpWSInitializeFrame(t *testing.T, id int64) string {
	t.Helper()
	params, err := json.Marshal(libacp.InitializeRequest{
		ProtocolVersion:    libacp.ProtocolVersion,
		ClientCapabilities: libacp.ClientCapabilities{},
		ClientInfo:         &libacp.Implementation{Name: "ws-test", Version: "1"},
	})
	require.NoError(t, err)
	req := libacp.NewRequest(libacp.NewRequestIDNumber(id), libacp.MethodInitialize, params)
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	return string(raw)
}

func TestUnit_ACPWebSocketHandler_InitializeRoundTrip(t *testing.T) {
	factory := acpsvc.New(acpsvc.Deps{})
	srv := httptest.NewServer(acpWebSocketHandler(factory, ""))
	t.Cleanup(srv.Close)

	ws := acpWSDial(t, srv, "")
	require.NoError(t, websocket.Message.Send(ws, acpWSInitializeFrame(t, 1)))

	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	var frame string
	require.NoError(t, websocket.Message.Receive(ws, &frame))

	in, err := libacp.ParseIncoming([]byte(frame))
	require.NoError(t, err)
	require.Equal(t, libacp.IncomingKindResponse, in.Kind, "wire: %s", frame)
	require.Nil(t, in.Response.Error, "wire: %s", frame)

	var result map[string]any
	require.NoError(t, json.Unmarshal(in.Response.Result, &result))
	require.Equal(t, float64(1), result["protocolVersion"], "wire: %s", frame)

	agentCaps, ok := result["agentCapabilities"].(map[string]any)
	require.True(t, ok, "agentCapabilities must be present, wire: %s", frame)
	_, hasSessionCaps := agentCaps["sessionCapabilities"]
	require.True(t, hasSessionCaps, "agentCapabilities.sessionCapabilities must be present, wire: %s", frame)
}

func TestUnit_ACPWebSocketHandler_TokenAuth(t *testing.T) {
	const token = "s3cr3t-token"
	factory := acpsvc.New(acpsvc.Deps{})
	srv := httptest.NewServer(acpWebSocketHandler(factory, token))
	t.Cleanup(srv.Close)

	t.Run("without token the handshake is rejected before upgrade", func(t *testing.T) {
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
		ws, err := websocket.Dial(wsURL, "", srv.URL+"/")
		if ws != nil {
			_ = ws.Close()
		}
		require.Error(t, err, "unauthenticated /acp upgrade must be rejected at the handshake")
	})

	t.Run("with token succeeds", func(t *testing.T) {
		ws := acpWSDial(t, srv, token)
		require.NoError(t, websocket.Message.Send(ws, acpWSInitializeFrame(t, 1)))

		require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
		var frame string
		require.NoError(t, websocket.Message.Receive(ws, &frame))

		in, err := libacp.ParseIncoming([]byte(frame))
		require.NoError(t, err)
		require.Equal(t, libacp.IncomingKindResponse, in.Kind, "wire: %s", frame)
		require.Nil(t, in.Response.Error, "wire: %s", frame)

		var result map[string]any
		require.NoError(t, json.Unmarshal(in.Response.Result, &result))
		require.Equal(t, float64(1), result["protocolVersion"], "wire: %s", frame)
	})
}

func TestUnit_ACPWebSocketHandler_SessionCookieAuth(t *testing.T) {
	const token = "s3cr3t-token"
	factory := acpsvc.New(acpsvc.Deps{})
	srv := httptest.NewServer(acpWebSocketHandler(factory, token))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	cfg, err := websocket.NewConfig(wsURL, srv.URL+"/")
	require.NoError(t, err)
	cfg.Header.Set("Cookie", "auth_token="+token)
	ws, err := websocket.DialConfig(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })

	require.NoError(t, websocket.Message.Send(ws, acpWSInitializeFrame(t, 1)))
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	var frame string
	require.NoError(t, websocket.Message.Receive(ws, &frame))

	in, err := libacp.ParseIncoming([]byte(frame))
	require.NoError(t, err)
	require.Equal(t, libacp.IncomingKindResponse, in.Kind, "wire: %s", frame)
	require.Nil(t, in.Response.Error, "wire: %s", frame)
}

func loginJWTCookie(t *testing.T, token string) string {
	t.Helper()
	mux := http.NewServeMux()
	serverapi.AddUIAuthRoutes(mux, token)
	loginSrv := httptest.NewServer(mux)
	t.Cleanup(loginSrv.Close)

	resp, err := http.Post(loginSrv.URL+"/ui/login", "application/json",
		strings.NewReader(`{"token":"`+token+`"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	for _, c := range resp.Cookies() {
		if c.Name == "auth_token" {
			require.NotEqual(t, token, c.Value, "cookie must carry a JWT, not the raw token")
			return c.Value
		}
	}
	t.Fatal("login set no auth_token cookie")
	return ""
}

func TestUnit_ACPWebSocketHandler_CookieJWTAuth(t *testing.T) {
	const token = "s3cr3t-token"
	factory := acpsvc.New(acpsvc.Deps{})
	srv := httptest.NewServer(acpWebSocketHandler(factory, token))
	t.Cleanup(srv.Close)

	jwt := loginJWTCookie(t, token)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	cfg, err := websocket.NewConfig(wsURL, srv.URL+"/")
	require.NoError(t, err)
	cfg.Header.Set("Cookie", "auth_token="+jwt)
	ws, err := websocket.DialConfig(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })

	require.NoError(t, websocket.Message.Send(ws, acpWSInitializeFrame(t, 1)))
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	var frame string
	require.NoError(t, websocket.Message.Receive(ws, &frame))

	in, err := libacp.ParseIncoming([]byte(frame))
	require.NoError(t, err)
	require.Equal(t, libacp.IncomingKindResponse, in.Kind, "wire: %s", frame)
	require.Nil(t, in.Response.Error, "wire: %s", frame)
}
