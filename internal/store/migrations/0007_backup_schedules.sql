-- Backup schedules (#53), retention (#54) and defaults (#55). A schedule is
-- per database, keyed by (project_id, db_key) rather than stored on the
-- databases rows, so it survives the rescans that replace those rows. db_key
-- is type:location:name, the same as project.DatabaseKey. Times are UTC
-- RFC 3339 text.

CREATE TABLE backup_defaults (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    frequency  TEXT    NOT NULL CHECK (frequency IN ('off', 'daily', 'weekly')),
    run_time   TEXT    NOT NULL,
    weekday    INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    keep       INTEGER NOT NULL CHECK (keep BETWEEN 1 AND 365),
    compress   INTEGER NOT NULL CHECK (compress IN (0, 1)),
    updated_at TEXT    NOT NULL
);

INSERT INTO backup_defaults (id, frequency, run_time, weekday, keep, compress, updated_at)
VALUES (1, 'daily', '02:00', 0, 14, 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

CREATE TABLE backup_schedules (
    project_id  INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    db_key      TEXT    NOT NULL,
    frequency   TEXT    NOT NULL CHECK (frequency IN ('off', 'daily', 'weekly')),
    run_time    TEXT    NOT NULL,
    weekday     INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    keep        INTEGER NOT NULL CHECK (keep BETWEEN 1 AND 365),
    compress    INTEGER NOT NULL CHECK (compress IN (0, 1)),
    last_run_at TEXT,
    updated_at  TEXT    NOT NULL,
    PRIMARY KEY (project_id, db_key)
);

-- The upgrade never starts backups by itself: every database of a tracked
-- project gets an Off schedule. Two detected databases can share a key, so
-- the second is ignored.
INSERT OR IGNORE INTO backup_schedules (project_id, db_key, frequency, run_time, weekday, keep, compress, updated_at)
SELECT d.project_id, d.type || ':' || d.location || ':' || d.name, 'off', '02:00', 0, 14, 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM databases d JOIN projects p ON p.id = d.project_id
WHERE p.state = 'tracked';

-- Retention stamps the rows whose Drive file it removed.
ALTER TABLE backups ADD COLUMN pruned_at TEXT;

CREATE INDEX backups_db_idx ON backups (project_id, db_key, id DESC);
