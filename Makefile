PROJECT_ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
# Read with make itself (no tr / shell redirects) so these work from PowerShell and cmd too.
VERSION := $(strip $(file < $(PROJECT_ROOT)runtime/version/version.txt))
COMMIT ?= $(or $(shell git -C $(PROJECT_ROOT) rev-parse --verify --quiet HEAD),unknown)
DIST := $(PROJECT_ROOT)dist/$(VERSION)
RUNTIME_S3_URI ?=

# Native binaries need .exe on Windows hosts.
ifeq ($(OS),Windows_NT)
EXE := .exe
endif
BIN := $(PROJECT_ROOT)bin/contenox-runtime$(EXE)

# Developer targets (help, build, build-windows, test, test-unit, lint, clean, dev-install,
# dev-uninstall) avoid inline VAR=value prefixes and shell builtins, so they run under cmd.exe
# (make from PowerShell) as well as sh. The integration and release targets are bash scripts:
# run them from a Unix shell (Git Bash, WSL, Linux, macOS).

.PHONY: help build build-windows test test-unit test-contenox-help test-api acp-conformance rust-e2e lint release-assets release-check release clean dev-install dev-uninstall

# $(info) prints the same in every shell (no echo quoting); the recipe is a silent no-op in both.
ifeq ($(or $(MAKECMDGOALS),help),help)
$(info Developer targets (PowerShell, cmd and Unix shells):)
$(info   build              build bin/contenox-runtime (.exe on Windows))
$(info   build-windows      cross-build bin/contenox-runtime-windows-amd64.exe)
$(info   test               go test ./...)
$(info   test-unit          go test -short -run "^TestUnit_" ./...)
$(info   lint               go vet ./...)
$(info   clean              remove bin/ and dist/)
$(info   dev-install        build and install contenox-runtime to the per-user bin dir (~/.local/bin, DEV_BIN_DIR overrides))
$(info   dev-uninstall      remove the dev-installed contenox-runtime)
$(info )
$(info Unix shell only (Git Bash, WSL, Linux, macOS):)
$(info   test-contenox-help CLI help smoke)
$(info   test-api           Python API tests)
$(info   acp-conformance    ACP conformance suite)
$(info   rust-e2e           Rust black-box end-to-end suite)
$(info   release-assets     build 4 raw binaries + SHA256SUMS + manifest)
$(info   release-check      run checks, then release-assets)
$(info   release            release-check + immutable S3 upload; requires RUNTIME_S3_URI=s3://bucket/prefix)
endif

help:
	@cd .

build: export CGO_ENABLED = 0
build:
	go build -o $(BIN) $(PROJECT_ROOT)cmd/contenox

build-windows: export CGO_ENABLED = 0
build-windows: export GOOS = windows
build-windows: export GOARCH = amd64
build-windows:
	go build -o $(PROJECT_ROOT)bin/contenox-runtime-windows-amd64.exe $(PROJECT_ROOT)cmd/contenox

test:
	go test -C $(PROJECT_ROOT) ./...

test-unit: export GOMAXPROCS = 4
test-unit:
	go test -C $(PROJECT_ROOT) -short -timeout 15m -run "^TestUnit_" ./...

test-contenox-help: build
	@chmod +x $(PROJECT_ROOT)scripts/verify_cli_help.sh
	@CONTENOX_BIN=$(BIN) $(PROJECT_ROOT)scripts/verify_cli_help.sh

test-api: build
	@bash $(PROJECT_ROOT)scripts/run_apitests.sh

ACP_VALIDATOR_BIN ?= $(wildcard $(PROJECT_ROOT)tools/acp-validator/target/debug/acp-validator)

acp-conformance: build
	@test -n "$(ACP_VALIDATOR_BIN)" || { echo "set ACP_VALIDATOR_BIN=/path/to/acp-validator"; exit 1; }
	@CONTENOX_BIN=$(BIN) ACP_VALIDATOR_BIN=$(ACP_VALIDATOR_BIN) bash $(PROJECT_ROOT)scripts/acp_conformance.sh

rust-e2e: build
	@tmp=$$(mktemp -d /tmp/contenox-e2e.XXXXXX); trap 'rm -rf "$$tmp"' EXIT; \
		cd $(PROJECT_ROOT)tools/contenox-e2e && TMPDIR=$$tmp cargo test -- --test-threads=1

lint:
	go vet -C $(PROJECT_ROOT) ./...

release-assets:
	@rm -rf $(DIST)
	@mkdir -p $(DIST)
	@for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do \
		goos=$${target%/*}; goarch=$${target#*/}; ext=""; \
		if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -o "$(DIST)/contenox-runtime-$$goos-$$goarch$$ext" $(PROJECT_ROOT)cmd/contenox || exit 1; \
	done
	@cd $(DIST) && sha256sum $$(find . -maxdepth 1 -type f -name 'contenox-runtime-*' -printf '%f\n' | LC_ALL=C sort) > SHA256SUMS
	@cd $(DIST) && sha256sum -c SHA256SUMS
	@python3 $(PROJECT_ROOT)scripts/release-manifest.py --dir $(DIST) --version $(VERSION) --tag $(VERSION) --commit "$(COMMIT)" --output $(DIST)/runtime-manifest.json
	@echo "release assets: $(DIST)"

release-check:
	$(MAKE) test-unit
	$(MAKE) test-contenox-help
	$(MAKE) release-assets

release:
	@test -z "$$(git status --porcelain)" || { echo "working tree is not clean"; exit 1; }
	@test -n "$(RUNTIME_S3_URI)" || { echo "set RUNTIME_S3_URI=s3://bucket/prefix"; exit 1; }
	$(MAKE) release-check
	@if aws s3 ls "$(RUNTIME_S3_URI)/$(VERSION)/" | grep -q .; then \
		echo "$(RUNTIME_S3_URI)/$(VERSION)/ already exists"; exit 1; \
	fi
	aws s3 cp --recursive "$(DIST)" "$(RUNTIME_S3_URI)/$(VERSION)/" --only-show-errors
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
		aws s3 cp --recursive "$(RUNTIME_S3_URI)/$(VERSION)/" "$$tmp" --only-show-errors; \
		cd "$$tmp" && sha256sum -c SHA256SUMS; \
		echo "released $(VERSION) to $(RUNTIME_S3_URI)/$(VERSION)/"

# The helper removes paths portably (rm -rf does not exist in cmd.exe).
clean:
	go run -C $(PROJECT_ROOT) ./tools/devtool clean $(PROJECT_ROOT)bin $(PROJECT_ROOT)dist

# Install a development build where local tools (e.g. the desktop harness) look for
# contenox-runtime: the per-user bin directory on PATH. Portable via a small Go helper, so it
# works the same from PowerShell, cmd and Unix shells.
dev-install:
	go run -C $(PROJECT_ROOT) ./tools/devtool install

dev-uninstall:
	go run -C $(PROJECT_ROOT) ./tools/devtool uninstall
