-- Schema version 3: persist completed FUSE mount duration per archive (once).

ALTER TABLE archives ADD COLUMN mount_duration_seconds REAL;
