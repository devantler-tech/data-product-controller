LOAD '$libdir/plugins/age';
SET search_path = ag_catalog, public;
SELECT * FROM cypher('lineage', $$
  CREATE (:product {id:'forbidden'}) RETURN 1
$$) AS (result agtype);
