# Photostead development tasks. Run `make help` for the list.
SHELL := /bin/bash
ENV_FILE := deploy/dev/.env
BIN := bin/photostead
PG := photostead-dev-postgres
# The Command Line Tools linker can lag behind the macOS SDK; Xcode's is current.
XCODE := /Applications/Xcode.app/Contents/Developer
ifneq ($(wildcard $(XCODE)),)
export DEVELOPER_DIR := $(XCODE)
endif
load-env = set -a; source $(ENV_FILE); set +a;

.PHONY: help env db-up db-down build web run test lint generate openapi status

help: ## show this list
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

env: ## create deploy/dev/.env with random secrets (once)
	@test -f $(ENV_FILE) && echo "$(ENV_FILE) already exists" || { \
	  pw=$$(openssl rand -hex 16); apw=$$(openssl rand -base64 18 | tr -d '/+='); \
	  sed -e "s|^PHOTOSTEAD_DB_PASSWORD=.*|PHOTOSTEAD_DB_PASSWORD=$$pw|" \
	      -e "s|^PHOTOSTEAD_ADMIN_PASSWORD=.*|PHOTOSTEAD_ADMIN_PASSWORD=$$apw|" deploy/dev/.env.example > $(ENV_FILE); \
	  chmod 600 $(ENV_FILE); echo "Wrote $(ENV_FILE); set PHOTOSTEAD_ADMIN_EMAIL in it"; }

db-up: ## start Postgres 18 + pgvector in Docker
	@$(load-env) docker start $(PG) >/dev/null 2>&1 || docker run -d --name $(PG) --restart unless-stopped \
	  -e POSTGRES_USER=photostead -e POSTGRES_PASSWORD="$$PHOTOSTEAD_DB_PASSWORD" -e POSTGRES_DB=photostead \
	  -p 127.0.0.1:5433:5432 --shm-size=512m -v photostead-dev-pgdata:/var/lib/postgresql pgvector/pgvector:pg18 >/dev/null
	@until docker exec $(PG) pg_isready -U photostead >/dev/null 2>&1; do sleep 1; done; echo "Postgres is ready"

db-down: ## stop Postgres (data is kept)
	docker stop $(PG)

generate: ## regenerate the type-checked SQL code
	cd server && sqlc generate

build: ## build the server binary
	cd server && go build -o ../$(BIN) ./cmd/photostead

web: ## build the web app
	cd web && npm install --no-audit --no-fund && npm run build

run: build ## run the server with the built web app
	@$(load-env) PHOTOSTEAD_WEB_DIR=web/dist PHOTOSTEAD_WATCH=$${PHOTOSTEAD_WATCH:-off} ./$(BIN) serve

test: ## run the server tests (the integration test needs `make db-up`)
	@$(load-env) cd server && go test ./...

lint: ## vet and format check
	cd server && go vet ./... && test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

openapi: build ## write the OpenAPI document to server/openapi/openapi.json
	@mkdir -p server/openapi && ./$(BIN) openapi > server/openapi/openapi.json && echo "server/openapi/openapi.json"

status: build ## files, renders and queued jobs
	@$(load-env) ./$(BIN) status
