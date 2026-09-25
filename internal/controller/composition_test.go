package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestCompositionReadFailureClearsReadiness catches stale success and public leakage after API failures.
func TestCompositionReadFailureClearsReadiness(t *testing.T) {
	t.Parallel()
	p, consumer := testProduct("p"), testProduct("consumer")
	p.Spec.Version = "v1.0.0"
	markCompositionProducerReady(p)
	composeInput(t, consumer, p.Name, "v1.0.0")
	r := compositionReconciler(t, p, consumer)
	reconcileComposition(t, r, consumer)
	store, ok := r.Client.(client.WithWatch)
	if !ok {
		t.Fatal("test client must support watches")
	}
	failure := errors.New("private API error sentinel")
	r.Client = interceptor.NewClient(store, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
			if key.Name == "p" {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("dependency read lacks the shared deadline")
				}
				return failure
			}
			return c.Get(ctx, key, object, options...)
		},
	})
	_, err := r.Reconcile(
		t.Context(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(consumer)},
	)
	if !errors.Is(err, failure) {
		t.Fatalf("retry error = %v", err)
	}
	got := &datav1alpha1.DataProduct{}
	if err := store.Get(t.Context(), client.ObjectKeyFromObject(consumer), got); err != nil {
		t.Fatal(err)
	}
	condition := readyCondition(t, got)
	if condition.Status != metav1.ConditionFalse || condition.Reason != "DependencyUnavailable" ||
		strings.Contains(condition.Message, "sentinel") {
		t.Fatalf("API failure status = %+v", got.Status)
	}
	if len(got.Status.Inputs) != 1 || got.Status.Inputs[0].Ready ||
		got.Status.Inputs[0].Output != nil {
		t.Fatal("failed read retained old verified lineage")
	}
}

// TestCompositionDiamondReadsEachProducerOnce catches repeated graph reads and false cycles at shared ancestors.
func TestCompositionDiamondReadsEachProducerOnce(t *testing.T) {
	t.Parallel()
	a, b, c, d := testProduct("a"), testProduct("b"), testProduct("c"), testProduct("d")
	for _, p := range []*datav1alpha1.DataProduct{b, c, d} {
		p.Spec.Version = "v1.0.0"
		markCompositionProducerReady(p)
	}
	composeInput(t, a, b.Name, "v1.0.0")
	composeInput(t, a, c.Name, "v1.0.0")
	composeInput(t, b, d.Name, "v1.0.0")
	composeInput(t, c, d.Name, "v1.0.0")
	r := compositionReconciler(t, a, b, c, d)
	reads := map[string]int{}
	store, ok := r.Client.(client.WithWatch)
	if !ok {
		t.Fatal("test client must support watches")
	}
	r.Client = interceptor.NewClient(store, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
			reads[key.Name]++
			return c.Get(ctx, key, object, options...)
		},
	})
	got := reconcileComposition(t, r, a)
	if readyCondition(t, got).Status != metav1.ConditionTrue || reads["d"] != 1 {
		t.Fatalf("diamond readiness=%+v, shared producer reads=%d", got.Status, reads["d"])
	}
}

// TestCompositionRequeuesTransitiveConsumers catches stranded ancestors after cycle or contract repair.
func TestCompositionRequeuesTransitiveConsumers(t *testing.T) {
	t.Parallel()
	a, b, c := testProduct("a"), testProduct("b"), testProduct("c")
	composeInput(t, a, b.Name, "v1.0.0")
	composeInput(t, b, c.Name, "v1.0.0")
	r := compositionReconciler(t, a, b, c)
	requests := r.requestsForDependency(t.Context(), c)
	if len(requests) != 2 || requests[0].Name != "a" || requests[1].Name != "b" {
		t.Fatalf("transitive consumer requests = %v, want a and b", requests)
	}
}

// TestCompositionLineageRecovery catches stale edges and stale compatibility after producer changes.
func TestCompositionLineageRecovery(t *testing.T) {
	t.Parallel()
	p, q, consumer := testProduct("p"), testProduct("q"), testProduct("consumer")
	p.Spec.Version, q.Spec.Version = "v1.2.0", "v1.0.0"
	markCompositionProducerReady(p)
	markCompositionProducerReady(q)
	composeInput(t, consumer, p.Name, "v1.1.0")
	composeInput(t, consumer, q.Name, "v1.0.0")
	r := compositionReconciler(t, p, q, consumer)
	got := reconcileComposition(t, r, consumer)
	if len(got.Status.Inputs) != 2 || got.Status.Inputs[0].Version != "v1.2.0" ||
		got.Status.Inputs[0].ProductRef.Namespace != "products" || got.Status.Inputs[0].ProductID != p.Spec.ID ||
		got.Status.Inputs[0].Owner.Name != "Data team" || got.Status.Inputs[0].Output.Name != "query" || !got.Status.Inputs[0].Ready {
		t.Fatalf("lineage = %+v", got.Status.Inputs)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Version = "v2.0.0"
	if err := r.Update(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	got = reconcileComposition(t, r, consumer)
	if readyCondition(t, got).Reason != "ContractIncompatible" || got.Status.Inputs[0].Ready ||
		got.Status.Inputs[0].Version != "v2.0.0" {
		t.Fatalf("breaking update not reflected: %+v", got.Status)
	}
	p.Spec.Version = "v1.3.0"
	if err := r.Update(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	got = reconcileComposition(t, r, consumer)
	if readyCondition(t, got).Status != metav1.ConditionTrue {
		t.Fatal("compatible upgrade did not recover")
	}
	got.Spec.Inputs = nil
	if err := r.Update(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	got = reconcileComposition(t, r, consumer)
	if len(got.Status.Inputs) != 0 ||
		meta.FindStatusCondition(
			got.Status.Conditions,
			datav1alpha1.ConditionCompositionReady,
		) != nil {
		t.Fatal("detached inputs retained lineage")
	}
}

// TestCompositionDisabledFailsClosed proves required contracts cannot bypass a disabled release flag.
func TestCompositionDisabledFailsClosed(t *testing.T) {
	t.Parallel()
	p, consumer := testProduct("p"), testProduct("consumer")
	p.Spec.Version = "v1.0.0"
	markCompositionProducerReady(p)
	composeInput(t, consumer, p.Name, "v1.0.0")
	r := compositionReconciler(t, p, consumer)
	reconcileComposition(t, r, consumer)
	r.CompositionEnabled = nil
	got := reconcileComposition(t, r, consumer)
	if readyCondition(t, got).Reason != "CompositionFeatureDisabled" ||
		len(got.Status.Inputs) != 0 {
		t.Fatal("disabled composition retained verified lineage")
	}
	got.Spec.Inputs[0].Contract = nil
	if err := r.Update(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	got = reconcileComposition(t, r, consumer)
	if readyCondition(t, got).Status != metav1.ConditionTrue {
		t.Fatal("legacy input lost existing behavior")
	}
}

// TestCompositionBounds catches unlimited recursion, fan-out and repeated diamond traversal.
func TestCompositionBounds(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"depth", "products", "edges", "shared deep path"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := testProduct("root")
			products := []*datav1alpha1.DataProduct{root}
			switch kind {
			case "depth", "shared deep path":
				previous := root
				for i := range 65 {
					p := testProduct(fmt.Sprintf("p-%d", i))
					p.Spec.Version = "v1.0.0"
					markCompositionProducerReady(p)
					composeInput(t, previous, p.Name, "v1.0.0")
					products = append(products, p)
					previous = p
				}
				if kind == "shared deep path" {
					// Visit the last half first, then reach its cached subtree by a longer path.
					short := root.Spec.Inputs[0]
					short.Name = "shortcut"
					short.ProductRef.Name = "p-32"
					root.Spec.Inputs = append([]datav1alpha1.InputPort{short}, root.Spec.Inputs...)
				}
			case "products":
				for i := range 256 {
					p := testProduct(fmt.Sprintf("p-%d", i))
					p.Spec.Version = "v1.0.0"
					markCompositionProducerReady(p)
					composeInput(t, root, p.Name, "v1.0.0")
					products = append(products, p)
				}
			case "edges":
				p := testProduct("p")
				p.Spec.Version = "v1.0.0"
				markCompositionProducerReady(p)
				products = append(products, p)
				for i := range 1025 {
					composeInput(t, root, p.Name, "v1.0.0")
					root.Spec.Inputs[i].Name = fmt.Sprintf("edge-%d", i)
				}
			}
			got := reconcileComposition(t, compositionReconciler(t, products...), root)
			if readyCondition(t, got).Reason != "CompositionLimitExceeded" {
				t.Fatalf("%s bound not enforced: %+v", kind, got.Status)
			}
		})
	}
}

// TestCompositionReportsMalformedEdges catches missing outputs, protocol drift and stale generations.
func TestCompositionReportsMalformedEdges(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "port", "protocol", "stale", "invalid minimum", "cross namespace"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			p, consumer := testProduct("p"), testProduct("consumer")
			p.Spec.Version = "v1.0.0"
			markCompositionProducerReady(p)
			composeInput(t, consumer, p.Name, "v1.0.0")
			want := "ContractIncompatible"
			switch kind {
			case "missing":
				consumer.Spec.Inputs[0].ProductRef.Name = "absent"
				want = "DependencyNotFound"
			case "port":
				consumer.Spec.Inputs[0].ProductRef.Output = "absent"
				want = "OutputNotFound"
			case "protocol":
				p.Spec.Outputs[0].Protocol = datav1alpha1.ProtocolGraphQL
			case "stale":
				p.Generation++
				want = "DependencyNotReady"
			case "invalid minimum":
				consumer.Spec.Inputs[0].Contract.MinimumVersion = "v1"
			case "cross namespace":
				p.Namespace = "upstream"
				consumer.Spec.Inputs[0].ProductRef.Namespace = "upstream"
				want = "DependenciesReady"
			}
			got := reconcileComposition(t, compositionReconciler(t, p, consumer), consumer)
			if readyCondition(t, got).Reason != want {
				t.Fatalf("reason = %s, want %s", readyCondition(t, got).Reason, want)
			}
			if strings.Contains(readyCondition(t, got).Message, "https://") {
				t.Fatal("diagnostic contains remote content")
			}
		})
	}
}

// TestCompositionRejectsCycles catches trusting stale Ready conditions around a dependency cycle.
func TestCompositionRejectsCycles(t *testing.T) {
	t.Parallel()
	for _, ready := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "unready", true: "previously ready"}[ready],
			func(t *testing.T) {
				t.Parallel()
				a, b, c := testProduct("a"), testProduct("b"), testProduct("c")
				composeInput(t, a, b.Name, "v1.0.0")
				composeInput(t, b, c.Name, "v1.0.0")
				composeInput(t, c, a.Name, "v1.0.0")
				if ready {
					for _, p := range []*datav1alpha1.DataProduct{a, b, c} {
						markCompositionProducerReady(p)
					}
				}
				r := compositionReconciler(t, a, b, c)
				got := reconcileComposition(t, r, a)
				condition := readyCondition(t, got)
				if condition.Status != metav1.ConditionFalse ||
					condition.Reason != "DependencyCycle" {
					t.Fatalf(
						"cycle readiness = %s/%s, want False/DependencyCycle",
						condition.Status,
						condition.Reason,
					)
				}
				// A repeated cycle observation must not write another status or hot-loop.
				again := reconcileComposition(t, r, a)
				if again.ResourceVersion != got.ResourceVersion {
					t.Fatal("unchanged cycle rewrote status")
				}
			},
		)
	}
}

// TestCompositionContractCompatibility catches accepting breaking or older producer contracts.
func TestCompositionContractCompatibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version, minimum, reason string
	}{
		{"v1.2.3", "v1.2.0", "DependenciesReady"},
		{"v2.0.0", "v1.0.0", "ContractIncompatible"},
		{"v1.1.0", "v1.2.0", "ContractIncompatible"},
		{"v0.2.0", "v0.1.0", "ContractIncompatible"},
		{"v0.1.0", "v0.1.0", "DependenciesReady"},
		{"v1.2.0-rc.1", "v1.0.0", "ContractIncompatible"},
		{"v1.2.0+build", "v1.0.0", "ContractIncompatible"},
		{"v01.2.0", "v1.0.0", "ContractIncompatible"},
	} {
		t.Run(tc.version+"/"+tc.minimum, func(t *testing.T) {
			t.Parallel()
			producer, consumer := testProduct("producer"), testProduct("consumer")
			producer.Spec.Version = tc.version
			markCompositionProducerReady(producer)
			composeInput(t, consumer, producer.Name, tc.minimum)
			r := compositionReconciler(t, producer, consumer)
			got := reconcileComposition(t, r, consumer)
			if condition := readyCondition(t, got); condition.Reason != tc.reason {
				t.Fatalf(
					"readiness = %s/%s, want %s",
					condition.Status,
					condition.Reason,
					tc.reason,
				)
			}
		})
	}
}

func composeInput(t *testing.T, consumer *datav1alpha1.DataProduct, producer, minimum string) {
	t.Helper()
	input := map[string]any{
		"name":       producer,
		"productRef": map[string]string{"name": producer, "output": "query"},
		"contract":   map[string]string{"protocol": "OpenAPI", "minimumVersion": minimum},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var port datav1alpha1.InputPort
	if err := json.Unmarshal(data, &port); err != nil {
		t.Fatal(err)
	}
	consumer.Spec.Inputs = append(consumer.Spec.Inputs, port)
	consumer.Spec.Version = "v1.0.0"
}

func markCompositionProducerReady(product *datav1alpha1.DataProduct) {
	product.Status.Conditions = []metav1.Condition{{
		Type: datav1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		ObservedGeneration: product.Generation, Reason: "DependenciesReady",
	}}
}

func compositionReconciler(
	t *testing.T,
	products ...*datav1alpha1.DataProduct,
) *DataProductReconciler {
	t.Helper()
	objects := make([]client.Object, len(products))
	for i, p := range products {
		objects[i] = p
	}
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).WithObjects(objects...).Build()
	return &DataProductReconciler{
		Client:             c,
		Scheme:             scheme,
		CompositionEnabled: func(context.Context) bool { return true },
	}
}

func reconcileComposition(
	t *testing.T,
	r *DataProductReconciler,
	product *datav1alpha1.DataProduct,
) *datav1alpha1.DataProduct {
	t.Helper()
	key := client.ObjectKeyFromObject(product)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	updated := &datav1alpha1.DataProduct{}
	if err := r.Get(context.Background(), key, updated); err != nil {
		t.Fatal(err)
	}
	return updated
}
