//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/controller"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestThreeProductComposition exercises the documented example through the controller, public API and browser.
func TestThreeProductComposition(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open("../../docs/examples/composition.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)
	products := make([]client.Object, 3)
	for i := range products {
		product := &datav1alpha1.DataProduct{}
		if err := decoder.Decode(product); err != nil {
			t.Fatal(err)
		}
		products[i] = product
	}
	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(products...).
		Build()
	r := &controller.DataProductReconciler{
		Client:             reader,
		CompositionEnabled: func(context.Context) bool { return true },
	}
	for _, product := range products {
		if _, err := r.Reconcile(
			t.Context(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(product)},
		); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(
		registry.NewHandler(reader, func(context.Context) bool { return true }),
	)
	t.Cleanup(server.Close)
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		server.URL+"/api/v1/products",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var collection struct {
		Products []struct {
			Name    string
			Ready   bool
			Lineage []datav1alpha1.InputStatus
		}
	}
	if err := json.NewDecoder(response.Body).Decode(&collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Products) != 3 || collection.Products[0].Name != "coastal-summary" ||
		!collection.Products[0].Ready ||
		len(collection.Products[0].Lineage) != 2 {
		t.Fatalf("composed API descriptor = %+v", collection)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	controlURL, err := launcher.New().Context(ctx).Headless(true).NoSandbox(true).Launch()
	if err != nil {
		t.Fatal(err)
	}
	browser := rod.New().
		ControlURL(controlURL).
		WithPanic(func(value interface{}) { t.Fatalf("browser: %v", value) }).
		MustConnect()
	t.Cleanup(func() { _ = browser.Close() })
	page := browser.MustPage().Timeout(30 * time.Second).MustNavigate(server.URL).MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible().MustClick()
	if !page.MustEval(`() => !!document.querySelector("#product-lineage")`).Bool() {
		t.Fatal("registry has no composition detail")
	}
	lineage := page.MustElement("#product-lineage").MustText()
	for _, want := range []string{"products/harbour", "v1.2.0", "Harbour team", "products/weather", "Weather team", "InputReady"} {
		if !strings.Contains(lineage, want) {
			t.Fatalf("lineage missing %q: %s", want, lineage)
		}
	}
	producer := &datav1alpha1.DataProduct{}
	key := client.ObjectKey{Namespace: "products", Name: "harbour"}
	if err := reader.Get(t.Context(), key, producer); err != nil {
		t.Fatal(err)
	}
	producer.Spec.Version = "v2.0.0"
	producer.Spec.Owner.Name = "<script>window.pwned=true</script>"
	if err := reader.Update(t.Context(), producer); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(
		t.Context(),
		ctrl.Request{
			NamespacedName: client.ObjectKey{Namespace: "products", Name: "coastal-summary"},
		},
	); err != nil {
		t.Fatal(err)
	}
	page.MustReload().MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible().MustClick()
	if !strings.Contains(page.MustElement("#product-lineage").MustText(), "ContractIncompatible") {
		t.Fatal("breaking upgrade hidden from registry user")
	}
	if page.MustEval(`() => window.pwned === true`).Bool() {
		t.Fatal("lineage executed untrusted metadata")
	}
	if source := page.MustElement("#product-surface").MustAttribute("src"); source != nil {
		t.Fatal("unready composed product opened a surface")
	}
}
