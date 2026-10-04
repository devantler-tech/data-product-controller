package preflight

import (
	"context"
	"fmt"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/controller"
)

// checkGraph validates bounded local dependencies while keeping missing producers unresolved.
func checkGraph(ctx context.Context, report *Report, documents []document) {
	products := make(map[string]*data.DataProduct)
	edges := 0
	for index := range documents {
		product := &documents[index].product
		products[product.Namespace+"/"+product.Name] = product
		edges += len(product.Spec.Inputs)
	}
	if edges > 1024 {
		report.add(0, "CompositionLimit", "spec.inputs")
		return
	}
	for index := range documents {
		product := &documents[index].product
		for inputIndex, input := range product.Spec.Inputs {
			path := fmt.Sprintf("/spec/inputs/%d/productRef", inputIndex)
			namespace := input.ProductRef.Namespace
			if namespace == "" {
				namespace = product.Namespace
			}
			if namespace != product.Namespace {
				report.addAt(index+1, "error", "CrossNamespaceInput", "spec.inputs", path)
				continue
			}
			producer := products[namespace+"/"+input.ProductRef.Name]
			if producer == nil {
				report.addAt(index+1, "warning", "ProducerUnresolved", "spec.inputs", path)
				continue
			}
			if code := controller.DeclaredInputCompatibility(input, producer); code != "" {
				if code == "OutputNotFound" {
					path += "/output"
				} else {
					path = fmt.Sprintf("/spec/inputs/%d/contract", inputIndex)
				}
				report.addAt(index+1, "error", code, "spec.inputs", path)
			}
		}
	}
	if !report.Valid {
		return
	}
	number, code, witness, truncated := graphWitness(ctx, documents)
	if code != "" {
		path := "/spec/inputs"
		if len(witness) > 0 {
			path = witness[0].Path
		}
		report.appendLegacy(number, "error", code, "spec.inputs")
		report.addDetail(number, "error", code, path, witness, truncated)
	}
}
