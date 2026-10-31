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
- **Result.** The rescan stores its result on the job like a scan; the scan manager applies it (`store.ApplyRescan`) inside the job, before the job is stored as done, and then calls `OnChange`; and `scan.track` on the rescan job adds the New rows the owner picked. The activity record uses `workspace.rescan`.

## Note (2026-10-31, manual backup)

Manual backups (#48, #49, #56) are not stored in the `jobs` table. They have their own `backups` table (migration 0005), because the history needs columns per backup: the project and database snapshot, the file name, the Drive file id and link, the size, the steps with their progress and the error and hint. This refines decision 3 (the `jobs` table records scans and backups): scans stay in `jobs`, a backup is recorded in `backups`, and the `activity` record (`backup.run`) is written beside it as before.

Decisions 4 and 5 stand for backups: one worker takes one backup at a time inside the panel process, queued ones wait in order, and a backup left `queued` or `running` by a crash or restart becomes `interrupted` when the store reopens (the manager also removes leftover temporary files when it starts).

## Note (2026-10-31, scheduled backups)

Scheduled backups (#53), retention (#54), backup defaults (#55) and Back up all (#52) replace decision 2.

- **Our own scheduler, not `robfig/cron`.** A schedule is Off, Daily at `HH:MM` or Weekly on a day at `HH:MM`, in the server zone (`TZ`). Three frequencies need no cron parser, so `backup.Scheduler` is a small loop in the panel process: every 30 s (after the previous tick ends) it reads the enabled schedules in one SQLite query and queues a schedule whose latest occurrence is later than both its `last_run_at` and its `updated_at` and at most 60 minutes old. `last_run_at` is stored before the backup is queued, so a due time never runs twice across a crash or restart; an outage longer than an hour skips that run, with no catch-up. A saved schedule never fires for a time already passed.
- **Schedules keyed by `db_key`.** The `backup_schedules` table (migration 0007) is keyed by `(project_id, db_key)` rather than stored on the `databases` rows, which every rescan replaces, so a schedule survives rescans. A row whose database is no longer detected is kept but never fires; untracking a project deletes its schedules. The upgrade seeds every existing database Off, so it starts no backup by itself; databases found later start from the `backup_defaults` row.
- **One worker for everything.** A scheduled run goes through the same `Manager.Enqueue` as a manual one (actor `schedule`), with the same checks and refused rows, and Back up all queues its databases one at a time through it too. Decision 4 stands: one worker runs one backup at a time, so two backups never race, and retention (removing the copies past the number to keep, after a successful upload) runs as that backup's last step.

## Note (2026-10-31, server metrics)

Server and project metrics (#59 to #63, #36) refine decision 1:

- **Sampling every 30 seconds, not 60.** The issue allows 30 to 60 seconds, design 05 says "sampled every 30 seconds", and 30 seconds gives the 1-hour chart 120 points. `metrics.Sampler` ticks start to start and its ticks never overlap (a tick that overruns skips the starts it missed), whether or not anyone watches. Rates are deltas over the real time between two readings; the first tick after a start only sets the baselines, and a reading more than 3 minutes after its baseline stores a gap, not an average over the stall.
- **Two tiers.** Raw 30-second samples are kept for 25 hours; 5-minute averages (rollups) are kept for the retention period (7 to 30 days, default 7, a Settings value). The 1-hour chart reads raw samples, 24 hours the rollups (288 points) and 7 days 30-minute averages of the rollups (336 points); the bucket containing now always comes from raw samples. A failed read is stored as a gap, never a zero. This keeps the database to a few MB.
- **Pruning every hour, not daily.** The sampler rolls up once a 5-minute boundary has passed and prunes every hour from the same loop, always rolling up first, so no bucket is pruned before it is rolled. Saving a lower retention prunes at once and is recorded as `metrics.retention`.
- **Disk usage every hour.** The disk use per app (`docker system df -v` for the volumes, then a capped walk of each project folder, one at a time) runs 2 minutes after start and then 60 minutes after each read ends, because the daemon walks every volume and layer for it. It is kept in memory only.
