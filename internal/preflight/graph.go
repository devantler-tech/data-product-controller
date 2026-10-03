package preflight

import (
	"context"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/controller"
)

func checkGraph(ctx context.Context, report *Report, documents []document) {
	products := make(map[string]*data.DataProduct)
	edges := 0
	for index := range documents {
		product := &documents[index].product
		products[product.Namespace+"/"+product.Name] = product
		edges += len(product.Spec.Inputs)
	}
	if edges > 1024 {
		report.add(0, "error", "CompositionLimit", "spec.inputs")
		return
	}
	for index := range documents {
		product := &documents[index].product
		for _, input := range product.Spec.Inputs {
			namespace := input.ProductRef.Namespace
			if namespace == "" {
				namespace = product.Namespace
			}
			if namespace != product.Namespace {
				report.add(index+1, "error", "CrossNamespaceInput", "spec.inputs")
				continue
			}
			producer := products[namespace+"/"+input.ProductRef.Name]
			if producer == nil {
				report.add(index+1, "warning", "ProducerUnresolved", "spec.inputs")
				continue
			}
			if code := controller.DeclaredInputCompatibility(input, producer); code != "" {
				report.add(index+1, "error", code, "spec.inputs")
			}
		}
	}
	if !report.Valid {
		return
	}
	visiting, depths := make(map[string]bool), make(map[string]int)
	var visit func(string) (int, string)
	visit = func(key string) (int, string) {
		if ctx.Err() != nil {
			return 0, "ValidationLimit"
		}
		if visiting[key] {
			return 0, "CompositionCycle"
		}
		if depth, ok := depths[key]; ok {
			return depth, ""
		}
		visiting[key] = true
		depth := 0
		for _, input := range products[key].Spec.Inputs {
			producer := products[key].Namespace + "/" + input.ProductRef.Name
			if products[producer] == nil {
				continue
			}
			child, code := visit(producer)
			if code != "" {
				return 0, code
			}
			depth = max(depth, child+1)
			if depth >= 64 {
				return 0, "CompositionLimit"
			}
		}
		visiting[key] = false
		depths[key] = depth
		return depth, ""
	}
	for index := range documents {
		product := &documents[index].product
		if _, code := visit(product.Namespace + "/" + product.Name); code != "" {
			report.add(index+1, "error", code, "spec.inputs")
			return
		}
	}
}
