-- Databases the owner added by hand (#58), kept apart from the detected rows
-- in `databases` so a rescan never replaces them. A row has its own backup key,
-- manual:<id> (project.DatabaseKey), so it never collides with a detected
-- database and its schedule (backup_schedules.db_key) is its own. The ids are
-- never reused (AUTOINCREMENT), so a removed row's key never points at a later
-- database's backups. user_var and password_var are environment variable
-- names read inside the container, never values; both empty means the image's
-- standard variables. Times are UTC RFC 3339 text.

CREATE TABLE manual_databases (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    type         TEXT    NOT NULL CHECK (type IN ('postgresql', 'mysql', 'mongodb')),
    name         TEXT    NOT NULL,
    service      TEXT    NOT NULL,
    user_var     TEXT    NOT NULL DEFAULT '',
    password_var TEXT    NOT NULL DEFAULT '',
    created_by   TEXT    NOT NULL,
    created_at   TEXT    NOT NULL
);
CREATE INDEX manual_databases_project_id ON manual_databases (project_id);
