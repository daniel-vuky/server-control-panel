# 5. Run only whitelisted docker compose commands, without a shell

Date: 2026-10-28

## Status

Accepted

## Context

The panel starts, stops and restarts projects, reads their status, logs and metrics, and runs backups. E1-F02-US01 requires that:

- no button, field or request can become arbitrary shell access;
- values from people or projects are passed as arguments;
- there is no terminal of any kind.

The panel has access to the Docker socket (see [ADR 8](0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)), which is root-equivalent on the host. A command injection would therefore be a full server compromise.

The owner runs projects with docker compose. pm2 and projects started directly with npm/yarn were considered and **dropped from v1**: projects are expected to run under docker compose. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: The docker CLI through a typed whitelist

- Pro: the same commands an operator would type.
- Pro: easy to audit, and it covers `docker compose` fully.

### Option B: The Docker Engine API through the socket

- Con: compose behaviour (project files, `.env` files, relative paths) would have to be reimplemented.

## Decision

1. **Docker compose is the only supported stack in v1.** A project is a folder containing `compose.yaml`, `compose.yml`, `docker-compose.yml` or `docker-compose.yaml`.
2. **Exactly one package, `internal/runner`, may run processes.** A `golangci-lint` `depguard` rule rejects any import of `os/exec` elsewhere.
3. The runner holds a **registry keyed by (action, stack)**. Each entry is an argv template whose placeholders replace whole arguments. Values are never concatenated into a string and never passed through a shell. Unknown actions and missing values are errors.
4. Every command has a timeout (60 seconds for lifecycle actions, per the spec). Its output is captured with known secrets masked.
5. Commands always pass an explicit `--project-directory` and `-f <compose file>`.

## Consequences

**Easier:**

- Command safety can be reviewed in one package and enforced by lint.
- Another stack could be added later as new registry keys, without reshaping the runner.

**Harder:**

- Projects not run with docker compose cannot be managed in v1.
- Every new capability needs a registry entry; there are no ad-hoc commands.

## Note (2026-10-29)

The implementation of #12 refined the decision:

- **Registry.** It holds `compose.config`, `compose.config.override` and `docker.containers`. `config` runs with `--no-env-resolution`, so the panel never prints a project's `env_file` contents. Entries are added only when a flow uses them.
- **Validation.** Path placeholders accept only absolute, clean paths, so no value can start with `-` and be taken for a flag. An unknown action, a missing or extra value, an empty value and a NUL byte are refused before anything runs.
- **Masking is per call** (decision 4). The caller passes the secret values for that call. Stderr and the error message are masked (`••••`) before they leave the runner. Stdout goes to the caller unmasked because the caller parses it, and is never logged or stored as it is.
- **Timeouts.** Read commands get 15 seconds. Lifecycle commands keep the 60 seconds of decision 4 when they arrive. Each output stream is capped at 4 MiB.
- **Minimal child environment.** The child gets `PATH`, `HOME` and `DOCKER_CONFIG` (both under `$PANEL_DATA_DIR/docker`, created on first use with mode 0700) and `DOCKER_HOST` only if the panel has it set. It never inherits the panel's own environment, so the `CF_ACCESS_*` values stay out of it.

## Note (2026-10-30)

The project lifecycle flow (#39 to #41, #43) added the lifecycle entries and tightened process handling:

- **Registry.** It gains `compose.up`, `compose.stop` and `compose.restart`, each with a `.override` variant, and `docker.ping` for the Docker check. The compose entries use `up -d`, `stop` and `restart` with the usual `--project-directory` and `-f` arguments (decision 5); the values are the two paths, plus the override path for the `.override` entries, and nothing else is accepted.
- **Timeouts.** Lifecycle commands get the 60 seconds of decision 4. `docker.ping` gets 5 seconds. Read commands keep 15 seconds.
- **Process group.** Each command starts in its own process group. On timeout or cancel the whole group is sent SIGKILL, because `docker compose` runs as a plugin child that would otherwise outlive the kill. The panel containers run with `init: true` to reap the killed trees.
- **Masking at action time.** The controller reads the project's `.env` when the action runs and passes those values to the runner as that call's secrets (the per-call masking of the 2026-10-29 note). The outcome message and the activity reason carry the masked text only.
