-- Schema version 5: track pre-conversion archive size for flattened 7z archives.

ALTER TABLE archives ADD COLUMN convert_source_size_bytes INTEGER;
