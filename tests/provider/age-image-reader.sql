LOAD '$libdir/plugins/age';
SET search_path = ag_catalog, public;
SELECT (
  jsonb_agg(node::text::jsonb) =
  '[{"id":"middle","depth":1},{"id":"target","depth":2}]'::jsonb
)::int
FROM cypher('lineage', $$
  MATCH p=(source:product {id:'source'})-[:feeds*1..2]->(target:product)
  RETURN {id:target.id, depth:length(p)} ORDER BY length(p)
$$) AS (node agtype);
