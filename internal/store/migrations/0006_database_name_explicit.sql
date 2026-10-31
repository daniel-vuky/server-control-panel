-- Whether a database's name came from a variable or URL (MYSQL_DATABASE: db)
-- rather than the service-name placeholder detection falls back to, so the
-- backup rule never takes an explicit name equal to its service for a missing
-- one. Rows stored before this read 0 until the next rescan.

ALTER TABLE databases ADD COLUMN name_explicit INTEGER NOT NULL DEFAULT 0 CHECK (name_explicit IN (0, 1));
