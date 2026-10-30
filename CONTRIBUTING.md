# Contributing to the Contenox runtime

Thanks for helping improve Contenox. This repository is the Go runtime: the
`contenox-runtime` binary (CLI, ACP over stdio, and `serve` for the HTTP API).
The desktop app is a separate project that consumes the runtime binary.

Please read the [Code of Conduct](CODE_OF_CONDUCT.md). Report security issues as
described in [SECURITY.md](SECURITY.md).

## Prerequisites

- [Go](https://go.dev/doc/install), the version in `go.mod` (1.26 at the time of writing).
- GNU `make` (on Windows: `winget install ezwinports.make` or GNU Make from winget).
- `git`.
- Only for specific suites: Python 3 (`make test-api`), Rust and `cargo`
  (`make rust-e2e`), Docker (container-backed tests, see below).

Native Windows is a supported development platform. Run `make` from PowerShell,
cmd, Git Bash or WSL.

## Build and run

```sh
make build            # bin/contenox-runtime (bin\contenox-runtime.exe on Windows)
make build-windows    # cross-build bin/contenox-runtime-windows-amd64.exe
bin/contenox-runtime --help
```

On PowerShell use `bin\contenox-runtime.exe --help`.

### Install a development build

```sh
make dev-install      # build and install contenox-runtime into your per-user bin dir
make dev-uninstall    # remove it again
```

The binary goes to `~/.local/bin` (`%USERPROFILE%\.local\bin` on Windows). Set
`DEV_BIN_DIR` to install elsewhere. The command prints a note if that directory is
not on your `PATH` or if another `contenox-runtime` comes first. Other local tools,
such as the desktop app, find the runtime only by the name `contenox-runtime` on
`PATH`. Stop a running runtime before reinstalling on Windows (the file is locked).

## Tests and checks

```sh
make test-unit        # fast path, same as CI: go test -short -run "^TestUnit_" ./...
make test             # go test ./... (everything, including slower suites)
make lint             # go vet ./...
gofmt -l .            # must print nothing
```

Run `make test-unit`, `make lint` and `gofmt` before opening a pull request.
Behavior changes need tests in the package that owns the behavior; unit tests are
named `TestUnit_...` so the fast path picks them up.

Some suites assume Linux (POSIX paths, the Linux-only agent sandbox, containers) and
are expected to fail on a native Windows checkout:

- Tests that start containers (several services use a containerised database via
  testcontainers) need Docker; on Windows they fail with "rootless Docker is not
  supported" or are skipped when no healthy provider is found.
- Sandbox and local-tool tests (`libsandbox`, `runtime/localtools`, `runtime/vfs`,
  agent hosting) use Linux paths and isolation and fail on Windows.

So on Windows `make test-unit` reports failures in those packages; they are not caused by
your change. Run `go test` for the packages you touched, and use WSL, Linux or CI for the
full suite.

### Targets that need a Unix shell

These targets are bash scripts and are meant for Linux, macOS, WSL or Git Bash.
They do not run under cmd.exe or plain PowerShell:

| Target | What it does | Extra tools |
|--------|--------------|-------------|
| `make test-contenox-help` | CLI help smoke check (run it when commands, flags or help text change) | bash |
| `make test-api` | Python API tests | bash, python3 |
| `make acp-conformance` | ACP conformance suite (`ACP_VALIDATOR_BIN` or a built `tools/acp-validator`) | bash, cargo |
| `make rust-e2e` | Rust black-box end-to-end suite | cargo |
| `make release-assets`, `release-check`, `release` | release builds, checksums, manifest, S3 upload | bash, python3, sha256sum, aws |

Maintainers run the release targets; contributors normally do not need them.

Other helpers: `make clean` removes `bin/` and `dist/`; `make help` lists targets.

## Repository layout

```text
cmd/contenox/      contenox-runtime entry point
runtime/           AI workflow packages (CLI, task engine, services, local tools)
lib*/              infrastructure libraries with no LLM dependency
apitests/          Python API tests
scripts/           bash helpers behind the Unix-only targets
tools/             dev helpers (devtool), ACP validator, Rust e2e suite
```

## Pull request guidelines

1. Open an issue first for major feature or architecture changes.
2. Branch from `main` with a descriptive name such as `feature/xyz`, `fix/abc` or
   `docs/def`.
3. Use clear commit messages. Conventional Commit prefixes are preferred.
4. Run `gofmt` on Go changes and keep the checks above green.
5. Keep LF line endings. The repository stores text files with LF; configure
   `core.autocrlf` accordingly on Windows.
6. Keep generated artifacts out of commits unless the release process requires them.
7. Update user-facing docs when commands, flags, HTTP routes or help text change.

## Code conventions

- Prefer self-documenting code. Add short comments only for non-obvious invariants,
  protocol behavior, security boundaries or tricky edge cases.
- Service constructors accept interfaces; wire concrete implementations at the
  composition root. Prefer the narrowest interface a piece of code needs.
- Keep chain behavior declarative. New Go primitives in the task engine should be rare.
- Tool exposure must be explicit: omitted, `null` or `[]` tools expose no registry
  tools. Use `["*"]` only when a chain intentionally opts into everything, and prefer
  narrow allowlists. Runtime allowlists may restrict task allowlists but never expand them.

Licensed under Apache-2.0.
