LOAD '$libdir/plugins/age';
SET search_path = ag_catalog, public;
SELECT * FROM cypher('lineage', $$
  MATCH (n:product {id:'target'}) DETACH DELETE n
$$) AS (node agtype);
