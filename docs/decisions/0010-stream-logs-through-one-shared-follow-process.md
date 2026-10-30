# 10. Stream logs through one shared follow process per project and container

Date: 2026-10-30

## Status

Accepted

## Context

The Logs tab of a project ([#37](https://github.com/daniel-vuky/server-control-panel/issues/37)) shows live container output. Docker delivers it only through a long-lived command, `docker compose logs --follow`. [ADR 5](0005-run-only-whitelisted-docker-compose-commands-without-a-shell.md) gives every command a timeout and a capped output, and that rule cannot hold for a command whose purpose is to never end.

A follow process costs about 50 MB (measured on Compose v5), and the panel runs under a memory limit ([ADR 8](0008-deploy-the-panel-as-a-docker-compose-project-with-pull-based-updates.md)). The browser's `EventSource` also cannot read the status or body of a non-200 answer, so an HTTP error would reach the page as a bare `error`.

### Option A: One follow process per viewer

- Con: every open tab or second viewer adds 50 MB, so memory grows with the number of tabs.

### Option B: Poll `compose logs --tail` on an interval

- Pro: every command ends and keeps its timeout.
- Con: lines are missed or repeated between polls, and each poll starts a process.

### Option C: One shared follow process per (project, service or all), alive only while someone watches

- Pro: memory depends on the number of distinct logs open, not on viewers, and nothing runs while nobody looks.
- Con: a new code path with its own lifetime rules, and a cap to enforce.

## Decision

Option C.

1. **Streaming is the one place a docker command runs without a timeout.** `Runner.Stream` runs only the `compose.logs` follow actions (`Run` refuses them, `Stream` refuses everything else). It shares the argv check, the minimal environment, the masking and the process-group kill with `Run`, delivers each line (cut at 16 KiB) and lives exactly as long as its context.
2. **Viewers bound the lifetime.** `internal/logs` keeps one stream per (project, service or all). The first viewer starts it; the last viewer leaving cancels its context at once, which kills the whole process group. A follow that exits on its own ends the stream for everyone (`exited`). With no running or restarting container in the selection no follow starts, because Compose's follow on stopped containers prints nothing and never exits.
3. **Backlog, then follow, without a gap.** The stream reads `compose logs --tail 500` through `Run` (with its timeout), then follows from the time it started and drops follow lines already in the backlog. A ring of the newest 500 lines serves late viewers, and viewers are sent batches at most every 200 ms.
4. **At most 3 follow processes run at once, panel-wide.** That is about 150 MB at the measured 50 MB each. With the panel's own heap, the short `docker ps`, `docker inspect` and `docker stats` children that can overlap them, and a rescan's `docker compose config`, it sets `mem_limit` to 384m ([deploy guide](../deploy.md)). A viewer joining an existing stream always gets in; a request that would start a fourth follow is refused. The backlog command does not count, since it ends within seconds.
5. **Refusals are SSE `end` reasons.** Because `EventSource` cannot read a non-200 body, only an unknown project id answers 404. Every other refusal (`unknown_service`, `not_found`, `docker_down`, `too_many_log_streams`) answers 200 as SSE and sends one `end {reason, message}` event at once. A viewer that falls behind is ended with `slow`, and the browser reconnects. A backlog or follow that fails (or a stream that panics) ends `error` with a masked message.
6. **Secrets stay masked.** Lines are masked with the project's `.env` values before they reach a viewer, and reading logs is not recorded in `activity` since it changes nothing.
7. **A silent stream is a stalled one.** The log stream, like `/api/events`, sends a `ping` event (`event: ping`, `data: {}`) every 25 seconds; a comment would never reach the page. The browser reopens a log stream that sent no `lines` and no `ping` for 60 seconds, the way it does after a network error, keeping the lines it holds ([ADR 3](0003-push-live-updates-over-server-sent-events.md), note of #32).

## Consequences

**Easier:**

- Memory is bounded and predictable, and an idle panel runs no follow process.
- Several tabs or people on the same logs cost one process.

**Harder:**

- Streaming is an exception to ADR 5's "every command has a timeout"; the exception is limited to the follow actions and enforced in the registry and in `Run` and `Stream`.
- A fourth distinct log view is refused until another closes.
- The line parser depends on Compose's `--timestamps` output format (`<prefix> | <timestamp> <text>`) and drops any line that does not match it.
