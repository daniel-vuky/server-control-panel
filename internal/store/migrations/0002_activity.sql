-- The activity record (#16): who did what, append-only. The store exposes only
-- an insert and a one-time running -> final update; nothing deletes rows.
-- Times are UTC RFC 3339 text.

CREATE TABLE activity (
    id           INTEGER PRIMARY KEY,
    started_at   TEXT NOT NULL,              -- UTC RFC 3339
    finished_at  TEXT,
    actor        TEXT NOT NULL,              -- verified email, or 'schedule'
    actor_kind   TEXT NOT NULL CHECK (actor_kind IN ('person', 'schedule')),
    action       TEXT NOT NULL,              -- project.start | project.stop | project.restart | scan.start | scan.track
    project_id   INTEGER,                    -- no FK: the record outlives the project
    target       TEXT NOT NULL,              -- project display name, or the workspace path for scans
    result       TEXT NOT NULL CHECK (result IN ('running', 'ok', 'failed', 'timed_out', 'refused', 'interrupted')),
    reason       TEXT NOT NULL DEFAULT ''    -- masked; never a secret value
);
CREATE INDEX activity_started_at ON activity (started_at);
