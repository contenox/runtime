.PHONY: core-test-unit core-test-system core-test compose-wipe libs-test benchmarks run build down logs ui-install ui-package ui-build ui-run api-test api-test-logs api-test-docker api-init wait-for-server
DEFAULT_ADMIN_USER ?= admin@admin.com
DEFAULT_CORE_VERSION ?= dev-demo

core-test-unit:
	GOMAXPROCS=4 go test -C ./core/ -run '^TestUnit_' ./...

core-test-system:
	GOMAXPROCS=4 go test -C ./core/ -run '^TestSystem_' ./...

core-test:
	GOMAXPROCS=4 go test -C ./core/ ./...

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
	docker compose logs -f runtime-mvp

compose-wipe:
	docker compose down --volumes --rmi all

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
	yarn workspaces focus @contenox/ui frontend

ui-package: ui-install
	yarn workspace @contenox/ui build

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

api-test-logs: run wait-for-server
	. apitests/.venv/bin/activate && pytest --log-cli-level=DEBUG --capture=no -v apitests


api-test-docker:
	docker build -f Dockerfile.apitests -t contenox-apitests .
	docker run --rm --network=host contenox-apitests

proto:
	protoc --go_out=. --go_opt=paths=source_relative     --go-grpc_out=. --go-grpc_opt=paths=source_relative gateway/tokenizerapi/proto/tokenizerapi.proto
