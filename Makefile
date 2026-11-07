# Oversight: agent oversight API (Go) + <oversight-app> micro frontend (React web component)
SHELL := /bin/bash
PORT ?= $(shell grep -E '^OVERSIGHT_PORT=' .env 2>/dev/null | cut -d= -f2 || true)
PORT := $(or $(PORT),8080)
VERSION := $(shell cat VERSION)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null)
API_URL ?= http://127.0.0.1:$(PORT)

.PHONY: help setup login check deps api web dev build serve test clean register-judge openapi release version

help:  ## Show targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-8s %s\n", $$1, $$2}'

setup: ## Device-code login (if needed), discover + write .env, verify access
	@scripts/setup.sh

login: ## Force a fresh `az login --use-device-code`
	@scripts/setup.sh --login

check: ## Verify login and .env without prompting
	@scripts/setup.sh --check

deps: ## Install Go and npm dependencies
	cd api && go mod download
	cd web && npm install --no-fund --no-audit

api: ## Run the API on :$(PORT)
	cd api && OVERSIGHT_DATA_DIR=../data go run ./cmd/oversight

web: ## Run the frontend dev server on :5180
	cd web && VITE_OVERSIGHT_API=$(API_URL) npm run dev

dev: ## Run API + frontend together (Ctrl-C stops both)
	@test -f .env || { echo "No .env yet: run 'make setup' first"; exit 1; }
	@test -d web/node_modules || (cd web && npm install --no-fund --no-audit)
	@trap 'kill 0' EXIT INT TERM; \
	  (cd api && OVERSIGHT_DATA_DIR=../data go run ./cmd/oversight) & \
	  (cd web && VITE_OVERSIGHT_API=$(API_URL) npm run dev -- --clearScreen false) & \
	  sleep 3; echo; echo "  API:      $(API_URL)/api/health"; echo "  Frontend: http://localhost:5180"; echo; \
	  wait

openapi: ## Regenerate the OpenAPI spec served at /api/docs
	python3 scripts/gen_openapi.py api/internal/server/openapi.json

release: ## Bump versions, test, commit and tag (make release V=0.2.0); push the tag to publish
	@test -n "$(V)" || { echo "usage: make release V=0.2.0"; exit 2; }
	@scripts/release.sh $(V)

version: ## Print the current version
	@echo $(VERSION)

register-judge: ## Register the judge in Foundry as an external agent (oversight-judge)
	cd api && OVERSIGHT_DATA_DIR=../data go run ./cmd/oversight register-judge

build: ## Build the API binary and the web component bundle
	cd api && go build -ldflags "$(LDFLAGS)" -o bin/oversight ./cmd/oversight
	cd web && npm run build

serve: build ## Run the built API, also serving the frontend at /
	cd api && OVERSIGHT_DATA_DIR=../data OVERSIGHT_WEB_DIR=../web/dist ./bin/oversight

test: ## Go tests + frontend typecheck
	cd api && go vet ./... && go test ./...
	cd web && npm run typecheck

clean:
	rm -rf api/bin web/dist
