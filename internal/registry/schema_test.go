package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/santhosh-tekuri/jsonschema/v6"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type offlineDiscoverySchemas struct{}

// Load denies every schema fetch beyond the two explicitly registered public documents.
func (offlineDiscoverySchemas) Load(string) (any, error) {
	return nil, errors.New("external schema loading is forbidden")
}

// TestDiscoveryPublishedSchemasValidateActualResponses checks real API output using an independent offline validator.
func TestDiscoveryPublishedSchemasValidateActualResponses(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		get: func(_ context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			schemaFixtureProduct().DeepCopyInto(discoveryProductObject(t, out))
			return nil
		},
		list: func(_ context.Context, out client.ObjectList, _ ...client.ListOption) error {
			discoveryProductList(t, out).Items = []datav1alpha1.DataProduct{
				*schemaFixtureProduct(),
			}
			return nil
		},
	}
	handler := discoveryHandler(reader, true)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineDiscoverySchemas{})
	compiler.AssertFormat()
	for _, name := range []string{"descriptor", "discovery"} {
		response := discoveryRequest(t, handler, "/api/v2/schema?type="+name)
		if response.Code != 200 ||
			response.Header().Get("Content-Type") != "application/schema+json" {
			t.Fatalf("schema unavailable: %d", response.Code)
		}
		var document map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource("urn:data-product-"+name+":v1", document); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ kind, target string }{{"descriptor", "/api/v2/products/products/customer-catalog"}, {"discovery", "/api/v2/products"}} {
		schema, err := compiler.Compile("urn:data-product-" + test.kind + ":v1")
		if err != nil {
			t.Fatal(err)
		}
		response := discoveryRequest(t, handler, test.target)
		var document map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(document); err != nil {
			t.Fatalf("%s emitted an invalid contract: %v", test.target, err)
		}
		for _, mutation := range []func(map[string]any){func(d map[string]any) { d["apiVersion"] = "unsupported/v9" }, func(d map[string]any) { d["privateResourceRef"] = "sentinel" }} {
			var invalid map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &invalid); err != nil {
				t.Fatal(err)
			}
			mutation(invalid)
			if err := schema.Validate(invalid); err == nil {
				t.Fatalf("%s schema accepted incompatible/private metadata", test.kind)
			}
		}
		if test.kind == "descriptor" {
			for _, mutate := range []func(map[string]any){
				func(d map[string]any) { d["name"] = "a..b" },
				func(d map[string]any) { d["name"] = "a.-b" },
				func(d map[string]any) { d["documentationUrl"] = "https://user:password@example.test/docs" },
				func(d map[string]any) { d["documentationUrl"] = "https://example.test/docs#fragment" },
				func(d map[string]any) { d["documentationUrl"] = "https://example.test/docs\\tail" },
				func(d map[string]any) { d["generation"] = float64(9007199254740992) },
			} {
				var invalid map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &invalid); err != nil {
					t.Fatal(err)
				}
				mutate(invalid)
				if err := schema.Validate(invalid); err == nil {
					t.Fatal("descriptor schema accepted invalid public identity, URL or generation")
				}
			}
			document["description"] = strings.Repeat("x", 16385)
			if err := schema.Validate(document); err == nil {
				t.Fatal("schema accepted oversized string")
			}
		}
	}
}

// schemaFixtureProduct includes all optional publication surfaces and a valid multi-label producer identity.
func schemaFixtureProduct() *datav1alpha1.DataProduct {
	product := registryProduct()
	product.Generation = 3
	product.Status.Conditions[0].ObservedGeneration = 3
	product.Spec.DocumentationURL = "https://example.test/docs"
	reference := datav1alpha1.ProductReference{
		Name:      strings.Repeat("a", 60) + ".upstream",
		Namespace: "products",
		Output:    "query",
	}
	product.Spec.Inputs = []datav1alpha1.InputPort{
		{
			Name:       "customer",
			ProductRef: reference,
			Contract: &datav1alpha1.InputContract{
				MinimumVersion: "v1.2.0",
				Protocol:       datav1alpha1.ProtocolOpenAPI,
			},
		},
	}
	product.Status.Conditions = append(
		product.Status.Conditions,
		metav1.Condition{Type: "CompositionReady", Status: "True", ObservedGeneration: 3},
	)
	product.Status.Inputs = []datav1alpha1.InputStatus{
		{
			Name:               "customer",
			ProductRef:         reference,
			ProductID:          "urn:example:customer",
			ObservedGeneration: 2,
			Version:            "v1.2.0",
			Owner: &datav1alpha1.ProductOwner{
				Name: "Customer team",
				URL:  "https://example.test/team",
			},
			Output: &datav1alpha1.OutputPort{
				Name:        "query",
				Protocol:    datav1alpha1.ProtocolOpenAPI,
				URL:         "https://example.test/query",
				ContractURL: "https://example.test/openapi.json",
				MediaType:   "application/json",
			},
			Ready:  true,
			Reason: "InputReady",
		},
	}
	product.Spec.UI.Contract = &datav1alpha1.UIContract{
		APIVersion:   "data-product-ui/v2",
		HostOrigins:  []datav1alpha1.UIHostOrigin{"https://registry.example.test"},
		Capabilities: []datav1alpha1.UICapability{"status", "resize", "appearance"},
	}
	return product
}

// TestDiscoveryHealthStates prevents absent, disabled, stale and unknown conditions from becoming ready.
func TestDiscoveryHealthStates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		condition *metav1.Condition
		state     string
	}{
		{"absent", nil, "unobserved"},
		{"current", &metav1.Condition{Status: "True", ObservedGeneration: 2}, "ready"},
		{"failed", &metav1.Condition{Status: "False", ObservedGeneration: 2}, "not-ready"},
		{"unknown", &metav1.Condition{Status: "Unknown", ObservedGeneration: 2}, "unobserved"},
		{"stale-disabled", &metav1.Condition{Status: "False", ObservedGeneration: 1, Reason: "ConnectorFeatureDisabled"}, "stale"},
		{"disabled", &metav1.Condition{Status: "False", ObservedGeneration: 2, Reason: "ConnectorFeatureDisabled"}, "disabled"},
		{"wrong-disabled-reason", &metav1.Condition{Status: "False", ObservedGeneration: 2, Reason: "ContractFeatureDisabled"}, "not-ready"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			product := registryProduct()
			product.Generation = 2
			product.Spec.Connector = &datav1alpha1.Connector{}
			if test.condition != nil {
				condition := *test.condition
				condition.Type = "ConnectorReady"
				condition.Message = "private-sentinel"
				product.Status.Conditions = append(product.Status.Conditions, condition)
			}
			descriptor := portableDescriptorFor(product)
			if got := descriptor.Health["connector"]; got.State != test.state ||
				strings.Contains(got.Message, "private-sentinel") {
				t.Fatalf("health=%+v, want %s", got, test.state)
			}
		})
	}
}

// TestDiscoveryResponseBudgetRejectsWholePage catches response limits applied only after partial publication.
func TestDiscoveryResponseBudgetRejectsWholePage(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		list: func(_ context.Context, out client.ObjectList, _ ...client.ListOption) error {
			product := registryProduct()
			product.Spec.Name = strings.Repeat("n", 12000)
			product.Spec.Description = strings.Repeat("d", 12000)
			product.Spec.Owner.Name = strings.Repeat("o", 12000)
			for range 100 {
				discoveryProductList(t, out).Items = append(
					discoveryProductList(t, out).Items,
					*product,
				)
			}
			return nil
		},
	}
	got := discoveryRequest(t, discoveryHandler(reader, true), "/api/v2/products?limit=100")
	if got.Code != http.StatusRequestEntityTooLarge ||
		strings.Contains(got.Body.String(), "customer-catalog") {
		t.Fatalf("oversized page was published: %d", got.Code)
	}
}

// TestDiscoveryCursorEncodingBudget includes JSON escaping in the public cursor bound.
func TestDiscoveryCursorEncodingBudget(t *testing.T) {
	t.Parallel()
	reader := discoveryReader{
		list: func(_ context.Context, out client.ObjectList, _ ...client.ListOption) error {
			discoveryProductList(t, out).Continue = strings.Repeat("\x00", 8192)
			return nil
		},
	}
	got := discoveryRequest(t, discoveryHandler(reader, true), "/api/v2/products")
	if got.Code != http.StatusBadGateway || strings.Contains(got.Body.String(), `"products"`) {
		t.Fatalf("encoded oversized cursor escaped: status=%d bytes=%d", got.Code, got.Body.Len())
	}
}

// TestDiscoveryRejectsInvalidPublicMetadata keeps every emitted descriptor inside the offline consumer contract.
func TestDiscoveryRejectsInvalidPublicMetadata(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*datav1alpha1.DataProduct)
	}{
		{"empty-label", func(p *datav1alpha1.DataProduct) { p.Name = "a..b" }},
		{"label-boundary", func(p *datav1alpha1.DataProduct) { p.Name = "a.-b" }},
		{"long-label", func(p *datav1alpha1.DataProduct) { p.Name = strings.Repeat("a", 64) }},
		{"credential-url", func(p *datav1alpha1.DataProduct) {
			invalid := url.URL{
				Scheme: "https", Host: "example.test", Path: "/data",
				User: url.UserPassword("user", "private-sentinel"),
			}
			p.Spec.Outputs[0].URL = invalid.String()
		}},
		{"fragment-url", func(p *datav1alpha1.DataProduct) {
			p.Spec.Outputs[0].ContractURL = "https://example.test/contract#fragment"
		}},
		{"backslash-url", func(p *datav1alpha1.DataProduct) { p.Spec.UI.URL = "https://example.test/path\\tail" }},
		{"unsafe-generation", func(p *datav1alpha1.DataProduct) { p.Generation = 9007199254740992 }},
		{"unsafe-observation", func(p *datav1alpha1.DataProduct) { p.Status.Conditions[0].ObservedGeneration = 9007199254740992 }},
		{"negative-observation", func(p *datav1alpha1.DataProduct) { p.Status.Conditions[0].ObservedGeneration = -1 }},
		{"missing-description", func(p *datav1alpha1.DataProduct) { p.Spec.Description = "" }},
		{"missing-outputs", func(p *datav1alpha1.DataProduct) { p.Spec.Outputs = nil }},
		{"unknown-protocol", func(p *datav1alpha1.DataProduct) { p.Spec.Outputs[0].Protocol = "Unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			product := registryProduct()
			test.mutate(product)
			reader := discoveryReader{
				list: func(_ context.Context, out client.ObjectList, _ ...client.ListOption) error {
					discoveryProductList(t, out).Items = []datav1alpha1.DataProduct{*product}
					return nil
				},
			}
			got := discoveryRequest(t, discoveryHandler(reader, true), "/api/v2/products")
			if got.Code != http.StatusUnprocessableEntity ||
				strings.Contains(got.Body.String(), "private-sentinel") ||
				strings.Contains(got.Body.String(), `"products"`) {
				t.Fatalf("invalid public metadata published: status=%d", got.Code)
			}
		})
	}
}
