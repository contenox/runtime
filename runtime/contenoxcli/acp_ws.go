package contenoxcli

import (
	"errors"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/serverapi"
	"golang.org/x/net/websocket"
)

var errACPUnauthorized = errors.New("acp: unauthorized websocket handshake")

type acpWSConn struct {
	ws       *websocket.Conn
	readBuf  []byte
	writeBuf []byte
}

func (c *acpWSConn) Read(p []byte) (int, error) {
	if len(c.readBuf) == 0 {
		var frame string
		if err := websocket.Message.Receive(c.ws, &frame); err != nil {
			return 0, err
		}
		c.readBuf = append([]byte(frame), '\n')
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *acpWSConn) Write(p []byte) (int, error) {
	c.writeBuf = append(c.writeBuf, p...)
	if len(c.writeBuf) > 0 && c.writeBuf[len(c.writeBuf)-1] == '\n' {
		frame := c.writeBuf[:len(c.writeBuf)-1]
		if err := websocket.Message.Send(c.ws, string(frame)); err != nil {
			return 0, err
		}
		c.writeBuf = c.writeBuf[:0]
	}
	return len(p), nil
}

func (c *acpWSConn) Close() error { return c.ws.Close() }

func acpWebSocketHandler(factory libacp.AgentFactory, token string) http.Handler {
	token = strings.TrimSpace(token)
	s := &websocket.Server{
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			cfg.Origin, _ = websocket.Origin(cfg, req)
			if token != "" && !serverapi.AuthenticateCredential(token, extractACPToken(req)) {
				return errACPUnauthorized
			}
			return nil
		},
	}
	s.Handler = func(ws *websocket.Conn) {
		req := ws.Request()
		ws.PayloadType = websocket.TextFrame
		adapter := &acpWSConn{ws: ws}
		conn := libacp.NewAgentSideConnection(adapter, factory)
		_ = conn.Run(req.Context())
	}
	return s
}

func extractACPToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
		return strings.TrimSpace(apiKey[7:])
	}
	if apiKey != "" {
		return apiKey
	}
	if tok := strings.TrimSpace(r.URL.Query().Get("token")); tok != "" {
		return tok
	}
	if cookie, err := r.Cookie("auth_token"); err == nil && cookie != nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}
