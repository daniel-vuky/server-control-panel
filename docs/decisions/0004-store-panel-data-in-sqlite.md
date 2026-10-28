# 4. Store panel data in SQLite

Date: 2026-10-28

## Status

Accepted

## Context

The panel keeps three kinds of data:

- **Configuration:** workspaces, tracked and ignored projects, databases, backup settings and defaults, alert channels and thresholds, the Google Drive token (encrypted at rest, E1-F02-US04) and app settings.
- **Records:** backup history (time, size, result, Drive file, steps, error), the activity log (who, what, when, result) and alert incidents.
- **Time series:** server and per-project metrics sampled every 60 seconds and kept for a retention period of 7–30 days (default 7).

Project logs are read live and not stored. Project database passwords are never stored. The volume is small: 3 projects plus the server at 60-second samples for 7 days is about 40k metric rows. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: SQLite

- Pro: one file, no extra service, near-zero extra RAM.
- Pro: handles this volume easily.
- Con: a single writer at a time, which is fine for one panel process.

### Option B: PostgreSQL

- Con: another service to run, update and back up.

### Option C: JSON files

- Con: no queries over history, and concurrent writes are risky.

## Decision

The panel stores everything in **one SQLite file**, `$PANEL_DATA_DIR/panel.db` (`/data/panel.db` in the container).

1. Driver: `modernc.org/sqlite` (pure Go, no CGO), opened with WAL journal mode, a busy timeout and foreign keys on.
2. Schema changes are numbered SQL files embedded in the binary. They are applied in order at startup, each in a transaction, and tracked in a `schema_migrations` table. A migration that has been applied is never edited.
3. A daily job deletes metric samples older than the retention period.

## Consequences

**Easier:**

- No database service to run.
- Backing up the panel itself is copying one file.

**Harder:**

- Heavy concurrent writes would need care. None are expected.
