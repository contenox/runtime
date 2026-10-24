# contenox-e2e

A black-box end-to-end suite that drives the shipped `contenox` binary from
outside the Go module. It is a trimmed backport of the enterprise suite at
`runtime/tools/contenox-e2e` (commit `bed1c421`, "Record the v1.3.6 source drop");
only the cases that hold against this checkout's surface were kept.

## Why it is not written in Go

An end-to-end test written in the language of the thing it tests can reach
internals, swap a fake in, and still call itself end-to-end. From here there is
no `internal/` to import and no in-process runtime to stand up: the suite spawns
`bin/contenox`, seeds a scratch `HOME`, and asserts on what a user or an
integrator can observe.

## What it covers

| file | cases |
| --- | --- |
| `tests/acp_editor.rs` | ACP over stdio: initialize handshake/capabilities, session/new, streamed prompt answer with usage and title, a failed turn as a JSON-RPC error, a gated write asking the editor's permission UI with the diff, the transcript readable from the CLI afterwards, `acpx` speaking the same protocol, and the hardened profile withholding `/mission`. |
| `tests/sessions.rs` | CLI session bookkeeping: active session and star, per-workspace listing vs `--all`, deletion, workspace markers travelling with the directory, and the control-plane directory not being a workspace. |
| `tests/scripted_backend.rs` | The `scripted-test` backend: a replayed dialog rewinds to turn one when the file is edited, and `doctor` reports a dialog deleted after registration. |

## Fixtures

`Instance` seeds a scratch `HOME` and workspace, runs `contenox init`, disables
the update check, and registers a scripted dialog as the backend the chain uses.

This checkout registers the dialog by pointing the backend URL at the script
file (`backend add <name> --type scripted-test --url <script.json>`): the
`scripted-test` catalog provider resolves its dialog from the backend's base URL.
The enterprise tree used a dedicated `--script` flag and a `role assign` verb
that do not exist here, so `Instance::scripted_backend` adapts accordingly.

`TMPDIR` must not sit inside an existing workspace: the harness refuses to run in
a directory that inherits a `.contenox/workspace.id`, because that would share
state with the real installation.

## What was dropped, and why

Deleted entirely: `envelopes.rs` (asserts the declared-envelope compiler and its
`vet`, which this tree does not ship) and `durable_ask.rs` (asserts an approvals
queue this CLI exposes only through a running `contenox serve`).

Dropped from the kept files (also present in the enterprise source): the
chain-router leaf and preseed cases (`.generated/chain-agent-acp.json`), the
autocomplete-role cases, the `answer` command-menu cases, the envelope `_meta`
cases, and the fs-capability gating cases. Those describe the enterprise ACP
generation; the v1 `acpsvc` in this tree does not implement them, and they were
red here rather than merely unused.

## Running

```sh
cd tools/contenox-e2e && cargo test -- --test-threads=1
# or, from the repo root:
make rust-e2e
```
