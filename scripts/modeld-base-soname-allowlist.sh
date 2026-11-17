#!/usr/bin/env bash
# The SONAMEs a modeld package may leave to the target host: base-OS runtime
# libraries present on any Linux image, the libraries the NVIDIA driver installs
# (a GPU host has those if nvidia-smi works), and the Vulkan loader, which ships
# with the graphics driver of whichever GPU the host has — that is what lets the
# Vulkan plugin accelerate an AMD GPU without a ROCm runtime in the package.
# Everything else a shipped binary needs must travel inside the package.
#
# One definition, sourced by both the vendoring step (which copies what is
# missing) and the release gate (which refuses a package still missing any), so the
# two can never disagree about what "self-contained" means.
#
# Usage: . modeld-base-soname-allowlist.sh; base_os_or_driver <soname>

base_os_or_driver() {
  case "$1" in
    libc.so*|libm.so*|libdl.so*|libpthread.so*|librt.so*|libgcc_s.so*|libstdc++.so*|ld-linux*|libresolv.so*|libutil.so*|libmvec.so*) return 0 ;;
    libEGL.so*|libGL.so*|libGLdispatch.so*|libGLX.so*|libdrm.so*|libnuma.so*) return 0 ;;
    libxml2.so*|libz.so*|libzstd.so*|libssl.so*|libcrypto.so*) return 0 ;;
    libcuda.so*|libnvidia-*|libcudadebugger.so*) return 0 ;;
    libvulkan.so*) return 0 ;;
  esac
  return 1
}
