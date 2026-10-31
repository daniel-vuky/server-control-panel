-- Dashboard summary tiles (#27, #28, #29): the server's one-minute load
-- average, disk space (the sum over the listed disks) and the Cloudflare
-- Tunnel state (1 connected, 0 disconnected or not running) join the server
-- series. Nullable like every other value: a reading that failed is a gap,
-- never a zero, and rows stored before this migration read as gaps.

ALTER TABLE server_samples ADD COLUMN load1 REAL;
ALTER TABLE server_samples ADD COLUMN disk_used INTEGER;
ALTER TABLE server_samples ADD COLUMN disk_total INTEGER;
ALTER TABLE server_samples ADD COLUMN tunnel REAL;

ALTER TABLE server_rollups ADD COLUMN load1 REAL;
ALTER TABLE server_rollups ADD COLUMN disk_used INTEGER;
ALTER TABLE server_rollups ADD COLUMN disk_total INTEGER;
ALTER TABLE server_rollups ADD COLUMN tunnel REAL;
