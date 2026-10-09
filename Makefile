# Lead Intelligence Platform — developer entry points.
# Every target is safe to run from a clean checkout. `make help` lists them.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

ENV_FILE := .env
ifneq (,$(wildcard $(ENV_FILE)))
include $(ENV_FILE)
export
endif

GO ?= go
COMPOSE ?= docker compose
BIN := bin

# Every module is a separate Go module, so "./..." only works from inside one.
# These targets iterate explicitly rather than relying on a workspace wildcard,
# which `go build ./...` does not support.
MODULES := crawler discovery entity-resolution enrichment extractor \
           knowledge-graph lead-engine personalization platform \
           qualification research

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: up
up: ## Start Postgres (docker compose)
	$(COMPOSE) up -d --wait
	@echo "postgres ready"

.PHONY: down
down: ## Stop Postgres and remove volumes
	$(COMPOSE) down -v

.PHONY: migrate
migrate: ## Apply every module's migrations
	$(GO) run ./lead-engine/cmd/lead-engine migrate

.PHONY: crawler-migrate
crawler-migrate: ## Apply only the crawler module migrations
	$(GO) run ./crawler/cmd/crawld -migrate-only

.PHONY: test
test: ## Run every module's tests
	@for m in $(MODULES); do \
	  [ -d "$$m" ] || continue; \
	  has_go=$$(ls "$$m"/*.go "$$m"/*/*.go "$$m"/*/*/*.go 2>/dev/null | head -n1 || true); \
	  if [ -z "$$has_go" ]; then echo "==> $$m (no packages yet)"; continue; fi; \
	  echo "==> $$m"; (cd $$m && $(GO) test ./...) || exit 1; \
	done

.PHONY: test-race
test-race: ## Run tests with the race detector
	@for m in $(MODULES); do \
	  [ -d "$$m" ] || continue; \
	  has_go=$$(ls "$$m"/*.go "$$m"/*/*.go "$$m"/*/*/*.go 2>/dev/null | head -n1 || true); \
	  if [ -z "$$has_go" ]; then echo "==> $$m (no packages yet)"; continue; fi; \
	  echo "==> $$m"; (cd $$m && $(GO) test -race ./...) || exit 1; \
	done

.PHONY: cover
cover: ## Run tests with coverage per module
	@for m in $(MODULES); do \
	  [ -d "$$m" ] || continue; \
	  has_go=$$(ls "$$m"/*.go "$$m"/*/*.go "$$m"/*/*/*.go 2>/dev/null | head -n1 || true); \
	  if [ -z "$$has_go" ]; then echo "==> $$m (no packages yet)"; continue; fi; \
	  (cd $$m && $(GO) test -coverprofile=cover.out ./... >/dev/null) || exit 1; \
	  echo "==> $$m"; \
	  (cd $$m && $(GO) tool cover -func=cover.out | tail -n 1); \
	done

.PHONY: vet
vet: ## go vet across the workspace
	@for m in $(MODULES); do \
	  [ -d "$$m" ] || continue; \
	  has_go=$$(ls "$$m"/*.go "$$m"/*/*.go "$$m"/*/*/*.go 2>/dev/null | head -n1 || true); \
	  if [ -z "$$has_go" ]; then echo "==> $$m (no packages yet)"; continue; fi; \
	  echo "==> $$m"; (cd $$m && $(GO) vet ./...) || exit 1; \
	done

.PHONY: fmt
fmt: ## gofmt the workspace
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Fail if anything is unformatted
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

.PHONY: tidy
tidy: ## go mod tidy in every module
	@for m in $(MODULES); do \
	  [ -d "$$m" ] || continue; \
	  echo "==> $$m"; (cd $$m && $(GO) mod tidy); \
	done

.PHONY: build
build: ## Build every module binary into ./bin
	@mkdir -p $(BIN)
	@set -e; for b in crawler/cmd/crawld lead-engine/cmd/lead-engine; do \
	  m=$${b%%/*}; pkg=$${b#*/}; name=$${pkg##*/}; \
	  if [ -d "$$m/$$(dirname $$pkg)" ]; then \
	    echo "==> $$b"; \
	    (cd $$m && $(GO) build -o ../$(BIN)/$$name ./$$pkg); \
	  else \
	    echo "==> $$b (skipped: not implemented yet)"; \
	  fi; \
	done

.PHONY: verify
verify: fmt-check vet test ## Everything CI should run

.PHONY: run
run: ## Run the pipeline against ./icp.json (50 leads, local fixtures allowed)
	$(GO) run ./lead-engine/cmd/lead-engine run --icp ./icp.json --limit 50

.PHONY: serve
serve: ## Start the REST API on $UPVISTA_HTTP_ADDR
	$(GO) run ./lead-engine/cmd/lead-engine serve

.PHONY: crawld
crawld: ## Start the standalone crawler service (reads DATABASE_URL, HTTP_ADDR)
	$(GO) run ./crawler/cmd/crawld

.PHONY: crawl-smoke
crawl-smoke: ## Start crawld against a local target and exercise the API
	./scripts/smoke-crawler.sh
