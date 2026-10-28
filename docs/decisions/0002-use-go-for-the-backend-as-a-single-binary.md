# 2. Use Go for the backend as a single binary

Date: 2026-10-28

## Status

Accepted

## Context

The backend does most of the work. It runs docker compose commands, samples server and container metrics every 60 seconds even when nobody has the panel open, streams logs and live values, runs scheduled backups and checks domains. It runs on the same 8 GB Ubuntu server it manages, and the owner wants it to be no burden there. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: Go

- Pro: estimated 20–40 MB RSS at idle and low CPU.
- Pro: compiles to one static binary with no runtime dependency.
- Pro: `os/exec` never goes through a shell. Goroutines suit streams and tickers.
- Pro: official or well-kept libraries exist for Google Drive, JWT/JWKS, cron and pure-Go SQLite.
- Con: a second language next to the TypeScript frontend.

### Option B: Node.js + Fastify

- Pro: one language, with types shared directly with the frontend.
- Con: an estimated 60–100 MB RSS at idle.
- Con: needs a Node runtime and `node_modules` in the image.

### Option C: Python + FastAPI

- Con: heavier than Go, and it offers no shared-language benefit either.

## Decision

The backend is written in **Go** (current stable release) and built as **one binary** that also serves the frontend.

1. HTTP uses the standard library `net/http` (Go 1.22+ routing patterns) and small hand-written middleware. No web framework.
2. The Vite build output is embedded with `embed.FS` and served by the same process and port. Unknown non-API paths fall back to `index.html`.
3. API request and response types live in `internal/api`. They are generated into `web/src/api/types.gen.ts` with `tygo`, and CI fails if the generated file is stale.
4. Lint uses `golangci-lint` (v2 config); tests use `go test`.
5. The build uses `CGO_ENABLED=0`, so the binary is static and cross-compiles to `linux/amd64` and `linux/arm64`.

## Consequences

**Easier:**

- The panel stays small and fast. The runtime image holds a single binary.
- The panel does not depend on any Node version installed on the server.

**Harder:**

- Contributors work in two languages.
- Shared types depend on the `tygo` generation step instead of a plain import.
