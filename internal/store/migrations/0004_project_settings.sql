-- Project settings (#38): a public domain, the stop template and the alert
-- thresholds. NULL public_domain means unset; NULL thresholds mean the server
-- default. Scans and rescans never touch these columns.

ALTER TABLE projects ADD COLUMN public_domain TEXT;
ALTER TABLE projects ADD COLUMN stop_template TEXT NOT NULL DEFAULT 'stop' CHECK (stop_template IN ('stop', 'down'));
ALTER TABLE projects ADD COLUMN alert_cpu_percent INTEGER;
ALTER TABLE projects ADD COLUMN alert_ram_mb INTEGER;
