# 7. Run background jobs inside the panel process

Date: 2026-10-28

## Status

Accepted

## Context

Several things must happen without anyone watching:

- metric sampling every 60 seconds (E6-F20-US01);
- pruning metrics past the retention period;
- checking each public domain every 60 seconds, alerting after 3 consecutive failures (E7-F23-US01);
- scheduled backups per database (E5-F16-US01);
- project scans (E2-F04-US02).

Backups of the same database must never overlap, and **Back up all now** runs them one after another. The panel must stay light, and the server has one panel instance. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: An in-process scheduler with a jobs table in SQLite

- Pro: nothing extra to run, and job state is queryable for progress screens.

### Option B: A separate worker with a queue (BullMQ/Redis or similar)

- Con: an extra process and an extra service. It only pays off with multiple workers, which this panel will never have.

## Decision

All background work runs **inside the panel process**.

1. **Tickers** run metric sampling (60 s), domain checks (60 s) and retention pruning (daily).
2. **Per-database backup schedules** use `robfig/cron/v3`.
3. **A `jobs` table** in SQLite records scans and backups: kind, target, state, steps, error, who started it and when.
4. **A single backup worker** takes backups from a queue one at a time.
5. **On startup**, jobs left in `running` state by a crash or restart are marked `interrupted`.
6. The panel samples its own process and lists itself in usage by app.

## Consequences

**Easier:**

- One process to deploy and monitor.
- Job progress and history come from the same database as everything else.

**Harder:**

- A panel restart interrupts running jobs. They are marked rather than resumed.
- Scheduling only runs while the panel is up.
