#!/usr/bin/env bash
set -euo pipefail

BIN="${CONTENOX_BIN:-./bin/contenox-runtime}"

if [[ ! -x "$BIN" ]]; then
  echo "ERROR: binary not found or not executable: $BIN" >&2
  exit 1
fi

echo "==> CLI help smoke: $BIN"

HELP_OUTPUT="$("$BIN" --help 2>&1)"
echo "$HELP_OUTPUT" | head -5

if ! echo "$HELP_OUTPUT" | grep -q "Version:"; then
  echo "FAIL: 'Version:' not found in --help output" >&2
  exit 1
fi

EXPECTED_CMDS=(
  "acp"
  "acpx"
  "agent"
  "approvals"
  "backend"
  "cache"
  "chat"
  "config"
  "doctor"
  "fleet"
  "init"
  "mcp"
  "mission"
  "model"
  "run"
  "sandbox"
  "serve"
  "session"
  "setup"
  "shell-env"
  "state"
  "tools"
  "update"
  "version"
  "workspace"
)

MISSING=()
for cmd in "${EXPECTED_CMDS[@]}"; do
  if ! echo "$HELP_OUTPUT" | grep -qE "^  $cmd[[:space:]]"; then
    MISSING+=("$cmd")
  fi
done

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "FAIL: missing subcommand(s) in --help output: ${MISSING[*]}" >&2
  exit 1
fi

VERSION_OUTPUT="$("$BIN" version 2>&1)"
if ! echo "$VERSION_OUTPUT" | grep -qE "v[0-9]+\.[0-9]+\.[0-9]+"; then
  echo "FAIL: 'contenox version' did not print a semver string" >&2
  echo "  Got: $VERSION_OUTPUT" >&2
  exit 1
fi

echo "==> OK: all ${#EXPECTED_CMDS[@]} subcommands present, version $VERSION_OUTPUT"
