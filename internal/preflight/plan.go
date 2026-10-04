package preflight

import "sort"

func productKey(doc document) string { return doc.product.Namespace + "/" + doc.product.Name }

// reviewPlan counts distinct producers while retaining every declared port edge.
func reviewPlan(documents []document) *ReviewPlan {
	plan := &ReviewPlan{Order: []PlanProduct{}, Edges: []PlanEdge{}}
	byKey := map[string]document{}
	needs := map[string]map[string]bool{}
	for _, doc := range documents {
		key := productKey(doc)
		byKey[key] = doc
		needs[key] = map[string]bool{}
		for index, input := range doc.product.Spec.Inputs {
			namespace := input.ProductRef.Namespace
			if namespace == "" {
				namespace = doc.product.Namespace
			}
			producer := namespace + "/" + input.ProductRef.Name
			needs[key][producer] = true
			plan.Edges = append(
				plan.Edges,
				PlanEdge{
					Consumer:   key,
					Producer:   producer,
					Input:      input.Name,
					InputIndex: index,
					Output:     input.ProductRef.Output,
					Source:     doc.origin.Source,
					Document:   doc.origin.Document,
				},
			)
		}
	}
	for len(needs) > 0 {
		ready := []string{}
		for key, required := range needs {
			if len(required) == 0 {
				ready = append(ready, key)
			}
		}
		sort.Strings(ready)
		if len(ready) == 0 {
			return nil
		}
		key := ready[0]
		doc := byKey[key]
		plan.Order = append(
			plan.Order,
			PlanProduct{Key: key, Source: doc.origin.Source, Document: doc.origin.Document},
		)
		delete(needs, key)
		for _, required := range needs {
			delete(required, key)
		}
	}
	sort.Slice(plan.Edges, func(i, j int) bool {
		a, b := plan.Edges[i], plan.Edges[j]
		if a.Consumer != b.Consumer {
			return a.Consumer < b.Consumer
		}
		if a.Input != b.Input {
			return a.Input < b.Input
		}
		if a.Producer != b.Producer {
			return a.Producer < b.Producer
		}
		return a.Output < b.Output
	})
	return plan
}
