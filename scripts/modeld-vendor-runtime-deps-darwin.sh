#!/usr/bin/env bash
set -euo pipefail

dir=${1:?usage: modeld-vendor-runtime-deps-darwin.sh package-dir}
[ -d "$dir" ] || { echo "no such package dir: $dir" >&2; exit 1; }
[ -f "$dir/LICENSES/llama.cpp/LICENSE" ] || { echo "missing llama.cpp license" >&2; exit 1; }
[ ! -d "$dir/modeld-libs" ] || { echo "darwin packages vendor no system libraries; unexpected modeld-libs/" >&2; exit 1; }

manifest="$dir/LICENSES/vendored-libraries.txt"
{
  printf '%s\n' '# Third-party runtime libraries shipped inside this package.'
  while IFS= read -r file; do
    printf '%s -> %s\n' "lib/llamacpp/$(basename "$file")" 'LICENSES/llama.cpp/LICENSE'
  done < <(find "$dir/lib/llamacpp" -maxdepth 1 \( -type f -o -type l \) \( -name '*.dylib' -o -name '*.so' \) | sort)
} > "$manifest"

[ "$(wc -l < "$manifest")" -gt 1 ] || { echo "empty darwin runtime library inventory" >&2; exit 1; }
