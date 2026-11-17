---
title: Building the native inference worker
---

# Building the native inference worker

`make build` or `task build` builds the portable Runtime CLI. The inference
worker is a separate native executable. Runtime connects to it over gRPC on
loopback. The [operator guide](../modeld.md) describes the owner, child workers
and session lifecycle.

Run `make modeld-help` for native build and packaging targets. A local native build needs
Go, GNU Make, Bash, GNU coreutils, CMake, a C++ compiler, the pinned llama.cpp sources, and the OpenVINO SDK
on platforms that include that backend:

```sh
make deps-modeld
task modeld:build
```

On macOS, use `make deps-llamacpp-ref` in place of `make deps-modeld`; the worker
uses llama.cpp and Metal there. The pinned versions and native flags live in
`mk/` and the backend Makefiles. Windows native releases use the MSVC
dependency-bundle path; they are not Linux cross-builds. Release packaging
requires Go 1.27.2 or newer and Visual Studio's LLVM x64 tools. MSVC produces the
llama.cpp/CUDA bundle, and LLVM targets the same MSVC ABI for Go/cgo. The bundle
ABI is part of its fingerprint, and the package gate rejects a mismatched ABI
or a missing OpenVINO GenAI import library before linking.

OpenVINO runtime, GenAI and tokenizer wheels must match the pinned C++ headers.
`make deps-openvino` installs those versions; `make -f Makefile.openvino check-sdk`
checks an existing SDK. A newer wheel can link successfully against older headers
and crash during inference. Dependency bundles carry the verified SDK version;
rebuild older bundles that lack it rather than copying the marker into them.

On Linux hosts with a RAM-backed `/tmp`, use a disk-backed temporary directory
for native compilation, for example `TMPDIR=/var/tmp make deps-modeld`. Keep
build directories on disk too. CUDA compilation can exhaust a small tmpfs even
when the destination filesystem has ample free space. The CUDA toolkit must
provide `nvcc` on `PATH`; use a host C++ compiler supported by that toolkit.
After building, verify inference on the GPU: a successful build alone does not
establish driver compatibility or GPU offload.

On Linux and macOS, `task dev-install` installs the CLI and worker together. It builds a relocatable
modeld bundle with every backend supported on the current platform, installs it
below the managed modeld data root, updates the same current pointer used by a
downloaded release, and exposes the newly built CLI at the path the shell actually
resolves. On Linux it includes CUDA automatically when `nvcc` is on `PATH` or at
`/usr/local/cuda/bin/nvcc`. `NVCC_CCBIN` selects a compatible host compiler when
the system compiler is newer than the CUDA toolkit supports.

`task dev-link` remains the fast CLI-only loop. When using an unpackaged worker
instead, set `CONTENOX_MODELD_BIN` to the absolute path of `bin/modeld`
(`bin/modeld.exe` on Windows). On Windows `task dev-install` installs the CLI. Point `CONTENOX_MODELD_BIN` to the launcher from the native package for a local development run. `contenox-runtime modeld status` probes its lease and
endpoint. Models live below the worker data root, which defaults to `~/.contenox`;
`CONTENOX_DATA_ROOT` selects another root.

## Native release packages

`task modeld:release` builds and checks the worker for the current platform. On
Linux it builds in the pinned CUDA container and includes the Vulkan plugin.
On macOS it targets Apple Silicon and checks for Metal. On Windows it uses the
MSVC, CUDA and OpenVINO toolchains. Use `task modeld:release CUDA_VERSION=13.4`
for the Visual Studio 18 / CUDA 13.4 toolchain.

To prepare and share a dependency bundle for a matching build host:

```sh
make build-modeld
make bundle-modeld-deps
make push-modeld-deps MODELD_DEPS_S3_URI=s3://YOUR-BUCKET/modeld-deps
```

To consume a prepared dependency bundle on its matching platform:

```sh
task modeld:package -- MODELD_DEPS_ROOT=/absolute/path/to/dependency-bundle
```

The packager checks required libraries, links the worker, runs the packaged
`version --json` smoke probe, and creates the archive, checksum, and index metadata
under `dist/`. It refuses to silently omit OpenVINO when required by the selected
profile. `MODELD_RELEASE_OPENVINO=0` explicitly selects a llama-only profile.

To publish through the existing store mechanism:

```sh
make push-modeld-release MODELD_RELEASE_S3_URI=s3://YOUR-BUCKET/modeld
```

Store arguments also accept local directories for offline assembly and testing.
The release prefix must be exposed over HTTP for installation. Then users run:

```sh
contenox-runtime modeld install --base-url https://YOUR-RELEASE-HOST/modeld
```

The installer reads `index.json`, chooses a compatible stable worker, verifies its
checksum and transport/backend report, and updates the managed installation
pointer. Compatibility is based on transport protocol and backend capability;
the worker need not have the same version string as the CLI. Supply the HTTP origin containing your release index with `--base-url`.

The platform matrix in `.github/workflows/release.yml` remains the supported CLI
matrix. `task modeld:check` compiles worker control paths for that same matrix
without native engines. This is a compile check, not evidence that native
libraries load or inference works on those machines. Native package smoke and
backend tests must run on the corresponding platform with its actual SDKs and
models before publishing that worker.

To verify the actual package installation using a local release store:

```sh
make push-modeld-release MODELD_RELEASE_S3_URI=/tmp/modeld-release-store
MODELD_TEST_RELEASE_DIR=/tmp/modeld-release-store go test ./internal/modeldinstall -run '^TestSystem_EnsureInstalled_NativeRelease$' -count=1
```

This starts a local HTTP server, downloads and checks the archive, probes the
worker after relocation into a temporary managed installation, and checks reuse.
It does not load a model or establish inference correctness.

## Developer checks

Run `make modeld-test` for the Go transport, shared owner, supervisor, native
provider, installer and Runtime integration packages. It includes historical
wire tests that predate the `TestUnit_` naming convention.

On a Linux build host, run `python3 scripts/test-modeld-linux-packaging.py` to
check the native package closure. Exercise both child-worker backends using
`make modeld-test-native CONTENOX_TEST_MODELD_BIN=/absolute/path/to/modeld`.
This checks backend selection and process lifecycle. Run the inference suites
with the model fixtures on the same host:

```sh
make -f Makefile.llamacpp-direct deps-tiny-model
make test-llamacpp-direct
make -f Makefile.openvino test-genai
```

The Linux Rust tests exercise the native worker through the CLI and ACP. From
the repository root, point them at the built CLI, the packaged worker launcher
and the small GGUF downloaded above:

```sh
export CONTENOX_BIN="$PWD/bin/contenox-runtime"
export CONTENOX_NATIVE_MODELD_BIN="$PWD/bin/dist/modeld"
export CONTENOX_LLAMA_TINY_GGUF="$PWD/.llamacpp-runtime/models/fastthink-0.5b-tiny-q2_k.gguf"
cargo test --manifest-path tools/contenox-e2e/Cargo.toml --test modeld_native --test modeld_worker -- --ignored
```

Use `CONTENOX_MODELD_BASE_URL` when testing worker installation from your HTTP
release index. A locally built worker can be selected with `CONTENOX_MODELD_BIN`.
