#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${CONTENOX_BIN:-"$ROOT_DIR/bin/contenox-runtime"}"
VALIDATOR="${ACP_VALIDATOR_BIN:-}"

fail() {
  printf 'acp-conformance: %s\n' "$*" >&2
  exit 1
}

[[ -n "$VALIDATOR" ]] || fail "set ACP_VALIDATOR_BIN=/path/to/acp-validator"
[[ -x "$VALIDATOR" ]] || fail "ACP_VALIDATOR_BIN is not executable: $VALIDATOR"
[[ -x "$BIN" ]] || fail "missing $BIN; run: make build"

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

export HOME="$HOME_DIR"
export CONTENOX_ACP_CHAIN_PATH="$TMP_ROOT/chain.json"
export CONTENOX_DEFAULT_MODEL="" CONTENOX_DEFAULT_PROVIDER="" \
       CONTENOX_DEFAULT_ALT_MODEL="" CONTENOX_DEFAULT_ALT_PROVIDER="" \
       CONTENOX_DEFAULT_MAX_TOKENS="" CONTENOX_DEFAULT_THINK=""

"$BIN" config set default-model acp-conformance-fixture >/dev/null
"$BIN" config set update-check false >/dev/null

cd "$WORK_DIR"
CHECKS="${ACP_VALIDATOR_CHECKS:-initialize,version_negotiation,session_new,prompt_streaming,update_ordering,unknown_method}"

"$VALIDATOR" \
  --agent "$BIN acp --auto" \
  --checks "$CHECKS" \
  --timeout 30
