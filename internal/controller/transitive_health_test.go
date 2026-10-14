package controller

import (
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestObservedTransitiveReadiness checks the same persisted leaf snapshot and recovery.
func TestObservedTransitiveReadiness(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"healthy", "false", "missing", "stale", "deleting"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			leaf, bridge, consumer := testProduct(
				"leaf",
			), testProduct(
				"bridge",
			), testProduct(
				"consumer",
			)
			for _, p := range []*data.DataProduct{leaf, bridge, consumer} {
				p.Spec.Version = "v1.0.0"
			}
			composeInput(t, bridge, leaf.Name, "v1.0.0")
			composeInput(t, consumer, bridge.Name, "v1.0.0")
			markCompositionProducerReady(leaf)
			markCompositionProducerReady(bridge)
			switch boundary {
			case "false":
				leaf.Status.Conditions[0].Status = metav1.ConditionFalse
			case "missing":
				leaf.Status.Conditions = nil
			case "stale":
				leaf.Status.Conditions[0].ObservedGeneration--
			case "deleting":
				now := metav1.Now()
				leaf.DeletionTimestamp = &now
				leaf.Finalizers = []string{"example.test/retain"}
			}
			reconciler := compositionReconciler(t, leaf, bridge, consumer)
			got := reconcileComposition(t, reconciler, consumer)
			condition := readyCondition(t, got)
			if (condition.Status == metav1.ConditionTrue) != (boundary == "healthy") {
				t.Fatalf("transitive %s: %+v", boundary, condition)
			}
			if boundary != "healthy" && condition.Reason != "DependencyNotReady" {
				t.Fatalf("unexpected failure: %+v", condition)
			}
			if len(got.Status.Inputs) != 1 || !got.Status.Inputs[0].Ready {
				t.Fatal("direct healthy bridge lineage lost its independent readiness")
			}
			if boundary != "deleting" {
				current := &data.DataProduct{}
				if err := reconciler.Get(
					t.Context(),
					client.ObjectKeyFromObject(leaf),
					current,
				); err != nil {
					t.Fatal(err)
				}
				markCompositionProducerReady(current)
				if err := reconciler.Status().Update(t.Context(), current); err != nil {
					t.Fatal(err)
				}
				got = reconcileComposition(t, reconciler, consumer)
				if c := readyCondition(t, got); c.Status != metav1.ConditionTrue {
					t.Fatalf("transitive recovery did not restore consumer: %+v", c)
				}
			}
		})
	}
}
