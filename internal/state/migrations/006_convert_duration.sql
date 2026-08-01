-- Schema version 6: persist completed 7z flatten duration per archive (once).

ALTER TABLE archives ADD COLUMN convert_duration_seconds REAL;
