#!/usr/bin/env bash
set -eu

target_os=$1
runtime_dir=$2
cuda=$3
hip=$4
vulkan=$5
libdir="$runtime_dir/lib"

mkdir -p "$libdir"
if [ "$target_os" = "windows" ]; then
	echo "post-processing windows DLL layout into $libdir"
	for f in "$runtime_dir"/bin/*llama*.dll "$runtime_dir"/bin/ggml*.dll "$runtime_dir"/bin/*mtmd*.dll; do
		[ -f "$f" ] && cp -a "$f" "$libdir/" || true
	done
	# Also handle any lib-prefixed variants that may have been produced
	for f in "$runtime_dir"/bin/libllama*.dll "$runtime_dir"/bin/libggml*.dll "$runtime_dir"/bin/libmtmd*.dll; do
		[ -f "$f" ] && cp -a "$f" "$libdir/" || true
	done
	ls "$libdir"/*llama*.dll >/dev/null 2>&1 || ls "$libdir"/llama.dll >/dev/null 2>&1 || { echo "direct llama.cpp runtime missing llama DLLs in $libdir after copy from bin/"; exit 1; }
	ls "$libdir"/ggml-cpu*.dll >/dev/null 2>&1 || { echo "direct llama.cpp runtime missing ggml-cpu DLL plugins (GGML_BACKEND_DL) in $libdir"; exit 1; }
	{ [ "$cuda" != "ON" ] || ls "$libdir"/ggml-cuda*.dll >/dev/null 2>&1 || { echo "CUDA requested but no ggml-cuda DLL"; exit 1; }; }
	{ [ "$vulkan" != "ON" ] || ls "$libdir"/ggml-vulkan*.dll >/dev/null 2>&1 || { echo "Vulkan requested but no ggml-vulkan DLL"; exit 1; }; }
elif [ "$target_os" = "darwin" ]; then
	for lib in libllama-common libllama libmtmd libggml libggml-base; do
		test -f "$libdir/$lib.dylib" || { echo "missing $lib.dylib in $libdir"; exit 1; }
	done
	for plugin in "$runtime_dir"/bin/libggml-*.so; do [ ! -e "$plugin" ] || cp "$plugin" "$libdir/"; done
	ls "$libdir"/libggml-cpu*.so >/dev/null 2>&1 || { echo "missing CPU plugin in $libdir (ggml backend plugins are .so modules on macOS)"; exit 1; }
else
	test -f "$libdir/libllama-common.so" || { echo "direct llama.cpp runtime missing libllama-common.so in $libdir (install layout changed?)"; exit 1; }
	test -f "$libdir/libllama.so" || { echo "direct llama.cpp runtime missing libllama.so in $libdir"; exit 1; }
	test -e "$libdir/libmtmd.so" || { echo "direct llama.cpp runtime missing libmtmd.so in $libdir (LLAMA_BUILD_MTMD install layout changed?)"; exit 1; }
	for plugin in "$runtime_dir"/bin/libggml-*.so; do [ -e "$plugin" ] && cp "$plugin" "$libdir/"; done
	ls "$libdir"/libggml-cpu*.so >/dev/null 2>&1 || { echo "direct llama.cpp runtime missing libggml-cpu*.so backend plugins (GGML_BACKEND_DL install layout changed?)"; exit 1; }
	{ [ "$cuda" != "ON" ] || [ -f "$libdir/libggml-cuda.so" ]; } || { echo "direct llama.cpp runtime built with CUDA but missing libggml-cuda.so in $libdir"; exit 1; }
	{ [ "$hip" != "ON" ] || [ -f "$libdir/libggml-hip.so" ]; } || { echo "direct llama.cpp runtime built with HIP but missing libggml-hip.so in $libdir"; exit 1; }
	{ [ "$vulkan" != "ON" ] || [ -f "$libdir/libggml-vulkan.so" ]; } || { echo "direct llama.cpp runtime built with Vulkan but missing libggml-vulkan.so in $libdir"; exit 1; }
	for lib in "$libdir"/lib*.so.*; do
		[ -f "$lib" ] && [ ! -L "$lib" ] || continue
		soname="$(readelf -d "$lib" | awk -F'[][]' '/SONAME/ { print $2; exit }')"
		base="$(basename "$lib")"
		if [ -n "$soname" ] && [ "$soname" != "$base" ]; then ln -sf "$base" "$libdir/$soname"; fi
	done
fi
