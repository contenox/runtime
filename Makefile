.PHONY: core-test core-smoketest libs-test benchmarks run build down logs ui-install ui-package ui-build ui-run api-test api-init wait-for-server
DEFAULT_ADMIN_USER ?= admin@admin.com
DEFAULT_CORE_VERSION ?= dev-demo

core-test:
	go test -C ./core/ ./...

core-smoketest:
	GOMAXPROCS=4 SMOKETESTS=true go test -C ./core/ ./...

libs-test:
	for d in libs/*; do \
	  if [ -f "$$d/go.mod" ]; then \
	    echo "=== Running tests in $$d ==="; \
	    (cd "$$d" && go test ./...); \
	  fi; \
	done

core-benchmark:
	go test -C ./core -bench=. -run=^$ -benchmem ./...

build:
	docker compose build --build-arg DEFAULT_ADMIN_USER=$(DEFAULT_ADMIN_USER) --build-arg CORE_VERSION=$(DEFAULT_CORE_VERSION)

down:
	docker compose down

run: down build
	docker compose up -d

logs: run
	docker compose logs -f backend

yarn-wipe:
	echo "Removing Yarn PnP files..."
	rm -f .pnp.cjs .pnp.loader.mjs
	echo "Removing Yarn state files and unplugged directory..."
	rm -rf .yarn/unplugged
	rm -f .yarn/install-state.gz
	echo "Removing node_modules directories..."
	rm -rf node_modules packages/*/node_modules frontend/node_modules
	echo "Running yarn install..."
	yarn install

ui-install:
	yarn workspaces focus @cate/ui frontend

ui-package: ui-install
	yarn workspace @cate/ui build

ui-build: ui-package
	yarn install
	yarn build

ui-run: ui-build
	yarn workspace frontend dev --host

api-test-init:
	python3 -m venv apitests/.venv
	. apitests/.venv/bin/activate && pip install -r apitests/requirements.txt

wait-for-server:
	@echo "Waiting for server to be ready..."
	@until wget --spider -q http://localhost:8081/api/health; do \
		echo "Server not yet available, waiting..."; \
		sleep 2; \
	done
	@echo "Server is up!"

api-test: run wait-for-server
	. apitests/.venv/bin/activate && pytest apitests/


proto:
	protoc --go_out=. --go_opt=paths=source_relative     --go-grpc_out=. --go-grpc_opt=paths=source_relative core/serverapi/tokenizerapi/proto/tokenizerapi.proto
