BIN := bin/supabackup
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)

OAPI := $(shell go env GOPATH)/bin/oapi-codegen

.PHONY: help build backend frontend api-gen api-check dev test lint check clean
.NOTPARALLEL:

help:
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

backend: ## Build the backend binary (embeds the current web dist)
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(BIN) ./backend/cmd/supabackup

frontend: ## Build the SPA and copy it into the Go embed directory
	cd frontend && npm ci && npm run build
	rm -rf backend/internal/web/dist
	cp -r frontend/dist backend/internal/web/dist

build: frontend backend ## Build everything: SPA first, then the Go binary embedding it

api-gen: ## Regenerate server + frontend API types from api/openapi.yaml
		$(OAPI) -config api/cfg.yaml api/openapi.yaml
	cd frontend && npm run gen:api

api-check: ## Fail if generated code drifted from the OpenAPI contract
		$(OAPI) -config api/cfg.yaml api/openapi.yaml
	cd frontend && npm run gen:api
	git diff --exit-code -- backend/internal/api api frontend/src/api/schema.d.ts || \
		(echo "ERROR: generated code drifted from api/openapi.yaml — run 'make api-gen' and commit" && exit 1)

dev: ## Start the full dev environment (app + PostgreSQL + MinIO)
	docker compose up --build

test: ## Run backend tests with the race detector
	go test -race ./backend/...

lint: ## gofmt + go vet
	@test -z "$$(gofmt -l backend | grep -v api.gen.go)" || (gofmt -l backend | grep -v api.gen.go && echo "run gofmt -w" && exit 1)
	go vet ./backend/...

check: lint api-check test ## Everything CI runs
	cd frontend && npm ci && npm run lint && npm run build
	$(MAKE) build

clean:
	rm -rf bin frontend/dist frontend/node_modules
