# server-control-panel

## Task board

Work is tracked on the GitHub Project board: https://github.com/users/daniel-vuky/projects/2 (project `2`, owner `daniel-vuky`).

Columns (the `Status` field): **Backlog → Ready → In progress → In review → Done**.

- Only pick up issues in **Ready**. Backlog items are not specified enough yet.
- One issue = one branch = one PR. Branches: `feat/<issue>-<slug>` or `fix/<issue>-<slug>`.
- PR bodies start with `Closes #<issue>` so merging closes the issue.
- Claude moves cards to In progress and In review; the user reviews and merges. Never merge PRs yourself.

Skills for the board:

- `work-board-item` — pick the next Ready issue and work it end to end, add an issue to the board, or move a card. Holds the project/field/option IDs.
- `get-board-items` — read and filter board items.
- `create-issues` — file new issues using `.github/ISSUE_TEMPLATE/` (feature, bug). After creating one, add it to the board with `work-board-item add <N>`.
- `update-issues` — edit issue fields or comment.

`gh` must be logged in as `daniel-vuky` with the `project` scope (`gh auth status`).

## Commands

The host needs only Docker and `make`; Go, Node, pnpm and every tool run in the dev image (ADR 0009). Run them through `make`, never natively.

- `make dev` (the default): start Air (Go hot reload) and Vite (web hot reload). UI at `http://localhost:5173`, API on `127.0.0.1:8471` inside the dev container, with the dev identity `dev@localhost` (build tag `dev`).
- `make shell`: open a bash shell in the dev image.
- `make setup`: install web dependencies (frozen lockfile) into the `node_modules` volume.
- `make dev-image`: build the dev toolchain image (cached). `SKIP_DEV_IMAGE=1` reuses a prebuilt `scp-dev` image (CI does this).
- `make test`: Go tests (release and `dev` tags), then web tests.
- `make lint`: golangci-lint, Biome (warnings fail), the TypeScript typecheck and shellcheck on the deploy scripts.
- `make fmt`: format Go and web sources.
- `make typegen`: regenerate `web/src/api/types.gen.ts` from the Go types in `internal/api` (`tygo.yaml`).
- `make build`: build the web app, then the single release binary at `bin/panel` (`VERSION` defaults to the short git SHA, or `dev` when git is unavailable).
- `make image`: build the production image `scp-panel:local` with the host's Docker (needs BuildKit).
- `make deploy-test`: run the deploy script tests and render the compose project with `docker compose config`, in a Linux container.
- `make deploy-e2e`: install, update and uninstall end to end in a throwaway `docker:29-dind` with a local registry (manual, needs privileged containers; not in CI).
- `make ci`: lint, test, build, then check that `types.gen.ts` is committed. `.github/workflows/ci.yml` runs it on pull requests, then also runs `make deploy-test` and builds the production image.

Production deployment is built (ADR 0008). `deploy/` holds the compose project, `install.sh`, `uninstall.sh` and the `updater.sh` loop; `.github/workflows/release.yml` pushes `latest` and `sha-<short>` images to GHCR after CI passes on `main` (docs-only pushes do not release). The owner's guide is `docs/deploy.md`.

## Architecture

One Go binary (`cmd/panel`) serves the JSON API and the embedded React app (`web/`, built into `internal/web/dist`). It binds `127.0.0.1` only and is reached through a Cloudflare Tunnel (ADR 0006).

- `internal/config`: reads `PANEL_ADDR` and refuses any address that is not `127.0.0.1:<port>`.
- `internal/auth`: the `Verifier` seam and the `Require` middleware. Release builds use `AccessVerifier`: it checks the `Cf-Access-Jwt-Assertion` JWT (signature, issuer, audience, expiry) and needs `CF_ACCESS_TEAM_DOMAIN` and `CF_ACCESS_AUD`, or startup fails. Setup is in `docs/cloudflare-access.md`. The `dev` build tag swaps in the fixed `dev@localhost` identity; it must never reach a release binary.
- `internal/api`: the router and API types. Only `GET /api/health` is public; everything else goes through `auth.Require`. `GET /api/me` returns the verified email.
- `deploy/`: the shell scripts and `compose.yaml` that install, update and uninstall the panel on a server, with their tests in `deploy/test/`. Run on Linux only; test them through `make deploy-test`.
- `internal/web`: serves the embedded single-page app for every non-API path.
- `web/`: React, Vite and TypeScript. Pages are declared in `web/src/app/routes.ts`; most are placeholders for later issues.
- Only `internal/runner` may import `os/exec` (depguard in `.golangci.yml`, ADR 0005). That package arrives with #12. It runs whitelisted `docker compose` commands without a shell.
- v1 manages docker compose projects only.

Decisions live in `docs/decisions/` (ADRs 0001-0009). The product spec and design references are in `knowledge/`.

## Conventions

- After changing types in `internal/api`, run `make typegen` and commit `web/src/api/types.gen.ts`; `make ci` fails if it is stale.
- Run Go, pnpm and lint tools through `make`, not natively on the host.
- `docs/superpowers/` (local design and plan docs) is gitignored; keep it local and do not commit it.
