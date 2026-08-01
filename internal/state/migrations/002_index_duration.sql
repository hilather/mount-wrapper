-- Schema version 2: persist completed index build duration per archive.

ALTER TABLE archives ADD COLUMN index_duration_seconds REAL;
