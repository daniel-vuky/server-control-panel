COMPOSE := docker compose -f compose.dev.yaml
RUN := $(COMPOSE) run --rm tools

.PHONY: setup dev shell test lint fmt typegen build ci

## setup: install web dependencies (frozen lockfile) into the node_modules volume
setup:
	$(RUN) pnpm -C web install --frozen-lockfile

## dev: start the Go (air) and Vite dev servers; http://localhost:5173
dev:
	$(COMPOSE) up --build api web

## shell: open a shell in the dev image
shell:
	$(RUN) bash

## The targets below get their recipes in later tasks.
test:
	@echo "test: added in a later task"

lint:
	@echo "lint: added in a later task"

fmt:
	@echo "fmt: added in a later task"

typegen:
	@echo "typegen: added in a later task"

build:
	@echo "build: added in a later task"

ci:
	@echo "ci: added in a later task"
