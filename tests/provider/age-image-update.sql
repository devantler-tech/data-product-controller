LOAD '$libdir/plugins/age';
SET search_path = ag_catalog, public;
SELECT * FROM cypher('lineage', $$
  MATCH (n:product {id:'middle'}) SET n.id='changed' RETURN n
$$) AS (node agtype);
