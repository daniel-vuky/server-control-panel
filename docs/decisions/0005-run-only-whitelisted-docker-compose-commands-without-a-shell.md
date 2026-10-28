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
