#!/usr/bin/env bash
set -euo pipefail

dir=${1:?usage: modeld-vendor-runtime-deps-windows.sh package-dir}
[ -d "$dir" ] || { echo "no such package dir: $dir" >&2; exit 1; }
[ -f "$dir/LICENSES/llama.cpp/LICENSE" ] || { echo "missing llama.cpp license" >&2; exit 1; }
[ -f "$dir/LICENSES/cuda/EULA.txt" ] || { echo "missing CUDA EULA" >&2; exit 1; }
[ -f "$dir/LICENSES/openvino/LICENSE" ] || { echo "missing OpenVINO license" >&2; exit 1; }

manifest="$dir/LICENSES/vendored-libraries.txt"
{
  printf '%s\n' '# Third-party runtime libraries shipped inside this package.'
  while IFS= read -r file; do
    name=$(basename "$file")
    case "$name" in
      cublas*.dll|cudart*.dll) license='LICENSES/cuda/EULA.txt' ;;
      *) license='LICENSES/llama.cpp/LICENSE' ;;
    esac
    printf '%s -> %s\n' "lib/llamacpp/$name" "$license"
  done < <(find "$dir/lib/llamacpp" -maxdepth 1 -type f -iname '*.dll' | sort)
  while IFS= read -r file; do
    name=$(basename "$file")
    case "$name" in
      openvino*.dll|tbb*.dll) license='LICENSES/openvino/LICENSE' ;;
      msvcp140.dll|vcomp140.dll|vcruntime140.dll|vcruntime140_1.dll)
        license='Microsoft Visual C++ Redistributable; Visual Studio distributable-code terms'
        ;;
      *) echo "unmapped Windows runtime library: $name" >&2; exit 1 ;;
    esac
    printf '%s -> %s\n' "modeld-libs/$name" "$license"
  done < <(find "$dir/modeld-libs" -maxdepth 1 -type f -iname '*.dll' | sort)
} > "$manifest"

[ -s "$manifest" ] || { echo "empty Windows runtime library inventory" >&2; exit 1; }
