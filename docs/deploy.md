# Deploy the panel

This guide installs the panel on your server, keeps it up to date and removes it again. The panel runs as a docker compose project ([ADR 0008](decisions/0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)). Docker is all the server needs.

## 1. What gets installed

Two containers, in one compose project called `server-control-panel`:

- `panel`: the panel itself. It listens on `127.0.0.1:8471` of the server, so only the Cloudflare Tunnel running on the host can reach it. It runs as your user, never as root, with no extra capabilities, and is limited to 384 MB of memory and half a CPU.
- `updater`: a small container that checks for a new panel image every 5 minutes and applies it (see section 5).

Nothing runs on the host outside docker compose: no service, no cron job, no extra program.

The memory limit leaves room for the live log views: each one runs a `docker compose logs --follow` that takes about 50 MB, and the panel allows up to 3 at a time, plus the short Docker reads that can overlap them, a rescan and the panel's own memory.

The files live in `/opt/server-control-panel`:

| File | What it is | On a re-run of the install command |
| --- | --- | --- |
| `panel.env` | Your Cloudflare values, workspace roots, the secret key, the time zone and the optional Google Drive values (mode 0600) | Never overwritten (a missing `PANEL_SECRET_KEY` or `TZ` is appended) |
| `.env` | `PANEL_TAG` and `UPDATE_INTERVAL` | Never overwritten (but the uninstall removes it, see section 8) |
| `data/` | The panel's own data | Kept |
| `compose.yaml` | The compose project | Refreshed |
| `compose.override.yaml` | Your user, the docker group and the workspace mounts, generated from `panel.env` | Regenerated |
| `updater.sh`, `uninstall.sh` | The update loop and the uninstall script | Refreshed |

Do not edit the refreshed files; your changes would be lost. Change settings in `panel.env` and `.env` instead.

## 2. Before you start

You need:

- a Linux server with Docker and the compose plugin (docker compose 2.20 or newer);
- a normal user that is in the `docker` group. Check with `docker ps`. If it fails, run `sudo usermod -aG docker $USER` and log in again;
- the Cloudflare side done: sections 1 to 5 of [Set up Cloudflare Access](cloudflare-access.md). That guide is not repeated here. Section 4 gives the two values the installer asks for, the team domain and the AUD tag.

Run the install command as that normal user. Running it with `sudo` from that user's account also works. Running it as root does not, because the panel must not run as root.

## 3. Once, after the first release

Every merge to `main` whose CI passes publishes the panel image to GitHub's container registry. The first time, GitHub makes the package private. The server pulls it without logging in, so make it public once:

1. Open the package page, <https://github.com/users/daniel-vuky/packages/container/package/server-control-panel>.
2. Open **Package settings**.
3. Under **Danger Zone**, select **Change visibility**, choose **Public** and confirm.

Until then, pulls fail with `denied` or `unauthorized`, and the installer says so.

## 4. Install

On the server, as the user from section 2:

```sh
curl -fsSL https://raw.githubusercontent.com/daniel-vuky/server-control-panel/main/deploy/install.sh | bash
```

The script checks Docker, creates `/opt/server-control-panel` (it uses `sudo` for that step only when your user cannot write to `/opt`), then asks three questions (and three optional ones, below):

1. **Cloudflare Access team domain**, like `myteam.cloudflareaccess.com`.
2. **Cloudflare Access application AUD tag**, the long hex string.
3. **Workspace roots**: the folders that hold your projects, comma-separated. Press Enter for the default, which is those of `/home`, `/srv`, `/opt` and `/var/www` that exist. A root that does not exist is skipped.

A wrong answer is explained and asked again. Then come three optional questions for backups to Google Drive: the panel's public URL (`https://<your panel domain>`, no path) and the Google client ID and secret. Press Enter to skip all three and set them later (see section 4a). The answers go into `panel.env`. The script then pulls the images, starts the project and waits up to 60 seconds for the panel to report healthy. A good finish prints a block that starts with:

```text
The server control panel is running.
```

followed by the install directory, the address and a link to this guide.

If it prints anything else, go to section 9.

The workspace roots are mounted into the panel read-only, at the same path as on the host. That includes `/opt/server-control-panel` with `panel.env`, because `/opt` is a default root. The panel already receives those values, so nothing new is exposed.

**Without a terminal.** If the install runs with no terminal (a script, cron, or `ssh host '…'` without `-t`), it cannot ask. Set all three variables instead: `CF_ACCESS_TEAM_DOMAIN`, `CF_ACCESS_AUD` and `WORKSPACE_ROOTS`. The optional `PANEL_PUBLIC_URL`, `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` stay empty unless you set them too. Put them on the `bash` side of the pipe:

```sh
ssh host 'curl -fsSL https://raw.githubusercontent.com/daniel-vuky/server-control-panel/main/deploy/install.sh | CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com CF_ACCESS_AUD=<tag> WORKSPACE_ROOTS=/home,/srv bash'
```

If one is missing, the script stops and names it.

Without a terminal, `sudo` cannot ask for a password either. So a first install, which creates `/opt/server-control-panel`, also needs one of these: `sudo` that asks no password for your user, or a `/opt/server-control-panel` that already exists and is owned by you (`sudo install -d -o $USER -g $(id -gn) -m 0750 /opt/server-control-panel`, run once in a terminal).

When the install is healthy, check the whole setup with the [manual acceptance checklist](cloudflare-access.md#manual-acceptance-checklist) in the Cloudflare guide. That checklist needs the panel running, so this is its first chance.

Running the install command again is always safe. It keeps `panel.env` and `.env`, refreshes the other files and restarts what changed.

## 4a. The secret key and the Google Drive settings

The install adds these to `panel.env`:

| Variable | What it is |
| --- | --- |
| `PANEL_SECRET_KEY` | 32 random bytes as 64 hex characters, generated for you. It encrypts the Google Drive token. |
| `PANEL_PUBLIC_URL` | The panel's public address, `https://<your panel domain>` with no path (a trailing `/` is dropped). Optional. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | The Google OAuth client for Drive backups. Optional; give both or neither. |

Keep `PANEL_SECRET_KEY` with the server's other secrets. It is stored outside `data/` on purpose, so a copy of the panel's database alone cannot decrypt the Drive token. If the key is lost or changed, nothing else breaks: you only connect Google Drive again from Settings.

An older `panel.env` that has no `PANEL_SECRET_KEY` gets one line appended on the next run of the install command; the rest of the file is not touched.

### The time zone (`TZ`)

`TZ` is the server's time zone, an IANA name such as `Asia/Ho_Chi_Minh` or `UTC`. Backup schedules run at their time in this zone, and every schedule time and next run in the panel is shown in it, with the zone named. Unset means UTC. The panel refuses to start with a `TZ` it does not know.

The install detects the host's zone (`timedatectl`, then `/etc/timezone`, then the `/etc/localtime` link, else `UTC`), offers it as the default when it asks, and writes it as `TZ=` in `panel.env`. Run it with `SCP_TZ=<zone>` in the environment to name another one without being asked (the plain `TZ` of your shell is ignored, since `sudo` passes it through). `posix/` is dropped from a detected name and `right/` zones are not used. An older `panel.env` with no `TZ` line gets one appended on the next run of the install command; a `TZ` line you set yourself is never changed.

To change it later, edit `TZ` in `panel.env` and run `cd /opt/server-control-panel && docker compose up -d`. The panel restarts to read it; a schedule keeps its time of day and runs at that time in the new zone.

Without the three optional values the panel runs as before and Google Drive shows as not set up. To set Drive up, follow [Connect Google Drive](google-drive.md), put the values in `panel.env` and run `cd /opt/server-control-panel && docker compose up -d`.

## 4b. Server metrics: disks and cloudflared

The Server page reads the host's CPU, memory, network and disks. The compose project mounts the host's `/proc` and its root (`/`) into the panel, both read-only: `/proc` for the counters, the root only to ask each disk for its used and total space. The panel never opens a file under it. A disk mounted after the panel started appears after the panel's next restart, which every release does.

**An install from before this feature** has a `compose.yaml` without the root mount. The updater does not refresh `compose.yaml`, so re-run the install command (section 4) once. Until then the System info card shows one row, `/ (panel data)`, for the disk that holds the panel's data, with "Re-run install.sh to list every disk."

**cloudflared** is read from the host: the panel looks for the process and asks its metrics server on the host's loopback, by default the first of `127.0.0.1:20241` to `127.0.0.1:20245`. If you moved cloudflared's metrics address (its `--metrics` option), add it to `panel.env` and re-run the install command:

```text
CLOUDFLARED_METRICS=127.0.0.1:<port>
```

It must be a loopback address (`127.0.0.1`, `localhost` or `[::1]` with a port); anything else stops the panel at start-up. The install does not ask for it. Without it, a cloudflared on another port shows as "Running · connection unknown".

## 5. Automatic updates

The `updater` container wakes up every 5 minutes, pulls the panel image with the tag set in `.env` (`PANEL_TAG`, `latest` by default) and compares it with the image the panel is running. Only when they differ does it recreate the panel. Otherwise it logs `panel is up to date`. After a real update it also removes old panel images that nothing uses, so disk space does not grow. It only ever touches images that carry the panel's label.

A failed pull or lookup changes nothing: the running panel stays up, and the updater tries again next round.

A panel you stopped yourself (`docker compose stop panel`) stays stopped: the updater logs `panel is stopped, leaving it` and does nothing else. Start it again with `cd /opt/server-control-panel && docker compose start panel`; the next round then updates it if a newer image arrived meanwhile.

Images are built only after CI passes on `main`. A merge that changes only `docs/`, `knowledge/` or `*.md` files runs no CI and releases nothing. A merge reaches the server within about 5 minutes of its image being published.

See what the updater did:

```sh
cd /opt/server-control-panel
docker compose logs updater
```

To change the interval, edit `UPDATE_INTERVAL` (seconds) in `/opt/server-control-panel/.env`, then apply it:

```sh
cd /opt/server-control-panel && docker compose up -d updater
```

A value that is not a number makes the updater log `invalid UPDATE_INTERVAL` and wait 300 seconds.

Edits to `panel.env` are not applied by the updater. Re-run the install command for those (see section 7).

## 6. Rollback

Every release has two tags: `latest` and `sha-<7 characters>`, for example `sha-c78a713`. The tags are listed on the package page, <https://github.com/users/daniel-vuky/packages/container/package/server-control-panel>.

To go back to an earlier build:

1. In `/opt/server-control-panel/.env`, set `PANEL_TAG=sha-<short sha>`.
2. Run `cd /opt/server-control-panel && docker compose up -d panel`.

The updater then keeps that version: it only follows the tag in `.env`. To follow the newest release again, set `PANEL_TAG=latest` and run the same command.

## 7. Add a workspace root

1. Edit `WORKSPACE_ROOTS` in `/opt/server-control-panel/panel.env`.
2. Run the install command again.

The script checks the list, mounts the new root and restarts the panel. A running container cannot gain a mount, which is why a re-run is needed. A root that would hide the container's own files (`/`, `/var`, `/usr`, `/etc`, `/tmp` and similar) is refused. The same re-run applies after any edit of `panel.env`, such as correcting the AUD tag.

If you delete a root folder on the host, also remove it from `WORKSPACE_ROOTS` and re-run the install command. Otherwise, the next time the updater recreates the panel, Docker creates the missing folder again, empty and owned by root.

## 8. Uninstall

```sh
/opt/server-control-panel/uninstall.sh
```

It asks for confirmation, then stops the containers, removes the panel images and deletes the compose files and `.env`. It keeps `panel.env` and `data/`, so a later install picks up where you left off. Because `.env` is deleted, a `PANEL_TAG` rollback pin (section 6) or a changed `UPDATE_INTERVAL` is lost: after reinstalling, check `.env` and set them again. The shared `docker:29-cli` image stays, because other things on the host may use it.

To remove everything, including `panel.env` and `data/`:

```sh
/opt/server-control-panel/uninstall.sh --purge
```

`--yes` skips the confirmation, for scripts.

By hand, the equivalent is `cd /opt/server-control-panel && docker compose down`, then deleting the directory.

## 9. Troubleshooting

Start with the state and the logs:

```sh
cd /opt/server-control-panel
docker compose ps
docker compose logs panel
```

- **The panel restarts in a loop, or the install says it is not healthy.** The logs name the cause. Usually a Cloudflare value in `panel.env` is wrong, and the panel refuses to start on purpose ([Configure the panel](cloudflare-access.md#6-configure-the-panel)). Fix `panel.env` and re-run the install command.
- **The install says the image could not be pulled.** Either the package is still private (section 3), or the server has no route to `ghcr.io`. Nothing is started in that case.
- **A new release does not start.** The updater does not roll back by itself. A release whose panel is unhealthy shows as `restarting` or `unhealthy` in `docker compose ps`, and `docker compose logs panel` shows why. Pin the previous build as in section 6: set `PANEL_TAG=sha-<previous short sha>` in `.env` and run `docker compose up -d panel`. Then report the bad release.
- **`panel.<domain>` shows a Cloudflare 502.** The panel is not running (see above), or the tunnel route points at another port than the panel's address. The default is `127.0.0.1:8471`. See [Route the panel through the tunnel](cloudflare-access.md#5-route-the-panel-through-the-tunnel).
- **A 401 after sign-in.** See the troubleshooting section of the [Cloudflare guide](cloudflare-access.md#troubleshooting).
- **The updater shows `pull failed`.** Network or registry trouble. It retries each round and the running panel is not affected.

To build the image yourself, you need Docker with BuildKit (on by default in current Docker). `make image` builds `scp-panel:local` from this repository.
