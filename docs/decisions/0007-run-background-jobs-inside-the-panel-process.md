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

## Note (2026-10-30)

The project lifecycle flow (#16, #39 to #43) added the `activity` table next to `jobs` (migration 0002).

- **Append-only.** Each row records who did what: actor (the verified email from `auth.FromContext`, or `schedule` for the jobs of this ADR), action (`project.start`, `project.stop`, `project.restart`, `scan.start`, `scan.track`), target, result and a masked reason. The store offers two inserts (a `running` row, or a row recorded already finished, such as a refusal), one guarded update from `running` to the final result and a reader of recent rows, `RecentActivity`. Nothing deletes rows, and `project_id` has no foreign key so a record outlives its project.
- **Results.** `running`, `ok`, `failed`, `timed_out`, `refused` (locked, not allowed, Docker down) and `interrupted`.
- **Interrupted rows.** As with jobs (decision 5), a row left `running` by a crash or restart is marked `interrupted` when the store reopens.
- **Reading it.** The timeline and its read API are #64 and not built yet.

## Note (2026-10-30, #24)

Rescans of tracked workspaces (#24) run as `scan` jobs: the same table, the same single-flight rule and the same progress stream as a first scan. What marks a rescan is `params.rescan`, the list of workspace ids (`{"depth":0,"rescan":[...]}`), and `ScanJob.rescan` on the API.

- **One scan at a time.** A rescan is refused with 409 while any scan runs, and the 409 body tells the client whether the running job is a rescan.
- **`WhileIdle`.** Removing a workspace must not race a scan of it. The scan manager's `WhileIdle(fn)` runs `fn` only while no scan runs, and a scan cannot start until `fn` returns.
- **Result.** The rescan stores its result on the job like a scan; the store applies it (`ApplyRescan`) when it finishes, and `scan.track` on the rescan job adds the New rows the owner picked. The activity record uses `workspace.rescan`.
