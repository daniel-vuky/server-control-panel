-- Workspaces, the projects found in them, the databases those projects use,
-- and background jobs. Times are UTC RFC 3339 text.

CREATE TABLE workspaces (
    id           INTEGER PRIMARY KEY,
    name         TEXT    NOT NULL,
    path         TEXT    NOT NULL UNIQUE,
    depth        INTEGER NOT NULL CHECK (depth BETWEEN 1 AND 3),
    created_at   TEXT    NOT NULL,
    last_scan_at TEXT    NOT NULL
);

CREATE TABLE projects (
    id              INTEGER PRIMARY KEY,
    workspace_id    INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    path            TEXT    NOT NULL UNIQUE,
    compose_files   TEXT    NOT NULL DEFAULT '[]', -- JSON list: the main file, then the override
    compose_project TEXT    NOT NULL,
    display_name    TEXT    NOT NULL,
    state           TEXT    NOT NULL CHECK (state IN ('tracked', 'ignored')),
    self            INTEGER NOT NULL DEFAULT 0 CHECK (self IN (0, 1)),
    details         TEXT    NOT NULL DEFAULT '{}', -- JSON: services with image, ports, volumes
    created_at      TEXT    NOT NULL
);
CREATE INDEX projects_workspace_id ON projects (workspace_id);

CREATE TABLE databases (
    id            INTEGER PRIMARY KEY,
    project_id    INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    type          TEXT    NOT NULL CHECK (type IN ('postgresql', 'mysql', 'mongodb', 'sqlite')),
    name          TEXT    NOT NULL,
    location_kind TEXT    NOT NULL CHECK (location_kind IN ('container', 'host', 'external', 'file', 'unknown')),
    location      TEXT    NOT NULL,
    sources       TEXT    NOT NULL DEFAULT '[]' -- JSON list of variable or service names, never values
);
CREATE INDEX databases_project_id ON databases (project_id);

CREATE TABLE jobs (
    id          INTEGER PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('scan')),
    target      TEXT NOT NULL,
    params      TEXT NOT NULL DEFAULT '{}', -- JSON: {"depth": n}
    state       TEXT NOT NULL CHECK (state IN ('running', 'done', 'failed', 'interrupted')),
    progress    TEXT,
    result      TEXT,
    error       TEXT,
    started_by  TEXT NOT NULL,
    started_at  TEXT NOT NULL,
    finished_at TEXT
);
