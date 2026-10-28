PROJECT_ROOT := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
VERSION := $(shell tr -d '\r\n' < $(PROJECT_ROOT)runtime/version/version.txt)
COMMIT ?= $(shell git rev-parse --verify --quiet HEAD 2>/dev/null || echo unknown)
DIST := $(PROJECT_ROOT)dist/$(VERSION)
RUNTIME_S3_URI ?=

.PHONY: help build build-windows test test-unit test-contenox-help test-api acp-conformance rust-e2e lint release-assets release-check release clean

help:
	@echo "build              build bin/contenox-runtime"
	@echo "build-windows      cross-build bin/contenox-runtime-windows-amd64.exe"
	@echo "test               go test ./..."
	@echo "test-unit          go test -short -run '^TestUnit_' ./..."
	@echo "test-contenox-help CLI help smoke"
	@echo "test-api           Python API tests"
	@echo "acp-conformance    ACP conformance suite"
	@echo "rust-e2e           Rust black-box end-to-end suite"
	@echo "lint               go vet ./..."
	@echo "release-assets     build 4 raw binaries + SHA256SUMS + manifest"
	@echo "release-check      run checks, then release-assets"
	@echo "release            release-check + immutable S3 upload"
	@echo "                   requires RUNTIME_S3_URI=s3://bucket/prefix"
	@echo "clean              remove bin/ and dist/"

build:
	CGO_ENABLED=0 go build -o $(PROJECT_ROOT)bin/contenox-runtime $(PROJECT_ROOT)cmd/contenox

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $(PROJECT_ROOT)bin/contenox-runtime-windows-amd64.exe $(PROJECT_ROOT)cmd/contenox

test:
	go test -C $(PROJECT_ROOT) ./...

test-unit:
	GOMAXPROCS=4 go test -C $(PROJECT_ROOT) -short -timeout 15m -run '^TestUnit_' ./...

test-contenox-help: build
	@chmod +x $(PROJECT_ROOT)scripts/verify_cli_help.sh
	@CONTENOX_BIN=$(PROJECT_ROOT)bin/contenox-runtime $(PROJECT_ROOT)scripts/verify_cli_help.sh

test-api: build
	@bash $(PROJECT_ROOT)scripts/run_apitests.sh

ACP_VALIDATOR_BIN ?= $(wildcard $(PROJECT_ROOT)tools/acp-validator/target/debug/acp-validator)

acp-conformance: build
	@test -n "$(ACP_VALIDATOR_BIN)" || { echo "set ACP_VALIDATOR_BIN=/path/to/acp-validator"; exit 1; }
	@CONTENOX_BIN=$(PROJECT_ROOT)bin/contenox-runtime ACP_VALIDATOR_BIN=$(ACP_VALIDATOR_BIN) bash $(PROJECT_ROOT)scripts/acp_conformance.sh

rust-e2e: build
	@tmp=$$(mktemp -d /tmp/contenox-e2e.XXXXXX); trap 'rm -rf "$$tmp"' EXIT; \
		cd $(PROJECT_ROOT)tools/contenox-e2e && TMPDIR=$$tmp cargo test -- --test-threads=1

lint:
	cd $(PROJECT_ROOT) && go vet ./...

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

clean:
	rm -rf $(PROJECT_ROOT)bin $(PROJECT_ROOT)dist
