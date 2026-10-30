-- Manual backups (#45, #48): the one Google Drive connection, the single-use
-- OAuth states of the connect flow, and the backup history. The refresh token
-- is stored sealed ("v1:<base64>"), never in the clear.

CREATE TABLE drive (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    refresh_token_enc TEXT NOT NULL,
    email             TEXT NOT NULL,
    folder_id         TEXT,
    folder_name       TEXT,
    state             TEXT NOT NULL CHECK (state IN ('connected', 'expired')),
    connected_by      TEXT NOT NULL,
    connected_at      TEXT NOT NULL
);

CREATE TABLE drive_oauth_states (
    state      TEXT PRIMARY KEY,
    actor      TEXT NOT NULL,
    return_to  TEXT NOT NULL,
    redirect   TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- project_id has no foreign key, so the history outlives the project.
CREATE TABLE backups (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL,
    project_name     TEXT NOT NULL,
    db_type          TEXT NOT NULL,
    db_name          TEXT NOT NULL,
    db_location_kind TEXT NOT NULL,
    db_location      TEXT NOT NULL,
    db_service       TEXT NOT NULL DEFAULT '',
    db_key           TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('queued', 'running', 'ok', 'failed', 'refused', 'interrupted')),
    failed_step      TEXT CHECK (failed_step IS NULL OR failed_step IN ('dump', 'compress', 'upload', 'prune')),
    steps            TEXT NOT NULL DEFAULT '[]',
    error            TEXT NOT NULL DEFAULT '',
    hint             TEXT NOT NULL DEFAULT '',
    raw_bytes        INTEGER NOT NULL DEFAULT 0,
    size_bytes       INTEGER NOT NULL DEFAULT 0,
    file_name        TEXT NOT NULL DEFAULT '',
    drive_file_id    TEXT NOT NULL DEFAULT '',
    drive_link       TEXT NOT NULL DEFAULT '',
    started_by       TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    started_at       TEXT,
    finished_at      TEXT,
    duration_ms      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX backups_project_idx ON backups (project_id, id DESC);
CREATE INDEX backups_state_idx ON backups (state);
