# 3. Push live updates over Server-Sent Events

Date: 2026-10-28

## Status

Accepted

## Context

E3-F09 requires these live updates, all pushed rather than reloaded, without disturbing scroll position, filters or input:

- Project status within 5 seconds.
- Metrics within 10 seconds.
- Live logs.

E3-F09-US02 requires the client to notice when updates are lost. The panel is reached through a Cloudflare Tunnel, which closes idle connections after about 100 seconds. The owner wants the panel light on the server. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: Server-Sent Events (SSE)

- Pro: plain HTTP with automatic browser reconnection, and it works through Cloudflare.
- Pro: one-way traffic is all the live data needs.
- Con: one-way only, so actions need a separate channel.

### Option B: WebSocket

- Con: two-way, which is not needed, and it brings more protocol and library surface.

### Option C: Polling

- Con: the spec requires values to be pushed.
- Con: each open tab would multiply the requests.

## Decision

Live data goes over **Server-Sent Events**. Actions (start, stop, backup and so on) go over ordinary REST calls.

1. **One stream per browser tab** (`GET /api/events`) carries status and metrics for whatever that tab subscribes to.
2. **A central hub collects data once and broadcasts it** to every subscriber. The number of open tabs never multiplies the commands the panel runs.
3. The 5-second status and 10-second metrics cadence runs **only while at least one subscriber is connected**. With nobody watching, only the 60-second history sampling runs (see [ADR 7](0007-run-background-jobs-inside-the-panel-process.md)).
4. A heartbeat goes out about every 25 seconds so the tunnel does not drop idle streams (a `ping` event since #32; see the last note). The client reconnects on its own and shows when it is disconnected.
5. Live logs use a separate SSE endpoint per open log view. It starts the log-follow command when the view opens and stops it when the client disconnects.

## Consequences

**Easier:**

- The transport costs almost nothing: an open HTTP connection per tab.
- The real cost, collecting data, is shared across all viewers and paused when nobody is looking.

**Harder:**

- The hub has to track subscribers and switch cadence as viewers come and go.
- Actions and their results travel on different channels (REST for the action, SSE for the resulting status).

## Note (2026-10-29)

The workspace scan (#18) added a second kind of stream next to the hub:

- **Per-job progress streams.** `GET /api/scans/{id}/events` streams the progress of one scan job. It is not the shared `/api/events` hub of decision 1, which arrives with live status (E3-F09). Later long-running jobs, such as backup progress, are expected to get a stream of the same kind.
- **Behaviour.** A client that connects gets the current snapshot as a `progress` event, then further `progress` events at most about 5 per second (later updates are coalesced), then one `end` event with the final state, and the stream closes. A client that connects after the end gets the snapshot and `end` straight away. A heartbeat goes out every 25 seconds (decision 4).
- **Fan-out.** A small in-memory broadcaster keyed by job id lets several tabs watch one scan. It starts no work of its own.
- **Client.** The browser uses `EventSource`. On `end` it fetches the job for the result. If the connection drops before `end`, it fetches the job once and reconnects if the job is still running.

## Note (2026-10-30)

The project lifecycle flow (#33, #39 to #44) built the hub of decision 2, scoped to project status and the Docker state. Metrics and live logs are not on it yet.

- **One shared read.** `GET /api/events` sends a `state` event (`LiveState {dockerUp, projects}`) to every subscriber. The hub runs one `docker.containers` read every 5 seconds, ticks never overlap, and it does nothing while nobody is connected (decision 3). A new subscriber gets the current state straight away.
- **Stale reads are dropped.** A generation counter, bumped on every lock change, discards a read that started before the change (a lock taken or released) and reads again, up to 3 reads, so the post-command status is not overwritten by an older one.
- **Retry.** `POST /api/docker/check` asks the hub for one fresh read and returns the result, so the Docker banner can recover without waiting for the next tick.
- **The action response travels with the action.** `POST /api/projects/{id}/{action}` waits for the command and returns the outcome, the status read from Docker afterwards and the actions now allowed. The toast does not wait for the stream. The stream still carries the in-between status (Starting…, Stopping…, Restarting…) to other tabs while the command runs. This refines the consequence "actions and results travel on different channels": the action's own result comes back on the action request.
- **Client.** One `EventSource` for the whole app, in `LiveProvider`. It receives the heartbeat of decision 4 and reconnects on its own.

## Note (2026-10-30, #24)

Rescan, workspace removal and Stop tracking (#19, #24, #25) change which projects exist, so the hub's cached list can be wrong until the next 5-second tick. `Hub.Refresh()` asks for a fresh read straight away:

- **No loop running** (nobody subscribed): it only invalidates the cache, so the next subscriber reads fresh.
- **A tick running:** nothing more; the running tick sees the changed generation, drops its list and ticks again.
- **Otherwise:** it starts one detached tick.

In every case `Refresh` first bumps the generation (`tsGen`), so a tick that read the tracked projects before the change never publishes.

The API calls it after an untrack, a workspace removal and an add, and the scan manager calls it through `OnChange` when a rescan applies. A write that holds project locks (`Controller.WithLocks`) does not publish on a successful release, since the hub's cached list still holds the rows it changed; the caller refreshes instead.

## Note (2026-10-30, #32)

The heartbeat of decision 4 is a named event, `event: ping` with `data: {}`, on every stream (`/api/events`, the scan progress streams and the log streams). A comment such as `: ping` keeps the tunnel open, but `EventSource` never hands it to the page, so the browser could not tell a quiet stream from a stalled one (the panel frozen, the TCP connection still open, no error). The client now arms a 60-second watchdog per stream, re-armed on every `state` (or `lines`) and `ping` event; when it fires, the badge shows "Connection lost, retrying…" and the stream is reopened after 1 second. A stall does not count towards the closed attempts that lead to the session check. See [ADR 10](0010-stream-logs-through-one-shared-follow-process.md).

## Note (2026-10-31, server metrics)

Server metrics, their history and the Server page (#59 to #63, #36) refine decision 3 and the polling of Option C:

- **Sampling with nobody watching is every 30 seconds,** not 60 ([ADR 7](0007-run-background-jobs-inside-the-panel-process.md)). The sampler is its own loop in `internal/metrics`, not a hub cadence. It reuses the hub's `docker stats` and `docker inspect` reads when they are at most 15 seconds old (a tab is open), and reads Docker itself otherwise. The hub's 5-second status and 10-second metrics cadence is unchanged and still runs only while someone subscribes.
- **History series are polled, by visible tabs only.** The Server page and a project's Overview re-read their series every 30 seconds while the tab is visible (the timer stops when the tab is hidden, and becoming visible re-reads at once). A sample changes only every 30 seconds and a series is a few kB, so a new SSE event would add hub state for no gain. This is the one place the panel polls; the series come from SQLite and the System cards from the panel's caches (`docker info` is read at most once a minute), so more tabs do not mean more Docker reads.
- **Live values stay on SSE.** Project status, CPU and RAM and the Docker state still come from `/api/events`: the project charts' current CPU and RAM read the live project, and the Server page's Docker row and usage table follow `dockerUp` at once.
