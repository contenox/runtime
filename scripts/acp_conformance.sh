#!/usr/bin/env bash
# Agent-side ACP conformance: drive the shipped `contenox acp` agent over stdio
# with acp-validator (tools/acp-validator), a conformance-checking ACP client
# built on the reference agent-client-protocol crate.
#
# This is the replacement for the deleted Go harness
# (libacp/agentconformance_test.go + libacp/cmd/acp-stub-agent): the agent under
# test is the real binary instead of a Go stub.
#
# The fixture chain answers every prompt deterministically — a noop task whose
# print is the reply — so no model, backend, GPU, or network is involved.
#
# Checks that need the agent to issue a tool call (permission_roundtrip,
# fs_callbacks, cancel) are not selected: the deterministic chain never calls a
# tool. Add them here when the fixture grows tool-call turns.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${CONTENOX_BIN:-"$ROOT_DIR/bin/contenox"}"
VALIDATOR="${ACP_VALIDATOR_BIN:-}"

fail() {
  printf 'acp-conformance: %s\n' "$*" >&2
  exit 1
}

[[ -n "$VALIDATOR" ]] || fail "set ACP_VALIDATOR_BIN=/path/to/acp-validator (see the Makefile comment above the target)"
[[ -x "$VALIDATOR" ]] || fail "ACP_VALIDATOR_BIN is not executable: $VALIDATOR"
[[ -x "$BIN" ]] || fail "missing $BIN; run: make build-contenox"

TMP_ROOT="$(mktemp -d)"
cleanup() { rm -rf "$TMP_ROOT"; }
trap cleanup EXIT

HOME_DIR="$TMP_ROOT/home"
WORK_DIR="$TMP_ROOT/work"
mkdir -p "$HOME_DIR" "$WORK_DIR"

cat > "$TMP_ROOT/chain.json" <<'JSON'
{
  "id": "acp-conformance-fixture",
  "description": "Deterministic no-model fixture: one noop task whose print is the reply.",
  "tasks": [
    {
      "id": "reply",
      "description": "Answer every prompt with the fixed fixture text.",
      "handler": "noop",
      "print": "acp conformance fixture reply"
    }
  ]
}
JSON

# The ACP surface resolves all of its state under HOME and reads the answering
# chain from CONTENOX_ACP_CHAIN_PATH. CONTENOX_DEFAULT_* values are read
# env-first on every launch, so they are emptied rather than inherited.
export HOME="$HOME_DIR"
export CONTENOX_ACP_CHAIN_PATH="$TMP_ROOT/chain.json"
export CONTENOX_DEFAULT_MODEL="" CONTENOX_DEFAULT_PROVIDER="" \
       CONTENOX_DEFAULT_ALT_MODEL="" CONTENOX_DEFAULT_ALT_PROVIDER="" \
       CONTENOX_DEFAULT_MAX_TOKENS="" CONTENOX_DEFAULT_THINK=""

# session/new requires a configured default model even though the fixture chain
# never resolves one; the name is deliberately fake so any real resolution fails
# loudly instead of finding a backend. update-check keeps the launch offline.
"$BIN" config set default-model acp-conformance-fixture >/dev/null
"$BIN" config set update-check false >/dev/null

cd "$WORK_DIR"
# ACP_VALIDATOR_CHECKS overrides the selected checks (comma-separated names as
# printed by the validator's --help); the default set is what the deterministic
# noop chain can satisfy, since it never issues a tool call.
CHECKS="${ACP_VALIDATOR_CHECKS:-initialize,version_negotiation,session_new,prompt_streaming,update_ordering,unknown_method}"

"$VALIDATOR" \
  --agent "$BIN acp --auto" \
  --checks "$CHECKS" \
  --timeout 30
