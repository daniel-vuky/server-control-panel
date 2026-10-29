# 8. Deploy the panel as a docker compose project with pull-based updates

Date: 2026-10-28

## Status

Accepted

## Context

The owner's rules for running the panel itself:

- It runs like every other project on the server, with docker compose. **Nothing is installed on the host as a background service**, to avoid conflicts.
- Installing takes **one command**, with no `git clone` or build on the server.
- New versions arrive through CI/CD without giving GitHub a way into the server. The server sits behind a Cloudflare Tunnel.

The server runs Ubuntu with 8 GB RAM. `cloudflared` is already installed directly on the host and stays outside the panel's control. The repository is public and open source (MIT). Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: docker compose, with images built by CI and pulled by the server

- Pro: matches how the owner runs everything else.
- Pro: no toolchain on the server.
- Con: the container needs the Docker socket and host mounts.

### Option B: systemd running the binary

- Pro: direct host access.
- Con: installs a service outside docker compose.
- Con: "pull and run" would need a Go and Node toolchain on the server, or a separate download step.

### Option C: build the image on the server (`up --build`)

- Con: heavy build images and CPU on the managed server.

### Option D: push deploys from GitHub Actions over SSH through the tunnel

- Pro: immediate deploys, with logs in Actions.
- Con: GitHub would hold a key into the server.

## Decision

1. **Image:** a multi-stage `Dockerfile` (web build, then Go build). The runtime image is based on the official `docker:<major>-cli` image, so it carries the docker CLI and the compose plugin. It contains only the panel binary on top of that.
2. **Release:** on every push to `main`, GitHub Actions builds `linux/amd64` and `linux/arm64` images and pushes them to `ghcr.io/daniel-vuky/server-control-panel` as `latest` and `sha-<short sha>`. The package is public, so the server needs no registry login.
3. **Compose project** (`/opt/server-control-panel/compose.yaml`), service `panel`:
   - `network_mode: host`, so the panel really binds `127.0.0.1` on the host and the host's `cloudflared` reaches it ([ADR 6](0006-authenticate-through-cloudflare-access-and-bind-to-loopback.md));
   - `/var/run/docker.sock` mounted, to control the other compose projects;
   - host `/proc` mounted read-only at `/host/proc`, for server metrics;
   - **workspace roots** (`WORKSPACE_ROOTS`, default `/home,/srv,/opt,/var/www`, keeping those that exist) mounted read-only **at the same path as on the host**, so compose files, `.env` files and relative paths resolve as they would on the host. These roots are also the allowed directories of E1-F02-US02;
   - a data directory mounted at `/data`;
   - runs as the installing user's UID with the host's docker GID added, with no extra capabilities;
   - limits `mem_limit: 200m` and `cpus: 0.5`, and `restart: unless-stopped`.
4. **Updates are pull-based.** A second service, `updater` (`docker:<major>-cli`), runs `docker compose pull panel && docker compose up -d panel` every `UPDATE_INTERVAL` seconds (default 300). GitHub never gets access to the server. Rollback is pinning a `sha-` tag.
5. **Install** is `curl -fsSL <raw url>/install.sh | bash`. It writes the compose files and `panel.env` (mode 0600, never overwritten on re-run), asks for the Cloudflare values and workspace roots, detects the UID and docker GID, and starts the project. Adding a workspace root outside the mounted ones means editing `panel.env` and re-running `install.sh`.
6. **The panel never controls its own compose project.** The panel's own project shows as monitor-only.

## Consequences

**Easier:**

- Installing and updating need nothing on the host besides Docker.
- The panel is managed the same way as every other project.

**Harder:**

- **The Docker socket is root-equivalent on the host.** This is accepted because controlling compose projects requires it, and it is why [ADR 5](0005-run-only-whitelisted-docker-compose-commands-without-a-shell.md) and ADR 6 cannot be relaxed.
- A workspace outside the mounted roots needs a re-install step, because a running container cannot gain new mounts.
- Updates arrive up to `UPDATE_INTERVAL` after a merge, not instantly.

## Note (2026-10-29)

The implementation of #75 refined the decision:

- **Release trigger.** Images are built after CI passes on `main`, not on every push (decision 2). A push that only changes `docs/`, `knowledge/` or `*.md` files runs no CI and so releases no image.
- **Updates.** The updater (decision 4) recreates the panel only when the pulled image differs from the running one. After an actual update it removes the panel's old images (filtered by the panel's image label) so disk use stays flat. A failed pull or lookup changes nothing and is retried next round. A panel the owner stopped is left stopped.
- **Hardening.** The panel drops every capability and runs with `no-new-privileges`; the updater mounts the install directory read-only.
- **Install.** `install.sh` stops the updater while it works and always starts it again, so the two never recreate the panel at the same time.
- **Uninstall.** `uninstall.sh` stops the project, removes the panel images and the compose files, and keeps `panel.env` and `data/`. `--purge` removes the whole install directory.
- **Rollback is manual.** A release that fails its health check is not rolled back automatically. The owner pins the previous `sha-` tag in `.env` (`docs/deploy.md`).
- **Layout.** The deploy files live in `deploy/`, and `install.sh` downloads them from there.
