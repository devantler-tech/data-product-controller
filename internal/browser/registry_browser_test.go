//go:build browser

package browser_test

import (
	"context"
	"net/http/httptest"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSandboxedProductCanQueryItsPublicAPI(t *testing.T) {
	productServer := httptest.NewTLSServer(demoproduct.NewHandler())
	t.Cleanup(productServer.Close)

	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register data-product API: %v", err)
	}
	product := &datav1alpha1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{Name: "harbour-observations", Namespace: "products"},
		Spec: datav1alpha1.DataProductSpec{
			ID:          productServer.URL + "/products/harbour",
			Name:        "Harbour observations",
			Description: "Queryable temperature and salinity observations.",
			Version:     "v1.0.0",
			Owner:       datav1alpha1.ProductOwner{Name: "data-platform-team"},
			Outputs: []datav1alpha1.OutputPort{{
				Name:        "observations",
				Protocol:    datav1alpha1.ProtocolOpenAPI,
				URL:         productServer.URL + "/api/observations",
				ContractURL: productServer.URL + "/openapi.json",
				MediaType:   "application/json",
			}},
			UI: &datav1alpha1.ProductUI{
				Title: "Explore harbour observations",
				URL:   productServer.URL + "/ui",
			},
		},
		Status: datav1alpha1.DataProductStatus{Conditions: []metav1.Condition{{
			Type:    datav1alpha1.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "DependenciesReady",
			Message: "All referenced data products and output ports are ready.",
		}}},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
	registryServer := httptest.NewTLSServer(
		registry.NewHandler(reader, func(context.Context) bool { return true }),
	)
	t.Cleanup(registryServer.Close)

	controlURL := launcher.New().
		Headless(true).
		NoSandbox(true).
		Set("ignore-certificate-errors").
		MustLaunch()
	browser := rod.New().ControlURL(controlURL).MustConnect()
	t.Cleanup(func() { _ = browser.Close() })

	page := browser.MustPage(registryServer.URL).MustWaitLoad()
	page.MustElement(".product-card").MustWaitVisible().MustClick()
	productFrame := page.MustElement("#product-surface").MustFrame().MustWaitLoad()

	statusElement := productFrame.MustElement("#status")
	statusElement.MustWait(`() => this.textContent === '2 observations'`)
	status := statusElement.MustText()
	if status != "2 observations" {
		t.Fatalf("embedded product status = %q, want %q", status, "2 observations")
	}
	if count := len(productFrame.MustElements(".observation")); count != 2 {
		t.Fatalf("embedded product observation count = %d, want 2", count)
	}
}
