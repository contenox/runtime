#!/usr/bin/env bash
# Fetch the pinned LunarG Vulkan SDK for the one build-time tool the Vulkan ggml
# plugin needs: glslc.
#
# llama.cpp precompiles every shader into libggml-vulkan.so, so glslc is required
# to build the plugin and nothing is required to run it — the plugin links only
# the Vulkan loader, which the target host's graphics driver installs. The Ubuntu
# baseline the release links against has no glslc package, so the tool comes from
# the SDK; no part of the SDK is redistributed.
#
# Usage: modeld-vulkan-sdk.sh <dest-dir>
# Prints the SDK root (the directory to put on PATH and CMAKE_PREFIX_PATH).
set -euo pipefail

VULKAN_SDK_VERSION=1.4.357.0
VULKAN_SDK_SHA256=0f09bf6a0625e346bf004be70b92907e934a4c76606b323441b2baf3a5a0e66d
VULKAN_SDK_URL="https://sdk.lunarg.com/sdk/download/${VULKAN_SDK_VERSION}/linux/vulkansdk-linux-x86_64-${VULKAN_SDK_VERSION}.tar.xz"

dest=${1:?usage: modeld-vulkan-sdk.sh <dest-dir>}
root="$dest/$VULKAN_SDK_VERSION/x86_64"

if [ -x "$root/bin/glslc" ]; then
  echo "$root"
  exit 0
fi

mkdir -p "$dest"
archive="$dest/vulkansdk-$VULKAN_SDK_VERSION.tar.xz"
if [ ! -f "$archive" ]; then
  echo "modeld-vulkan-sdk: fetching $VULKAN_SDK_URL" >&2
  curl -fL --retry 3 -o "$archive.part" "$VULKAN_SDK_URL"
  mv "$archive.part" "$archive"
fi

echo "$VULKAN_SDK_SHA256  $archive" | sha256sum -c - >&2
tar -xf "$archive" -C "$dest"
[ -x "$root/bin/glslc" ] || { echo "modeld-vulkan-sdk: $root/bin/glslc is missing after extracting $archive" >&2; exit 1; }
echo "$root"
