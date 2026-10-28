VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
COMPOSE := docker compose -f compose.dev.yaml
RUN := $(COMPOSE) run --rm tools

.DEFAULT_GOAL := dev
.PHONY: dev-image setup dev shell test lint fmt typegen build ci

## dev-image: build the dev toolchain image (cached after the first run); SKIP_DEV_IMAGE=1 reuses a prebuilt scp-dev (CI)
dev-image:
	@if [ -n "$(SKIP_DEV_IMAGE)" ]; then echo "SKIP_DEV_IMAGE set: using the prebuilt scp-dev image"; else $(COMPOSE) build api; fi

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

## typegen: regenerate web/src/api/types.gen.ts from the Go API types
typegen: dev-image
	$(RUN) go tool tygo generate

## build: build the web app, then the single release binary into bin/panel
# emptyOutDir wipes internal/web/dist, so the .gitkeep that keeps go:embed compiling is recreated.
build: setup
	$(RUN) sh -c 'pnpm -C web build && touch internal/web/dist/.gitkeep && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/panel ./cmd/panel'

## ci: everything CI runs, ending with a check that the generated types are committed
ci: setup lint test build
	$(RUN) sh -c 'go tool tygo generate && git diff --exit-code web/src/api/types.gen.ts'
