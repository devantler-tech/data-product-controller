package dataspace_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
)

func firstObject(t *testing.T, v any) map[string]any {
	t.Helper()
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		t.Fatal("missing fixture array")
	}
	m, ok := a[0].(map[string]any)
	if !ok {
		t.Fatal("missing fixture object")
	}
	return m
}

func TestDistributionBudget(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1024, 1025} {
		source := object(t, example(t, "catalog"))
		bindings := object(t, example(t, "bindings"))
		dataset := firstObject(t, source["dcat:dataset"])
		bound := firstObject(t, bindings["datasets"])
		output := encode(t, firstObject(t, dataset["dcat:distribution"]))
		selection := encode(t, firstObject(t, bound["distributions"]))
		outputs, selections := []any{}, []any{}
		for i := 0; i < n; i++ {
			id := []byte(fmt.Sprintf("urn:example:observations-%d", i))
			outputs = append(
				outputs,
				object(t, bytes.ReplaceAll(output, []byte("urn:example:observations"), id)),
			)
			selected := bytes.ReplaceAll(selection, []byte("urn:example:observations"), id)
			selected = bytes.ReplaceAll(
				selected,
				[]byte("urn:example:dsp-observations"),
				[]byte(fmt.Sprintf("urn:example:dsp-observations-%d", i)),
			)
			selections = append(selections, object(t, selected))
		}
		dataset["dcat:distribution"] = outputs
		bound["distributions"] = selections
		b, err := dataspace.Export(
			bytes.NewReader(encode(t, source)),
			bytes.NewReader(encode(t, bindings)),
		)
		if n == 1024 && err != nil {
			t.Fatal(err)
		}
		if n == 1025 && (err == nil || len(b) > 0) {
			t.Fatal("exceeded distribution budget")
		}
	}
}

func TestOfferAndBindingByteLimits(t *testing.T) {
	t.Parallel()
	for _, n := range []int{16, 17} {
		bindings := object(t, example(t, "bindings"))
		dataset := firstObject(t, bindings["datasets"])
		template := encode(t, firstObject(t, dataset["offers"]))
		offers := []any{}
		for i := 0; i < n; i++ {
			offers = append(
				offers,
				object(
					t,
					bytes.ReplaceAll(
						template,
						[]byte("urn:example:harbour-offer"),
						[]byte(fmt.Sprintf("urn:example:offer-%d", i)),
					),
				),
			)
		}
		dataset["offers"] = offers
		b, err := dataspace.Export(
			bytes.NewReader(example(t, "catalog")),
			bytes.NewReader(encode(t, bindings)),
		)
		if n == 16 && err != nil {
			t.Fatal(err)
		}
		if n == 17 && (err == nil || len(b) > 0) {
			t.Fatal("exceeded offer budget")
		}
	}
	b, err := dataspace.Export(
		bytes.NewReader(example(t, "catalog")),
		strings.NewReader(strings.Repeat(" ", (1<<20)+1)),
	)
	if err == nil || len(b) > 0 || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("binding byte budget failed: %v", err)
	}
}
