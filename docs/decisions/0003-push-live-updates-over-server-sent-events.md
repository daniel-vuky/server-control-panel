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
4. A heartbeat comment goes out about every 25 seconds so the tunnel does not drop idle streams. The client reconnects on its own and shows when it is disconnected.
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
- **Behaviour.** A client that connects gets the current snapshot as a `progress` event, then further `progress` events at most about 5 per second (later updates are coalesced), then one `end` event with the final state, and the stream closes. A client that connects after the end gets the snapshot and `end` straight away. A `: ping` heartbeat goes out every 25 seconds (decision 4).
- **Fan-out.** A small in-memory broadcaster keyed by job id lets several tabs watch one scan. It starts no work of its own.
- **Client.** The browser uses `EventSource`. On `end` it fetches the job for the result. If the connection drops before `end`, it fetches the job once and reconnects if the job is still running.

## Note (2026-10-30)

The project lifecycle flow (#33, #39 to #44) built the hub of decision 2, scoped to project status and the Docker state. Metrics and live logs are not on it yet.

- **One shared read.** `GET /api/events` sends a `state` event (`LiveState {dockerUp, projects}`) to every subscriber. The hub runs one `docker.containers` read every 5 seconds, ticks never overlap, and it does nothing while nobody is connected (decision 3). A new subscriber gets the current state straight away.
- **Stale reads are dropped.** A generation counter, bumped on every lock change, discards a read that started before the change (a lock taken or released) and reads again, up to 3 reads, so the post-command status is not overwritten by an older one.
- **Retry.** `POST /api/docker/check` asks the hub for one fresh read and returns the result, so the Docker banner can recover without waiting for the next tick.
- **The action response travels with the action.** `POST /api/projects/{id}/{action}` waits for the command and returns the outcome, the status read from Docker afterwards and the actions now allowed. The toast does not wait for the stream. The stream still carries the in-between status (Starting…, Stopping…, Restarting…) to other tabs while the command runs. This refines the consequence "actions and results travel on different channels": the action's own result comes back on the action request.
- **Client.** One `EventSource` for the whole app, in `LiveProvider`. It receives the heartbeat of decision 4 and reconnects on its own.
