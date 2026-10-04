package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/controller"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	"github.com/santhosh-tekuri/jsonschema/v6"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type traceResult struct {
	APIVersion string   `json:"apiVersion"`
	Root       string   `json:"root"`
	Complete   bool     `json:"complete"`
	Issues     []string `json:"issues"`
	Nodes      []struct {
		Key                string `json:"key"`
		State              string `json:"state"`
		Version            string `json:"version"`
		Generation         int64  `json:"generation"`
		ObservedGeneration int64  `json:"observedGeneration"`
	} `json:"nodes"`
	Edges []struct {
		From          string `json:"from"`
		To            string `json:"to"`
		Input         string `json:"input"`
		State         string `json:"state"`
		Compatibility string `json:"compatibility"`
	} `json:"edges"`
}

func traceProduct(name string, targets ...string) *datav1alpha1.DataProduct {
	p := registryProduct()
	p.Name = name
	p.Generation = 2
	p.Status.Conditions[0].ObservedGeneration = 2
	p.Spec.Version = "v1.2.0"
	for i, target := range targets {
		p.Spec.Inputs = append(p.Spec.Inputs, datav1alpha1.InputPort{
			Name:       fmt.Sprintf("input-%d", i),
			ProductRef: datav1alpha1.ProductReference{Name: target, Output: "query"},
			Contract: &datav1alpha1.InputContract{
				Protocol:       datav1alpha1.ProtocolOpenAPI,
				MinimumVersion: "v1.0.0",
			},
		})
	}
	return p
}

func traceReader(
	t *testing.T,
	products ...*datav1alpha1.DataProduct,
) (discoveryReader, map[string]int) {
	t.Helper()
	index := map[string]*datav1alpha1.DataProduct{}
	calls := map[string]int{}
	for _, p := range products {
		index[p.Namespace+"/"+p.Name] = p
	}
	return discoveryReader{
		get: func(ctx context.Context, key client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
				t.Error("trace read has no shared deadline")
			}
			calls[key.String()]++
			if p := index[key.String()]; p != nil {
				*discoveryProductObject(t, out) = *p.DeepCopy()
				return nil
			}
			return apierrors.NewNotFound(
				schema.GroupResource{Group: "data.devantler.tech", Resource: "dataproducts"},
				key.Name,
			)
		},
	}, calls
}

func traceHandler(reader client.Reader) http.Handler {
	return NewHandlerWithOptions(
		reader,
		HandlerOptions{
			DiscoveryEnabled:   func(context.Context) bool { return true },
			LineageEnabled:     func(context.Context) bool { return true },
			InputCompatibility: controller.DeclaredInputCompatibility,
		},
	)
}

func readTrace(t *testing.T, h http.Handler) (traceResult, string) {
	t.Helper()
	r := discoveryRequest(t, h, "/api/v2/products/products/root/lineage")
	if r.Code != http.StatusOK {
		t.Fatalf("trace status=%d body=%s", r.Code, r.Body.String())
	}
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("trace can be cached")
	}
	var result traceResult
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result, r.Body.String()
}

// Removing the transitive walk or caching only successful reads breaks the returned graph.
func TestLineageTransitiveDiamond(t *testing.T) {
	reader, calls := traceReader(
		t,
		traceProduct("root", "left", "right"),
		traceProduct("left", "leaf"),
		traceProduct("right", "leaf"),
		traceProduct("leaf"),
	)
	result, _ := readTrace(t, traceHandler(reader))
	if result.APIVersion != "data-product-lineage/v1" || result.Root != "products/root" ||
		!result.Complete ||
		len(result.Nodes) != 4 ||
		len(result.Edges) != 4 {
		t.Fatalf("trace=%+v", result)
	}
	for key, count := range calls {
		if count != 1 {
			t.Fatalf("read %s %d times", key, count)
		}
	}
	if result.Nodes[0].Key != "products/leaf" || result.Nodes[3].Key != "products/root" {
		t.Fatal("node order unstable")
	}
	for _, e := range result.Edges {
		if e.Compatibility != "compatible" || e.State != "resolved" {
			t.Fatalf("edge=%+v", e)
		}
	}
}

func TestLineagePartialBranchesAndPrivacy(t *testing.T) {
	root := traceProduct("root", "missing", "missing", "bad", "unavailable", "foreign", "healthy")
	root.Spec.Inputs[4].ProductRef.Namespace = "foreign"
	bad := traceProduct("bad")
	bad.Spec.Owner.URL = (&url.URL{
		Scheme: "https", Host: "example.test", User: url.UserPassword("credential", "sentinel"),
	}).String()
	healthy := traceProduct("healthy")
	healthy.Status.Conditions[0].Message = "private-provider-diagnostic"
	root.Status.Inputs = []datav1alpha1.InputStatus{
		{
			Name: "input-0",
			ProductRef: datav1alpha1.ProductReference{
				Namespace: "private",
				Name:      "status-redirect",
				Output:    "query",
			},
		},
	}
	reader, calls := traceReader(t, root, bad, healthy)
	original := reader.get
	reader.get = func(ctx context.Context, key client.ObjectKey, out client.Object, opts ...client.GetOption) error {
		if key.Namespace != "products" {
			t.Fatal("cross-namespace read")
		}
		if key.Name == "unavailable" {
			calls[key.String()]++
			return errors.New("private-backend-sentinel")
		}
		return original(ctx, key, out, opts...)
	}
	result, body := readTrace(t, traceHandler(reader))
	if result.Complete || len(result.Edges) != 6 || calls["products/missing"] != 1 ||
		calls["products/healthy"] != 1 {
		t.Fatalf("trace=%+v reads=%v", result, calls)
	}
	for _, secret := range []string{"credential:sentinel", "private-provider-diagnostic", "private-backend-sentinel", "status-redirect", "resourceRef", "secretRef"} {
		if strings.Contains(body, secret) {
			t.Fatalf("trace leaked %q", secret)
		}
	}
	states := map[string]string{}
	for _, e := range result.Edges {
		states[e.Input] = e.State
	}
	for input, want := range map[string]string{"input-0": "missing", "input-2": "invalid", "input-3": "unavailable", "input-4": "cross-namespace", "input-5": "resolved"} {
		if states[input] != want {
			t.Fatalf("%s=%s want=%s", input, states[input], want)
		}
	}
}

func TestLineageCompatibilityAndReadiness(t *testing.T) {
	for _, tc := range []struct{ name, version, protocol, want string }{
		{"upgrade", "v1.3.0", "OpenAPI", "compatible"},
		{"breaking", "v2.0.0", "OpenAPI", "contract-incompatible"},
		{"older", "v1.0.0", "OpenAPI", "contract-incompatible"},
		{"protocol", "v1.2.0", "GraphQL", "contract-incompatible"},
		{"zero-exact", "v0.2.0", "OpenAPI", "compatible"},
		{"zero-upgrade", "v0.2.1", "OpenAPI", "contract-incompatible"},
		{"prerelease", "v1.2.0-beta", "OpenAPI", "contract-incompatible"},
		{"output", "v1.2.0", "OpenAPI", "output-missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := traceProduct("root", "producer")
			root.Spec.Inputs[0].Contract.MinimumVersion = "v1.2.0"
			if strings.HasPrefix(tc.name, "zero") {
				root.Spec.Inputs[0].Contract.MinimumVersion = "v0.2.0"
			}
			p := traceProduct("producer")
			p.Spec.Version = tc.version
			p.Spec.Outputs[0].Protocol = datav1alpha1.OutputProtocol(tc.protocol)
			if tc.name == "output" {
				p.Spec.Outputs[0].Name = "another"
			}
			p.Status.Conditions[0].ObservedGeneration = 1
			reader, _ := traceReader(t, root, p)
			result, _ := readTrace(t, traceHandler(reader))
			if !result.Complete || result.Edges[0].Compatibility != tc.want {
				t.Fatalf("trace=%+v", result)
			}
			if result.Nodes[0].State != "stale" || result.Nodes[0].ObservedGeneration != 1 {
				t.Fatalf("stale health lost: %+v", result)
			}
		})
	}
	p := traceProduct("producer", "leaf")
	now := metav1.Now()
	p.DeletionTimestamp = &now
	reader, calls := traceReader(t, traceProduct("root", "producer"), p, traceProduct("leaf"))
	result, _ := readTrace(t, traceHandler(reader))
	if calls["products/leaf"] != 1 || result.Nodes[1].State != "deleting" {
		t.Fatalf("deleting branch=%+v reads=%v", result, calls)
	}
}

func TestLineageCyclesAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		products []*datav1alpha1.DataProduct
		issue    string
	}{
		{"cycle", []*datav1alpha1.DataProduct{traceProduct("root", "second"), traceProduct("second", "root")}, "cycle"},
		{"reads", []*datav1alpha1.DataProduct{traceProduct("root")}, "product-limit"},
		{"depth", []*datav1alpha1.DataProduct{traceProduct("root", "p-0")}, "depth-limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "reads" {
				for i := 0; i < 400; i++ {
					tc.products[0].Spec.Inputs = append(
						tc.products[0].Spec.Inputs,
						datav1alpha1.InputPort{
							Name: fmt.Sprintf("i-%d", i),
							ProductRef: datav1alpha1.ProductReference{
								Name:   fmt.Sprintf("missing-%d", i),
								Output: "query",
							},
						},
					)
				}
			}
			if tc.name == "depth" {
				for i := 0; i < 70; i++ {
					tc.products = append(
						tc.products,
						traceProduct(fmt.Sprintf("p-%d", i), fmt.Sprintf("p-%d", i+1)),
					)
				}
			}
			reader, calls := traceReader(t, tc.products...)
			result, _ := readTrace(t, traceHandler(reader))
			if result.Complete || !containsString(result.Issues, tc.issue) {
				t.Fatalf("trace=%+v", result)
			}
			if len(calls) > 256 || len(result.Nodes) > 256 || len(result.Edges) > 1024 {
				t.Fatal("unbounded trace")
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestLineageRootIdentityAndQueries(t *testing.T) {
	reader, calls := traceReader(t, traceProduct("root"))
	h := traceHandler(reader)
	for _, path := range []string{"/api/v2/products/products/root/lineage?extra=1", "/api/v2/products/Bad/root/lineage"} {
		if r := discoveryRequest(t, h, path); r.Code != http.StatusBadRequest {
			t.Fatalf("invalid path status=%d", r.Code)
		}
	}
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/products/products/root/lineage",
		strings.NewReader("{}"),
	)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || len(calls) != 0 {
		t.Fatal("invalid trace request read products")
	}
	wrong := discoveryReader{
		get: func(_ context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			*discoveryProductObject(t, out) = *traceProduct("wrong")
			return nil
		},
	}
	r = discoveryRequest(t, traceHandler(wrong), "/api/v2/products/products/root/lineage")
	if r.Code != http.StatusBadGateway || strings.Contains(r.Body.String(), "wrong") {
		t.Fatal("wrong root identity accepted")
	}
}

func TestLineageFeatureStates(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		enabled, discovery, evaluator bool
		want                          int
	}{
		{"omitted", false, true, true, 404},
		{"enabled", true, true, true, 200},
		{"discovery-off", true, false, true, 404},
		{"evaluator-missing", true, true, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flagClient, err := featureflag.NewClient(
				"lineage-test-"+tc.name,
				featureflag.NewProvider(map[string]bool{"registry-lineage": tc.enabled}),
			)
			if err != nil {
				t.Fatal(err)
			}
			reader, calls := traceReader(t, traceProduct("root"))
			options := HandlerOptions{
				DiscoveryEnabled: func(context.Context) bool { return tc.discovery },
				LineageEnabled:   func(ctx context.Context) bool { return featureflag.Enabled(ctx, flagClient, "registry-lineage") },
			}
			if tc.evaluator {
				options.InputCompatibility = controller.DeclaredInputCompatibility
			}
			handler := NewHandlerWithOptions(reader, options)
			response := discoveryRequest(t, handler, "/api/v2/products/products/root/lineage")
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d", response.Code, tc.want)
			}
			schemaResponse := discoveryRequest(t, handler, "/api/v2/schema?type=lineage")
			if schemaResponse.Code != tc.want {
				t.Fatalf("schema status=%d", schemaResponse.Code)
			}
			if tc.want == 404 && len(calls) != 0 {
				t.Fatal("disabled trace read products")
			}
			var config map[string]bool
			if err := json.Unmarshal(
				discoveryRequest(t, handler, "/api/v1/ui-config").Body.Bytes(),
				&config,
			); err != nil {
				t.Fatal(err)
			}
			if config["lineageEnabled"] != (tc.want == 200) {
				t.Fatal("advertised capability differs from gate")
			}
		})
	}
}

func TestLineagePublishedSchema(t *testing.T) {
	reader, _ := traceReader(
		t,
		traceProduct("root", "producer", "missing"),
		traceProduct("producer"),
	)
	handler := traceHandler(reader)
	response := discoveryRequest(t, handler, "/api/v2/schema?type=lineage")
	if response.Code != 200 {
		t.Fatal("trace schema unavailable")
	}
	var schemaDocument any
	if err := json.Unmarshal(response.Body.Bytes(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineDiscoverySchemas{})
	if err := compiler.AddResource("urn:trace:v1", schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("urn:trace:v1")
	if err != nil {
		t.Fatal(err)
	}
	_, body := readTrace(t, handler)
	var document map[string]any
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("actual trace violates schema: %v", err)
	}
	document["credentials"] = "sentinel"
	if schema.Validate(document) == nil {
		t.Fatal("schema permits private fields")
	}
}

func TestLineageDeadlineAndConcurrentAdmission(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	reader := discoveryReader{
		get: func(ctx context.Context, _ client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			*discoveryProductObject(t, out) = *traceProduct("root")
			return nil
		},
	}
	handler := traceHandler(reader)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/api/v2/products/products/root/lineage",
				nil,
			),
		)
		done <- response
	}()
	<-entered
	second := discoveryRequest(t, handler, "/api/v2/products/products/root/lineage")
	if second.Code != 429 {
		t.Fatalf("concurrent trace status=%d", second.Code)
	}
	close(release)
	if first := <-done; first.Code != 200 {
		t.Fatalf("first trace status=%d", first.Code)
	}
	if third := discoveryRequest(
		t,
		handler,
		"/api/v2/products/products/root/lineage",
	); third.Code != 200 {
		t.Fatal("trace slot not released")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	expired := discoveryReader{
		get: func(ctx context.Context, key client.ObjectKey, out client.Object, _ ...client.GetOption) error {
			if key.Name == "root" {
				*discoveryProductObject(t, out) = *traceProduct("root", "blocked")
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	response := httptest.NewRecorder()
	traceHandler(
		expired,
	).ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v2/products/products/root/lineage", nil))
	var trace traceResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &trace) != nil ||
		trace.Complete ||
		!containsString(trace.Issues, "timeout") {
		t.Fatalf("expired trace=%d %s", response.Code, response.Body.String())
	}
}

func TestLineageGlobalEdgeAndMetadataBounds(t *testing.T) {
	root := traceProduct("root")
	products := []*datav1alpha1.DataProduct{root}
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("p-%d", i)
		root.Spec.Inputs = append(
			root.Spec.Inputs,
			datav1alpha1.InputPort{
				Name:       name,
				ProductRef: datav1alpha1.ProductReference{Name: name, Output: "query"},
			},
		)
		p := traceProduct(name)
		for j := 0; j < 40; j++ {
			p.Spec.Inputs = append(
				p.Spec.Inputs,
				datav1alpha1.InputPort{
					Name: fmt.Sprintf("i-%d", j),
					ProductRef: datav1alpha1.ProductReference{
						Name:   fmt.Sprintf("p-%d", j),
						Output: "query",
					},
				},
			)
		}
		products = append(products, p)
	}
	reader, _ := traceReader(t, products...)
	result, _ := readTrace(t, traceHandler(reader))
	if result.Complete || len(result.Edges) != 1024 ||
		!containsString(result.Issues, "edge-limit") {
		t.Fatalf(
			"unbounded edges=%d complete=%v issues=%v",
			len(result.Edges),
			result.Complete,
			result.Issues,
		)
	}
	products = []*datav1alpha1.DataProduct{traceProduct("root")}
	root = products[0]
	for i := 0; i < 100; i++ {
		p := traceProduct(fmt.Sprintf("p-%d", i))
		p.Spec.Description = strings.Repeat("x", 16<<10)
		root.Spec.Inputs = append(
			root.Spec.Inputs,
			datav1alpha1.InputPort{
				Name:       p.Name,
				ProductRef: datav1alpha1.ProductReference{Name: p.Name, Output: "query"},
			},
		)
		products = append(products, p)
	}
	reader, _ = traceReader(t, products...)
	result, _ = readTrace(t, traceHandler(reader))
	if result.Complete || !containsString(result.Issues, "metadata-limit") {
		t.Fatal("retained metadata limit not enforced")
	}
}
