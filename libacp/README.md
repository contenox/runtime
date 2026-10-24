# libacp

`libacp` is a Go implementation of the [Agent Client Protocol](https://agentclientprotocol.com)
(ACP) — the JSON-RPC-over-NDJSON protocol editors and coding agents use to talk
to each other. It implements ACP v1 (`ProtocolVersion = 1`).

It is maintained as a reusable package inside the Contenox runtime source. The
module path is retained as a stable Go import namespace; it does not identify a
public development repository.

## What it provides

`libacp` implements both roles of the protocol:

- **Agent side** — implement the [`Agent`](agent.go) interface (or embed
  [`UnimplementedAgent`](agent.go) and override only what you need) and serve
  it over a transport with `NewAgentSideConnection`.
- **Client side** — implement the [`Client`](client.go) interface (or embed
  [`UnimplementedClient`](client.go)) and drive an agent over a transport with
  `NewClientSideConnection`.

Both roles share the same wire machinery:

- NDJSON framing over any `io.ReadWriteCloser`
- Request-id correlation
- Per-request cancelable contexts honoring `$/cancel_request`
- Panic-safe handler dispatch
- Extension-method passthrough

The `acpexec` subpackage (`git.contenox.com/libacp/acpexec`) spawns an
agent (or client) subprocess over stdio and wires its stdin/stdout together
into the `io.ReadWriteCloser` a connection expects, plus a `Supervisor` for
restart/backoff around a long-lived subprocess.

It ships in each Contenox source archive rather than as a separate module. Build
and test it from that archive with `go test ./libacp/...`.

## Usage

Client-role usage — spawn an agent subprocess, connect, initialize, open a
session, and send a prompt:

```go
proc, err := acpexec.Spawn(ctx, exec.Command("contenox", "acp"))
if err != nil {
	return err
}

conn := libacp.NewClientSideConnection(proc, func(*libacp.ClientSideConnection) libacp.Client {
	return myClient{} // embeds libacp.UnimplementedClient
})
go conn.Run(ctx)

if _, err := conn.Initialize(ctx, libacp.InitializeRequest{
	ProtocolVersion: libacp.ProtocolVersion,
	ClientInfo:      &libacp.Implementation{Name: "my-editor", Version: "1.0"},
}); err != nil {
	return err
}

sess, err := conn.NewSession(ctx, libacp.NewSessionRequest{Cwd: "/abs/path/to/project"})
if err != nil {
	return err
}

resp, err := conn.Prompt(ctx, libacp.PromptRequest{
	SessionID: sess.SessionID,
	Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hello")},
})
_ = conn.CancelPrompt(sess.SessionID) // cancel the in-flight turn from another goroutine
```

Agent-role usage mirrors this: implement `Agent` (or embed
`UnimplementedAgent`), and serve it over a transport with
`NewAgentSideConnection(rw, factory)`.

See the [package doc](doc.go) and the `*_test.go` files for further detail on
individual methods, session updates, permissions, terminals, and MCP wiring.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
