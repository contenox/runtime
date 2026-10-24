# acp-validator

A conformance-checking ACP client used to validate ACP **agent** (server-side)
implementations — this is what `make acp-conformance` runs against
`libacp/cmd/acp-stub-agent` (see `libacp/agentconformance_test.go`).

It is not vendored as a buildable Go module; it's a standalone Rust binary
whose source lives here so it survives outside any one session's scratch
directory. It pins `agent-client-protocol = "=1.2.0"` from crates.io, so no
rust-sdk checkout is needed:

```sh
cd tools/acp-validator && cargo build
# binary at tools/acp-validator/target/debug/acp-validator
```

Either let `make acp-conformance` pick that binary up (the Makefile defaults
`ACP_VALIDATOR_BIN` to it) or point `ACP_VALIDATOR_BIN` at any other build.
