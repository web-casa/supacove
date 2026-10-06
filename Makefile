BIN := bin/supabackup
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)

# Respect GOBIN when set — installing and probing must target the same path
# (review round 4, P1-14). `go env GOBIN` is empty when unset.
GOBIN_DIR := $(shell go env GOBIN 2>/dev/null)
ifeq ($(GOBIN_DIR),)
GOBIN_DIR := $(shell go env GOPATH)/bin
else
GOBIN_DIR := $(GOBIN_DIR)
endif
OAPI := $(GOBIN_DIR)/oapi-codegen

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
	touch backend/internal/web/dist/.gitkeep  # keep the committed embed marker

build: frontend backend ## Build everything: SPA first, then the Go binary embedding it

api-gen: ## Regenerate server + frontend API types from api/openapi.yaml
	@test -x "$(OAPI)" || GOBIN="$(GOBIN_DIR)" go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0
	$(OAPI) -config api/cfg.yaml api/openapi.yaml
	cd frontend && npm run gen:api

## Contract compatibility gate: no BREAKING change vs the committed
## baseline (api/openapi-baseline.yaml, refreshed at each release tag).
api-breaking:
	@command -v oasdiff >/dev/null 2>&1 || { echo "oasdiff not installed: go install github.com/oasdiff/oasdiff@v1.11.5"; exit 1; }
	oasdiff breaking --fail-on ERR api/openapi-baseline.yaml api/openapi.yaml
	@echo "contract: no breaking changes vs baseline"

## External Prometheus format validation of the live /metrics exposition.
## Needs a running instance; dumps the scrape and pipes it to promtool.
metrics-check:
	@command -v promtool >/dev/null 2>&1 || { echo "promtool not installed (or use docker: see below)"; exit 1; }
	@curl -sf -b "$${SB_METRICS_COOKIE:?set SB_METRICS_COOKIE to a logged-in cookie jar}" 	  "$${SB_BASE_URL:-http://127.0.0.1:8080}/metrics" | promtool check metrics

api-check: ## Fail if generated code drifted from the OpenAPI contract
	@test -x "$(OAPI)" || GOBIN="$(GOBIN_DIR)" go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0
	@test -x frontend/node_modules/.bin/openapi-typescript || (cd frontend && npm ci --no-fund --no-audit)
	$(OAPI) -config api/cfg.yaml api/openapi.yaml
	cd frontend && npm run gen:api
	git diff --exit-code -- backend/internal/api api frontend/src/api/schema.d.ts || \
		(echo "ERROR: generated code drifted from api/openapi.yaml — run 'make api-gen' and commit" && exit 1)

dev: ## Start the full dev environment (app + PostgreSQL + MinIO)
	docker compose up --build

test: ## Run backend tests with the race detector
	go test -race ./backend/...

## Static analysis: golangci-lint (backend) + eslint/stylelint (frontend).
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0"; exit 1; }
	golangci-lint run ./...
	cd frontend && npm run lint && npm run stylelint

lint: ## gofmt + go vet
	@test -z "$$(gofmt -l backend | grep -v api.gen.go)" || (gofmt -l backend | grep -v api.gen.go && echo "run gofmt -w" && exit 1)
	go vet ./backend/...

check: lint frontend api-check test ## Everything CI runs; frontend sets up deps before api-check
	cd frontend && npm run lint
	$(MAKE) build

clean:
	rm -rf bin frontend/dist frontend/node_modules
