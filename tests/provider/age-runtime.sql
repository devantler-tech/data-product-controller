-- Run only through the independent owner's local PostgreSQL session.
-- Readers receive no settings privilege, and the assertion exposes only a boolean.
SELECT EXISTS (
  SELECT FROM unnest(string_to_array(current_setting('shared_preload_libraries'), ',')) AS library(name)
  WHERE btrim(name) = 'age'
) AND EXISTS (
  SELECT FROM pg_catalog.pg_extension WHERE extname = 'age' AND extversion = '1.7.0'
);
