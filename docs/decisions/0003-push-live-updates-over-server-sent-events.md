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
