-- Alerts and the Activity page (#64, #65, #66). The activity table is rebuilt to
-- widen two CHECKs (actor_kind gains 'system'; result gains 'sent' and
-- 'not_sent') and to add the channels an alert went to. Every row is copied with
-- its id, so the history and its order stay. Nothing references activity, so
-- foreign_keys on does not get in the way. It stays append-only. Then the alert
-- thresholds, the channels and the open incidents. Times are UTC RFC 3339 text.

CREATE TABLE activity_new (
    id           INTEGER PRIMARY KEY,
    started_at   TEXT NOT NULL,              -- UTC RFC 3339
    finished_at  TEXT,
    actor        TEXT NOT NULL,              -- verified email, 'schedule' or 'system'
    actor_kind   TEXT NOT NULL CHECK (actor_kind IN ('person', 'schedule', 'system')),
    action       TEXT NOT NULL,
    project_id   INTEGER,                    -- no FK: the record outlives the project
    target       TEXT NOT NULL,
    result       TEXT NOT NULL CHECK (result IN ('running', 'ok', 'failed', 'timed_out', 'refused', 'interrupted', 'sent', 'not_sent')),
    reason       TEXT NOT NULL DEFAULT '',   -- masked; never a secret value
    channels     TEXT NOT NULL DEFAULT ''    -- comma-separated: telegram,email
);

INSERT INTO activity_new (id, started_at, finished_at, actor, actor_kind, action, project_id, target, result, reason)
SELECT id, started_at, finished_at, actor, actor_kind, action, project_id, target, result, reason FROM activity;

DROP TABLE activity;
ALTER TABLE activity_new RENAME TO activity;
CREATE INDEX activity_started_at ON activity (started_at);
CREATE INDEX activity_project ON activity (project_id, id);

CREATE TABLE alert_settings (
    id                    INTEGER PRIMARY KEY CHECK (id = 1),
    cpu_percent           INTEGER NOT NULL CHECK (cpu_percent BETWEEN 1 AND 100),
    ram_percent           INTEGER NOT NULL CHECK (ram_percent BETWEEN 1 AND 100),
    disk_percent          INTEGER NOT NULL CHECK (disk_percent BETWEEN 1 AND 100),
    sustained_minutes     INTEGER NOT NULL CHECK (sustained_minutes BETWEEN 1 AND 60),
    tunnel_seen_connected INTEGER NOT NULL DEFAULT 0 CHECK (tunnel_seen_connected IN (0, 1))
);

INSERT INTO alert_settings (id, cpu_percent, ram_percent, disk_percent, sustained_minutes) VALUES (1, 90, 90, 85, 5);

CREATE TABLE alert_channels (
    kind         TEXT PRIMARY KEY CHECK (kind IN ('telegram', 'email')),
    enabled      INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    sealed_token TEXT NOT NULL DEFAULT '',   -- secret.Seal output; never the token
    chat_id      TEXT NOT NULL DEFAULT '',
    recipients   TEXT NOT NULL DEFAULT '',   -- newline-joined
    updated_at   TEXT NOT NULL
);

CREATE TABLE alert_incidents (
    key         TEXT PRIMARY KEY,
    project_id  INTEGER REFERENCES projects (id) ON DELETE CASCADE,
    opened_at   TEXT NOT NULL,
    activity_id INTEGER                      -- the alert's activity row; no FK, like activity.project_id
);
