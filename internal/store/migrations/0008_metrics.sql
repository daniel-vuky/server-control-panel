-- Server and project metrics (#63, #59, #36). Two tiers: raw samples every
-- 30 s, kept 25 hours, and 5-minute averages ("rollups", ts = bucket start, a
-- multiple of 300), kept for the retention period. ts is unix seconds, not
-- RFC 3339 text like elsewhere, because the reads bucket it with integer
-- division. Every value is nullable: a reading that failed is a gap, never a
-- zero.

CREATE TABLE metrics_settings (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    retention_days INTEGER NOT NULL CHECK (retention_days BETWEEN 7 AND 30),
    updated_at     TEXT    NOT NULL
);

INSERT INTO metrics_settings (id, retention_days, updated_at)
VALUES (1, 7, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

CREATE TABLE server_samples (
    ts         INTEGER PRIMARY KEY,
    cpu        REAL,
    mem_used   INTEGER,
    mem_total  INTEGER,
    disk_read  REAL,
    disk_write REAL,
    net_in     REAL,
    net_out    REAL
);

CREATE TABLE server_rollups (
    ts         INTEGER PRIMARY KEY,
    cpu        REAL,
    mem_used   INTEGER,
    mem_total  INTEGER,
    disk_read  REAL,
    disk_write REAL,
    net_in     REAL,
    net_out    REAL
);

-- A project's rows go with it (ON DELETE CASCADE); untracking, which keeps the
-- project as an ignored row, deletes them itself.
CREATE TABLE project_samples (
    project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    ts         INTEGER NOT NULL,
    cpu        REAL,
    mem        INTEGER,
    net_in     REAL,
    net_out    REAL,
    PRIMARY KEY (project_id, ts)
) WITHOUT ROWID;

CREATE TABLE project_rollups (
    project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    ts         INTEGER NOT NULL,
    cpu        REAL,
    mem        INTEGER,
    net_in     REAL,
    net_out    REAL,
    PRIMARY KEY (project_id, ts)
) WITHOUT ROWID;

-- The prune deletes by age across every project.
CREATE INDEX project_samples_ts_idx ON project_samples (ts);
CREATE INDEX project_rollups_ts_idx ON project_rollups (ts);
