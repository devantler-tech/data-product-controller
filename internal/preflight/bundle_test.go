package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
)

// selected evaluates explicit in-memory sources as a v2 bundle for regression fixtures.
func selected(t *testing.T, sources ...string) BundleReport {
	t.Helper()
	readers := make([]io.Reader, 0, len(sources))
	for _, source := range sources {
		readers = append(readers, strings.NewReader(source))
	}
	return CheckBundle(context.Background(), readers, "")
}

// richCode locates an expected public finding and fails when validation omits it.
func richCode(t *testing.T, r BundleReport, code string) BundleDiagnostic {
	t.Helper()
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("missing %s: %+v", code, r)
	return BundleDiagnostic{}
}

// dependent creates a fixture with one declared input referencing the named local producer.
func dependent(name, producer string) data.DataProduct {
	p := product(name)
	p.Spec.Inputs = []data.InputPort{
		{
			Name:       "upstream",
			ProductRef: data.ProductReference{Name: producer, Output: "observations"},
		},
	}
	return p
}

// TestBundlePhysicalProvenanceAndIndexedDependency preserves empty-document offsets and input indexes.
func TestBundlePhysicalProvenanceAndIndexedDependency(t *testing.T) {
	p := dependent("consumer", "missing")
	r := selected(t, bundle(t, product("first")), "null\n---\n# empty\n---\n"+bundle(t, p))
	d := richCode(t, r, "ProducerUnresolved")
	if d.Source != 2 || d.Document != 3 || d.Line < 5 || d.Column < 1 ||
		d.Path != "/spec/inputs/0/productRef" {
		t.Fatalf("finding cannot locate selected input: %+v", d)
	}
	if r.Products != 2 || r.Sources[1].Documents != 3 || r.Plan != nil || len(r.Descriptors) != 0 {
		t.Fatalf("incomplete selection exposed a plan: %+v", r)
	}
	legacy := Check(context.Background(), strings.NewReader("null\n---\n"+bundle(t, p)), "")
	if len(legacy.Diagnostics) != 1 || legacy.Diagnostics[0].Document != 1 ||
		legacy.Diagnostics[0].Path != "spec.inputs" {
		t.Fatalf("v1 diagnostic contract changed: %+v", legacy.Diagnostics)
	}
}

// TestBundlePreciseAdmissionAndPrivateMapKeys requires useful coordinates without private submitted keys.
func TestBundlePreciseAdmissionAndPrivateMapKeys(t *testing.T) {
	p := product("invalid")
	p.Spec.Owner.Name = ""
	r := selected(t, bundle(t, p))
	d := richCode(t, r, "InvalidField")
	if d.Path != "/spec/owner/name" || d.Source != 1 || d.Document != 1 || d.Column == 0 {
		t.Fatalf("coarse admission finding: %+v", d)
	}
	p = product("labels")
	p.Labels = map[string]string{"PRIVATE_BAD_KEY!": "PRIVATE_VALUE"}
	r = selected(t, bundle(t, p))
	encoded, _ := json.Marshal(r)
	if r.Valid || strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatalf("private rejected label escaped: %s", encoded)
	}
}

// TestBundleCycleWitnessAndStablePlan checks actionable cycles and deterministic producer-first plans.
func TestBundleCycleWitnessAndStablePlan(t *testing.T) {
	a, b := dependent("a", "b"), dependent("b", "a")
	r := selected(t, bundle(t, b), bundle(t, a))
	d := richCode(t, r, "CompositionCycle")
	if len(d.Witness) != 2 || d.WitnessTruncated {
		t.Fatalf("cycle has no bounded witness: %+v", d)
	}
	for _, step := range d.Witness {
		if step.Source < 1 || step.Document != 1 || step.Path != "/spec/inputs/0/productRef" {
			t.Fatalf("unsafe witness: %+v", step)
		}
	}
	p, c := product("z-producer"), dependent("a-consumer", "z-producer")
	c.Spec.Inputs = append(
		c.Spec.Inputs,
		data.InputPort{
			Name:       "second",
			ProductRef: data.ProductReference{Name: p.Name, Output: "observations"},
		},
	)
	one, two := selected(t, bundle(t, c), bundle(t, p)), selected(t, bundle(t, p, c))
	if !one.Valid || !one.Complete || one.Plan == nil || two.Plan == nil {
		t.Fatalf("valid ports rejected: %+v", one)
	}
	if len(one.Plan.Edges) != 2 || len(one.Plan.Order) != 2 ||
		one.Plan.Order[0].Key != "products/z-producer" {
		t.Fatalf("plan duplicates producer prerequisites: %+v", one.Plan)
	}
	keys := func(r BundleReport) []string {
		var result []string
		for _, p := range r.Plan.Order {
			result = append(result, p.Key)
		}
		return result
	}
	if !reflect.DeepEqual(keys(one), keys(two)) {
		t.Fatal("selection permutation changed semantic order")
	}
}

// TestBundlePerProductFeaturesAndTruncation preserves per-product requirements and omitted finding counts.
func TestBundlePerProductFeaturesAndTruncation(t *testing.T) {
	p, c := product("producer"), dependent("consumer", "producer")
	r := selected(t, bundle(t, c, p))
	if len(r.ProductFeatures) != 2 ||
		!reflect.DeepEqual(r.ProductFeatures[0].RequiredFeatures, []string{"composition"}) ||
		len(r.ProductFeatures[1].RequiredFeatures) != 0 {
		t.Fatalf("product feature origin absent: %+v", r.ProductFeatures)
	}
	p = product("unresolved")
	for index := 0; index < 140; index++ {
		input := data.InputPort{
			Name:       fmt.Sprintf("input-%d", index),
			ProductRef: data.ProductReference{Name: "missing", Output: "observations"},
		}
		p.Spec.Inputs = append(p.Spec.Inputs, input)
	}
	r = selected(t, bundle(t, p))
	if r.DiagnosticCounts.Total != 140 || r.DiagnosticCounts.Warnings != 140 ||
		r.DiagnosticCounts.Omitted != 12 ||
		len(r.Diagnostics) != 128 ||
		r.Complete ||
		!r.Valid {
		t.Fatalf("findings were silently dropped: %+v", r.DiagnosticCounts)
	}
}

// TestBundleAggregateIngressAndFileBoundaries enforces one input budget without joining file syntax.
func TestBundleAggregateIngressAndFileBoundaries(t *testing.T) {
	r := selected(t, strings.Repeat(" ", maxInputBytes/2+1), strings.Repeat(" ", maxInputBytes/2+1))
	if finding := richCode(
		t,
		r,
		"InputLimit",
	); finding.Message != "The selected inputs exceed 2 MiB in total." {
		t.Fatalf("aggregate limit wording: %s", finding.Message)
	}
	if r.Valid || r.Plan != nil {
		t.Fatal("per-file budgets widened aggregate ingress")
	}
	r = selected(t, "apiVersion: [", bundle(t, product("valid")))
	richCode(t, r, "InvalidDocument")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := CheckBundle(
		ctx,
		[]io.Reader{strings.NewReader(bundle(t, product("valid")))},
		"",
	); r.Valid ||
		r.Complete {
		t.Fatal("canceled selection succeeded")
	}
}

// TestPublicLegacyReportDoesNotRetainPrivateDeclarations rejects private evaluation state in public v1 results.
func TestPublicLegacyReportDoesNotRetainPrivateDeclarations(t *testing.T) {
	p := product("public")
	p.Annotations = map[string]string{"PRIVATE_FIELD": "PRIVATE_VALUE"}
	r := Check(context.Background(), strings.NewReader(bundle(t, p)), "")
	if output := fmt.Sprintf("%+v", r); strings.Contains(output, "PRIVATE_") {
		t.Fatal("ordinary report logging retains the private declaration")
	}
}

// TestBundleOmittedErrorStillInvalidatesSelection prevents diagnostic truncation from hiding invalidity.
func TestBundleOmittedErrorStillInvalidatesSelection(t *testing.T) {
	p := product("unresolved")
	for index := range 128 {
		p.Spec.Inputs = append(p.Spec.Inputs, data.InputPort{
			Name:       fmt.Sprintf("input-%d", index),
			ProductRef: data.ProductReference{Name: "missing", Output: "observations"},
		})
	}
	p.Spec.Inputs = append(p.Spec.Inputs, data.InputPort{
		Name: "forbidden",
		ProductRef: data.ProductReference{
			Namespace: "foreign",
			Name:      "missing",
			Output:    "observations",
		},
	})
	r := selected(t, bundle(t, p))
	if r.Valid || r.Complete || r.Plan != nil || len(r.Descriptors) != 0 ||
		r.DiagnosticCounts.Errors != 1 || r.DiagnosticCounts.Warnings != 128 ||
		r.DiagnosticCounts.Omitted != 1 || len(r.Diagnostics) != 128 {
		t.Fatalf("an omitted error granted success or a partial plan: %+v", r)
	}
	if err := reportSchema(t).Validate(reportValue(t, r)); err != nil {
		t.Fatalf("truncated report violates its schema: %v", err)
	}
}

// TestBundleDepthWitnessMatchesProductLevelBound checks the accepted depth and the first rejected level.
func TestBundleDepthWitnessMatchesProductLevelBound(t *testing.T) {
	for _, count := range []int{64, 65} {
		var chain []data.DataProduct
		for index := range count {
			p := product(fmt.Sprintf("depth-%03d", index))
			if index > 0 {
				p = dependent(p.Name, chain[index-1].Name)
			}
			chain = append(chain, p)
		}
		r := selected(t, bundle(t, chain...))
		if count == 64 {
			if !r.Valid || !r.Complete || r.Plan == nil || len(r.Plan.Order) != 64 {
				t.Fatal("supported 64-product review plan rejected")
			}
			continue
		}
		d := richCode(t, r, "CompositionLimit")
		if r.Valid || r.Complete || r.Plan != nil || len(d.Witness) != 64 || d.WitnessTruncated {
			t.Fatalf("depth decision lost its exact bounded witness: %+v", d)
		}
		for _, step := range d.Witness {
			if step.Source != 1 || step.Document < 2 || step.Document > 65 ||
				step.Path != "/spec/inputs/0/productRef" {
				t.Fatalf("invalid depth provenance: %+v", step)
			}
		}
	}
}

// TestInterruptedBundleRetainsObservedFeatureRequirements derives the union from retained observations.
func TestInterruptedBundleRetainsObservedFeatureRequirements(t *testing.T) {
	// Cancellation may interrupt the product loop after a checked consumer but before
	// the legacy engine finalizes its aggregate feature list.
	r := Report{
		Valid:            true,
		Complete:         true,
		RequiredFeatures: []string{},
		sources:          []SourceSummary{{Source: 1, Documents: 2, Products: 2}},
		productFeatures: []ProductFeatures{
			{Source: 1, Document: 1, RequiredFeatures: []string{"composition"}},
		},
	}
	r.add(0, "ValidationLimit", "")
	result := bundleReport(r)
	if result.Valid || result.Complete || result.Plan != nil ||
		!reflect.DeepEqual(result.RequiredFeatures, []string{"composition"}) {
		t.Fatalf("interruption lost the checked product's requirements: %+v", result)
	}
}
