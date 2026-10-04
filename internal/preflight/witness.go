package preflight

import (
	"context"
	"fmt"
	"sort"
)

// graphWitness memoizes suffix paths so diamonds preserve the full depth decision.
func graphWitness(ctx context.Context, documents []document) (int, string, []WitnessStep, bool) {
	byKey := map[string]document{}
	ordinals := map[string]int{}
	keys := []string{}
	for index, doc := range documents {
		key := productKey(doc)
		byKey[key] = doc
		ordinals[key] = index + 1
		keys = append(keys, key)
	}
	sort.Strings(keys)
	active := map[string]int{}
	suffixes := map[string][]WitnessStep{}
	stack := []WitnessStep{}
	var visit func(string) ([]WitnessStep, string, []WitnessStep)
	visit = func(key string) ([]WitnessStep, string, []WitnessStep) {
		if ctx.Err() != nil {
			return nil, "ValidationLimit", nil
		}
		if index, ok := active[key]; ok {
			return nil, "CompositionCycle", append([]WitnessStep{}, stack[index:]...)
		}
		if cached, ok := suffixes[key]; ok {
			if len(stack)+len(cached) >= 64 {
				return nil, "CompositionLimit", append(append([]WitnessStep{}, stack...), cached...)
			}
			return cached, "", nil
		}
		if len(stack) >= 64 {
			return nil, "CompositionLimit", append([]WitnessStep{}, stack...)
		}
		active[key] = len(stack)
		doc := byKey[key]
		var longest []WitnessStep
		for index, input := range doc.product.Spec.Inputs {
			namespace := input.ProductRef.Namespace
			if namespace == "" {
				namespace = doc.product.Namespace
			}
			producer := namespace + "/" + input.ProductRef.Name
			if _, ok := byKey[producer]; !ok {
				continue
			}
			step := WitnessStep{
				Source:   doc.origin.Source,
				Document: doc.origin.Document,
				Path:     fmt.Sprintf("/spec/inputs/%d/productRef", index),
			}
			stack = append(stack, step)
			suffix, code, witness := visit(producer)
			stack = stack[:len(stack)-1]
			if code != "" {
				return nil, code, witness
			}
			candidate := append([]WitnessStep{step}, suffix...)
			if len(candidate) > len(longest) {
				longest = candidate
			}
		}
		delete(active, key)
		suffixes[key] = longest
		return longest, "", nil
	}
	for _, key := range keys {
		_, code, witness := visit(key)
		if code != "" {
			number := ordinals[key]
			if len(witness) > 0 {
				first := witness[0]
				for index, doc := range documents {
					if doc.origin.Source == first.Source && doc.origin.Document == first.Document {
						number = index + 1
						break
					}
				}
			}
			truncated := len(witness) > 64
			if truncated {
				witness = witness[:64]
			}
			return number, code, witness, truncated
		}
	}
	return 0, "", nil, false
}
