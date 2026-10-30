# server-control-panel

## Task board

Work is tracked on the GitHub Project board: https://github.com/users/daniel-vuky/projects/2 (project `2`, owner `daniel-vuky`).

Columns (the `Status` field): **Backlog → Ready → In progress → In review → Done**.

- Only pick up issues in **Ready**. Backlog items are not specified enough yet.
- One flow = one branch = one PR. A flow is a group of dependent issues worked end to end in one round (a single issue is a flow of one). Branches: `feat/<first-issue>-<slug>` or `fix/<first-issue>-<slug>`.
- PR bodies start with one `Closes #<issue>` line per issue in the flow, so merging once closes them all.
- Claude moves cards to In progress and In review; the user reviews and merges. Never merge PRs yourself.

Skills for the board:

- `work-board-item` — pick the next Ready issue and work it end to end, add an issue to the board, or move a card. Holds the project/field/option IDs.
- `get-board-items` — read and filter board items.
- `create-issues` — file new issues using `.github/ISSUE_TEMPLATE/` (feature, bug). After creating one, add it to the board with `work-board-item add <N>`.
- `update-issues` — edit issue fields or comment.

`gh` must be logged in as `daniel-vuky` with the `project` scope (`gh auth status`).

## Commands

The host needs only Docker and `make`; Go, Node, pnpm and every tool run in the dev image (ADR 0009). Run them through `make`, never natively.

- `make dev` (the default): start Air (Go hot reload) and Vite (web hot reload). UI at `http://localhost:5173`, API on `127.0.0.1:8471` inside the dev container, with the dev identity `dev@localhost` (build tag `dev`). The api container gets the host's Docker socket (root-equivalent on the host), `WORKSPACE_ROOTS=/src/dev/workspaces` and `PANEL_DATA_DIR=/src/data` (gitignored).
- `make fixtures-up` / `make fixtures-down`: start and remove the sample compose projects in `dev/workspaces/` with the host's Docker (`shop` Partial, `notes` Running, `blog` stopped), so a dev scan shows several statuses. `make test` and CI never need Docker.
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

- `internal/config`: reads `PANEL_ADDR` and refuses any address that is not `127.0.0.1:<port>`. It also reads `WORKSPACE_ROOTS` (comma-separated absolute paths, the only folders that may become workspaces) and `PANEL_DATA_DIR` (the panel's own state, default `/data`).
- `internal/auth`: the `Verifier` seam and the `Require` middleware. Release builds use `AccessVerifier`: it checks the `Cf-Access-Jwt-Assertion` JWT (signature, issuer, audience, expiry) and needs `CF_ACCESS_TEAM_DOMAIN` and `CF_ACCESS_AUD`, or startup fails. Setup is in `docs/cloudflare-access.md`. The `dev` build tag swaps in the fixed `dev@localhost` identity; it must never reach a release binary.
- `internal/api`: the router and API types. Only `GET /api/health` is public; everything else goes through `auth.Require`. `GET /api/me` returns the verified email. Workspace flow: `POST /api/scans` (starts a scan, 409 while one runs), `GET /api/scans/{id}`, `GET /api/scans/{id}/events` (per-job SSE: `progress`, then `end`), `POST /api/scans/{id}/track` (stores the workspace and its projects, tracked or ignored, in one transaction), `GET /api/workspaces`, `GET /api/projects` (tracked projects with effective status and allowed `actions`; Unknown when Docker cannot be read). Lifecycle and live state: `POST /api/projects/{id}/{action}` (`start`, `stop`, `restart`; waits for the outcome and answers 200 `ActionResult {outcome, status, message, actions}`; 409 `locked` or `not_allowed` and 503 `docker_down` with `ActionRefused`; 404 for an unknown project), `GET /api/events` (SSE, one `state` event per change, a `LiveState {dockerUp, projects}`) and `POST /api/docker/check` (Retry: one fresh Docker read for the hub). Every state-changing `POST /api/*` must send `Content-Type: application/json` or it gets 415 and nothing runs (`requireJSON`, inside `auth.Require`). Field errors are 422 `{error, field, code}`.
- `internal/runner`: the only package that runs processes. A registry of argv templates keyed by (action, stack): `compose.config`, `compose.up`, `compose.stop`, `compose.restart` (each with a `.override` variant), `docker.containers` and `docker.ping`. Values are whole arguments and must be absolute clean paths where they are paths. Read commands time out after 15 s, lifecycle commands (`compose.up/stop/restart`) after 60 s, `docker.ping` after 5 s; each output stream is capped at 4 MiB. Every command runs in its own process group and the whole group is SIGKILLed on timeout or cancel, so a `docker compose` plugin child cannot outlive it; the panel containers run with `init: true` to reap the killed trees. Stderr and error messages are masked (`••••`) for the secret values the caller passes. The child gets a minimal environment (`PATH`, `HOME` and `DOCKER_CONFIG` under `$PANEL_DATA_DIR/docker`), never the panel's own.
- `internal/roots`: the `WORKSPACE_ROOTS` guard. Resolves a path (symlinks, `..`) to one canonical form and refuses anything outside the roots, with the error codes `not_absolute`, `not_found`, `unreadable`, `outside_roots`, `not_a_folder`.
- `internal/store`: SQLite at `$PANEL_DATA_DIR/panel.db` (`modernc.org/sqlite`, no cgo), with embedded migrations applied on open. Holds workspaces, projects and scan jobs; a job left running becomes interrupted on reopen. The `activity` table (migration 0002) is the append-only record of who did what: two inserts (a `running` row, or a finished one such as a refusal), one guarded `running` to final update and a store-level reader `RecentActivity` (no HTTP route yet), never a delete; the actor email comes only from `auth.FromContext`, the reason is masked, and a row left `running` becomes `interrupted` on reopen. Scans, tracks and project actions (refusals included) are recorded.
- `internal/project`: the `Status` values (`running`, `partial`, `stopped`, `error`, `unknown`, plus the in-between `starting`, `stopping`, `restarting` and `notfound`) and the mapping from `docker.containers` output. `Allowed` gives the actions a status permits (the server uses it; the UI reads the `actions` the server sends and never derives them), and `AnyActive` says whether anything runs or restarts.
- `internal/control`: project lifecycle. The `Controller` holds per-project in-memory locks; `Do` checks (not allowed, locked, Docker down, the panel's own project; a refusal is a `*Refusal` error, answered 409 or 503 and recorded in `activity` as `refused`), runs the command with the project's `.env` secrets masked, re-reads Docker and settles an `Outcome` (`ok`, `failed`, `timed_out`). A start or restart that exits 0 but leaves anything but Running or Partial is failed (Partial counts as started); a stop that exits 0 but leaves something running or restarting is failed. The command runs on its own context, so a client disconnect never aborts it, and the lock is released on every path. The `Hub` does one non-overlapping Docker read every 5 s, shared by all `/api/events` subscribers and only while someone is subscribed; a generation counter drops a stale read that lands after a lock change, and `Check` backs Retry. Lock order: `hub.mu` then `ctl.mu`.
- `internal/scan`: the workspace scanner. It walks depth 1-3 (symlinks, hidden folders and `node_modules` skipped; no descent into a project), reads each compose file with `docker compose config --format json --no-env-resolution` through the runner, parses `.env` for masking and database hints, detects databases without keeping any secret, and recognises the panel's own project (Monitor only) by its image label. It runs one scan at a time and fans progress out through a broadcaster.
- `deploy/`: the shell scripts and `compose.yaml` that install, update and uninstall the panel on a server, with their tests in `deploy/test/`. Run on Linux only; test them through `make deploy-test`.
- `internal/web`: serves the embedded single-page app for every non-API path.
- `web/`: React, Vite and TypeScript. Pages are declared in `web/src/app/routes.ts`. Built: `/onboarding` (path and depth, live scan progress, results table, Add), the `/` Dashboard table of tracked projects with lifecycle controls, and the Workspaces card on `/settings`. `LiveProvider` (`web/src/app/live.tsx`) holds one `EventSource` on `/api/events` for every page and shows the Docker banner (Retry posts `/api/docker/check`). `StatusBadge` is the status pill for all nine statuses. The Dashboard row has a Start/Stop switch and a ⋯ menu (Start, Restart); Stop and Restart of a Running or Partial project ask first in a confirm dialog; results show in a toast stack. The row's allowed actions come from the server, never re-derived in the browser. The other pages are placeholders for later issues.
- Only `internal/runner` may import `os/exec` (depguard in `.golangci.yml`, ADR 0005). It runs whitelisted `docker` commands without a shell; there is no terminal or free-form command anywhere.
- `dev/workspaces/`: sample compose projects used as the dev `WORKSPACE_ROOTS` (`shop`, `blog`, `notes`, `clients/acme/web`, `broken`, `tools`, and an `outside` symlink that must be skipped). `shop/.env` holds `devpassword`, which must never appear in the DB, logs or API.
- v1 manages docker compose projects only.

Decisions live in `docs/decisions/` (ADRs 0001-0009). The product spec and design references are in `knowledge/`.

## Conventions

- After changing types in `internal/api`, run `make typegen` and commit `web/src/api/types.gen.ts`; `make ci` fails if it is stale.
- Run Go, pnpm and lint tools through `make`, not natively on the host.
- `docs/superpowers/` (local design and plan docs) is gitignored; keep it local and do not commit it.
