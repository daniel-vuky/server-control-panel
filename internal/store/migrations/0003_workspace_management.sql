-- Workspace management (#19, #24, #25): a tracked project whose folder a rescan
-- could not find gets missing_since (UTC RFC 3339); it is cleared when the
-- folder is found again. NULL means present.

ALTER TABLE projects ADD COLUMN missing_since TEXT;
