COMPOSE := docker compose -f compose.dev.yaml
RUN := $(COMPOSE) run --rm tools

.DEFAULT_GOAL := dev
.PHONY: dev-image setup dev shell test lint fmt typegen build ci

## dev-image: build the dev toolchain image (cached after the first run)
dev-image:
	$(COMPOSE) build api

## setup: install web dependencies (frozen lockfile) into the node_modules volume
setup: dev-image
	$(RUN) pnpm -C web install --frozen-lockfile

## dev: start the Go (air) and Vite dev servers; http://localhost:5173
dev:
	$(COMPOSE) up --build api web

## shell: open a shell in the dev image
shell: dev-image
	$(RUN) bash

## test: Go tests, then web tests
test: setup
	$(RUN) sh -c 'go test ./... && go test -tags dev ./... && pnpm -C web test'

## lint: golangci-lint, Biome (warnings fail) and the TypeScript typecheck
lint: setup
	$(RUN) sh -c 'golangci-lint run && pnpm -C web lint && pnpm -C web typecheck'

## fmt: format Go and web sources
fmt: setup
	$(RUN) sh -c 'golangci-lint fmt && pnpm -C web format'

## The targets below get their recipes in later tasks.
typegen: dev-image
	@echo "typegen: added in a later task"

build: dev-image
	@echo "build: added in a later task"

ci: dev-image
	@echo "ci: added in a later task"
