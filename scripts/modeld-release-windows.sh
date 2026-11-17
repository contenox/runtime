#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
[ "$(go env GOOS)-$(go env GOARCH)" = windows-amd64 ] || {
  echo 'requires native Windows amd64 under Git Bash' >&2
  exit 1
}
for tool in go cmake make bash python3 zip; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done

export LLAMA_RUNTIME_ROOT="$root/.build/modeld-release/llama-runtime"
export LLAMA_CPP_BUILD_ROOT="$root/.build/modeld-release/llama-build"
export LLAMA_CPP_REF_DIR="$root/.build/modeld-release/llama-src"
export OPENVINO_WORKDIR="$root/.build/modeld-release/openvino"
export OPENVINO_GENAI_REPO="$root/.build/modeld-release/openvino-genai"
export MODELD_RELEASE_OPENVINO=1
deps_out="$root/.build/modeld-release/windows-deps"

make deps-modeld
make build-llamacpp-runtime
make bundle-modeld-deps MODELD_DEPS_OUT="$deps_out"

mapfile -t bundles < <(find "$deps_out" -mindepth 2 -maxdepth 2 -name bundle.env -print | while IFS= read -r envfile; do
  unset MODELD_BUNDLE_PLATFORM MODELD_BUNDLE_OPENVINO
  . "$envfile"
  if [ "${MODELD_BUNDLE_PLATFORM:-}" = windows-amd64 ] && [ "${MODELD_BUNDLE_OPENVINO:-}" = 1 ]; then
    dirname "$envfile"
  fi
done)
[ "${#bundles[@]}" -eq 1 ] || {
  echo "expected one Windows/OpenVINO dependency bundle under $deps_out, found ${#bundles[@]}" >&2
  exit 1
}
make package-modeld-release-windows MODELD_DEPS_ROOT="${bundles[0]}"
