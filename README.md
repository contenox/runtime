# Contenox Runtime

The Go runtime for Contenox: an Agent Client Protocol server with an HTTP control plane.

The desktop and web clients live in separate repositories.

## Build

```bash
make build
```

The binary is `bin/contenox`.

## Run

```bash
bin/contenox serve
bin/contenox acp
```

`serve` exposes the HTTP API and ACP WebSocket. `acp` speaks ACP over stdio.

## Test

```bash
make test-unit
make test-contenox-help
make test-api
make acp-conformance
make rust-e2e
```

## License

Apache-2.0.
