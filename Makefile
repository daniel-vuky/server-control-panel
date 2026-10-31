VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
COMPOSE := docker compose -f compose.dev.yaml
RUN := $(COMPOSE) run --rm tools

.DEFAULT_GOAL := dev
.PHONY: dev-image setup dev shell test lint fmt typegen build image deploy-test deploy-e2e ci fixtures-up fixtures-down

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

## lint: golangci-lint, Biome (warnings fail), the TypeScript typecheck and shellcheck on the deploy scripts
lint: setup
	$(RUN) sh -c 'golangci-lint run && pnpm -C web lint && pnpm -C web typecheck && shellcheck -x deploy/*.sh deploy/test/*.sh deploy/test/fake-docker'

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

## image: build the production image scp-panel:local (host docker; not the dev toolchain)
image:
	docker build --build-arg VERSION=$(VERSION) --build-arg REVISION=$$(git rev-parse HEAD 2>/dev/null || echo unknown) -t scp-panel:local .

## deploy-test: run the deploy script tests, then render the compose project and check it with docker compose config, in a Linux container (never on the host)
deploy-test:
	docker run --rm -v "$(CURDIR)":/src:ro -w /src docker:29-cli sh -c 'apk add --no-cache bash coreutils >/dev/null && bash deploy/test/run.sh && bash deploy/test/compose-config.sh'

## deploy-e2e: install, update and uninstall end to end in a throwaway docker:29-dind with a local registry (manual: needs privileged containers; not in CI)
deploy-e2e:
	bash deploy/test/e2e.sh

## fixtures-up: start the sample compose projects in dev/workspaces with the host's docker (shop web only so it is Partial, notes, ledger with Postgres and MariaDB; blog stays stopped)
fixtures-up:
	docker compose -f dev/workspaces/shop/compose.yaml up -d web
	docker compose -f dev/workspaces/notes/compose.yml up -d
	docker compose -f dev/workspaces/ledger/compose.yaml up -d --wait

## fixtures-down: stop and remove every sample compose project (errors ignored)
fixtures-down:
	-docker compose -f dev/workspaces/shop/compose.yaml down
	-docker compose -f dev/workspaces/blog/docker-compose.yml down
	-docker compose -f dev/workspaces/notes/compose.yml down
	-docker compose -f dev/workspaces/ledger/compose.yaml down -v
	-docker compose -f dev/workspaces/clients/acme/web/compose.yaml down

## ci: everything CI runs, ending with a check that the generated types are committed
ci: setup lint test build
	$(RUN) sh -c 'go tool tygo generate && git diff --exit-code web/src/api/types.gen.ts'
