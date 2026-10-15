// Command producer creates the local EDC fixture through the real DCAT handler.
// Its Kubernetes reader is in-memory; only hosted acceptance proves a live controller snapshot.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	datav1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// main publishes the synthetic snapshot or exits without claiming a usable catalog.
func main() {
	if err := snapshot(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// snapshot consumes only the committed synthetic products, never a kubeconfig or live client.
func snapshot() error {
	input, err := os.Open("tests/dataspace/edc/products.yaml")
	if err != nil {
		return fmt.Errorf("fixture unavailable: %w", err)
	}
	defer func() { _ = input.Close() }()
	decoder := yaml.NewYAMLOrJSONDecoder(input, 4096)
	products := make([]client.Object, 0, 2)
	for range 2 {
		var product datav1.DataProduct
		if err := decoder.Decode(&product); err != nil {
			return fmt.Errorf("fixture invalid: %w", err)
		}
		products = append(products, &product)
	}
	var extra datav1.DataProduct
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("fixture requires exactly two products")
	}
	scheme := runtime.NewScheme()
	if err := datav1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("fixture scheme invalid: %w", err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(products...).Build()
	handler, err := catalog.NewHandler(reader, catalog.Options{
		ID: "urn:example:controller-catalog", Enabled: func(context.Context) bool { return true },
	})
	if err != nil {
		return fmt.Errorf("catalog unavailable: %w", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/v1/catalog", nil,
	))
	if response.Code != http.StatusOK {
		return fmt.Errorf("catalog rejected the fixture: status %d", response.Code)
	}
	if _, err := os.Stdout.Write(response.Body.Bytes()); err != nil {
		return fmt.Errorf("fixture output failed: %w", err)
	}
	return nil
}
