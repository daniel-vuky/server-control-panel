# 9. Run the development toolchain in Docker

Date: 2026-10-28

## Status

Accepted

## Context

Developing the panel needs Go, golangci-lint, Node, pnpm and shellcheck. The owner does not want these installed natively on the development machine. Docker is already there. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: Native installs (Homebrew, nvm)

- Con: tools installed on the machine, with versions that drift from CI.

### Option B: A dev container image driven by `make`

- Pro: one image pins every tool version, shared by local development and CI.
- Con: commands run through Docker, with some file-sync overhead on macOS.

## Decision

1. A `Dockerfile.dev` builds the **dev image**: Go (the version in `go.mod`), golangci-lint v2, Node 22 with pnpm (via corepack) and shellcheck.
2. `compose.dev.yaml` runs the dev environment from that image:
   - the repo is bind-mounted;
   - **`node_modules`, the Go module cache and the Go build cache live in named volumes**, so Linux-native binaries never mix with the host's files;
   - the Vite dev server and the Go server share one network namespace, so Vite proxies `/api` to `127.0.0.1:8471`;
   - only port 5173 is published to the host.
3. **Every `make` target runs inside the dev image**, so the host needs only Docker and `make`:
   - `make dev` starts the environment;
   - `make test`, `make lint`, `make fmt`, `make typegen` and `make build` run one-off containers.
4. `make image` builds the production image from the host with the regular `Dockerfile` (see [ADR 8](0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)).

## Consequences

**Easier:**

- Nothing is installed natively.
- Tool versions are identical for every contributor.

**Harder:**

- The first run builds the dev image and fills the caches.
- Editors need their own Go or TypeScript tooling (or a dev-container integration) for in-editor features; the build itself does not.
