CREATE EXTENSION age;
DO $$
BEGIN
  IF (SELECT extversion FROM pg_extension WHERE extname = 'age') <> '1.7.0' THEN
    RAISE EXCEPTION 'unexpected AGE extension version';
  END IF;
END $$;
LOAD 'age';
SET search_path = ag_catalog, public;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE ROLE age_writer LOGIN PASSWORD 'synthetic-age-writer' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE age_reader LOGIN PASSWORD 'synthetic-age-reader' NOSUPERUSER NOCREATEDB NOCREATEROLE;
SELECT create_graph('lineage');
SELECT * FROM cypher('lineage', $$
  CREATE (:product {id:'source'})-[:feeds]->(:product {id:'middle'})-[:feeds]->(:product {id:'target'})
$$) AS (result agtype);
REVOKE ALL ON DATABASE postgres FROM PUBLIC;
GRANT CONNECT ON DATABASE postgres TO age_writer, age_reader;
GRANT USAGE ON SCHEMA ag_catalog, lineage TO age_writer, age_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA lineage TO age_reader;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA lineage TO age_writer;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA lineage TO age_writer;
