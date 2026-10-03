-- Executed by the independent database bootstrap owner, never by a query reader.
CREATE EXTENSION age;
LOAD 'age';
SET search_path = ag_catalog, public;
DO $$
DECLARE role_name text;
BEGIN
  FOREACH role_name IN ARRAY ARRAY['sql_reader', 'document_reader', 'graph_reader', 'catalog_writer'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = role_name) THEN
      EXECUTE format('CREATE ROLE %I NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION', role_name);
    END IF;
  END LOOP;
END $$;
ALTER DATABASE catalog OWNER TO postgres;
REVOKE ALL ON DATABASE catalog FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE catalog TO sql_reader, document_reader, graph_reader, catalog_writer;
GRANT USAGE ON SCHEMA public TO sql_reader, document_reader, catalog_writer;
CREATE TABLE public.catalog_rows (id text PRIMARY KEY, value text NOT NULL);
CREATE TABLE public.documents (id text PRIMARY KEY, payload jsonb NOT NULL);
ALTER TABLE public.catalog_rows OWNER TO catalog_writer;
ALTER TABLE public.documents OWNER TO catalog_writer;
GRANT SELECT ON public.catalog_rows TO sql_reader;
GRANT SELECT ON public.documents TO document_reader;
SELECT create_graph('lineage');
SELECT create_vlabel('lineage', 'product');
SELECT create_elabel('lineage', 'feeds');
GRANT USAGE ON SCHEMA ag_catalog, lineage TO graph_reader, catalog_writer;
GRANT SELECT ON ALL TABLES IN SCHEMA lineage TO graph_reader;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA lineage TO catalog_writer;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA lineage TO catalog_writer;
