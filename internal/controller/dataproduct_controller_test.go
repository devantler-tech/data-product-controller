package controller

import (
	"context"
	"testing"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileMarksStandaloneProductReady(t *testing.T) {
	t.Parallel()

	product := testProduct("catalog")
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: product.Name, Namespace: product.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile standalone product: %v", err)
	}

	updated := &datav1alpha1.DataProduct{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: product.Name, Namespace: product.Namespace,
	}, updated); err != nil {
		t.Fatalf("read reconciled product: %v", err)
	}

	condition := readyCondition(t, updated)
	if condition.Status != metav1.ConditionTrue {
		t.Fatalf("Ready status = %s, want %s", condition.Status, metav1.ConditionTrue)
	}
	if condition.Reason != "DependenciesReady" {
		t.Fatalf("Ready reason = %q, want %q", condition.Reason, "DependenciesReady")
	}
	if updated.Status.ObservedGeneration != updated.Generation {
		t.Fatalf(
			"observed generation = %d, want %d",
			updated.Status.ObservedGeneration,
			updated.Generation,
		)
	}
}

func TestReconcileReportsMissingProductDependency(t *testing.T) {
	t.Parallel()

	product := testProduct("combined-catalog")
	product.Spec.Inputs = []datav1alpha1.InputPort{{
		Name: "customers",
		ProductRef: datav1alpha1.ProductReference{
			Name:   "customer-data",
			Output: "query",
		},
	}}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: product.Name, Namespace: product.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile product with missing dependency: %v", err)
	}

	updated := &datav1alpha1.DataProduct{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: product.Name, Namespace: product.Namespace,
	}, updated); err != nil {
		t.Fatalf("read reconciled product: %v", err)
	}

	condition := readyCondition(t, updated)
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("Ready status = %s, want %s", condition.Status, metav1.ConditionFalse)
	}
	if condition.Reason != "DependencyNotFound" {
		t.Fatalf("Ready reason = %q, want %q", condition.Reason, "DependencyNotFound")
	}
	if condition.Message != `Input "customers" references missing DataProduct products/customer-data.` {
		t.Fatalf("Ready message = %q", condition.Message)
	}
}

func TestReconcileReportsUnreadyProductDependency(t *testing.T) {
	t.Parallel()

	dependency := testProduct("customer-data")
	dependency.Status.Conditions = []metav1.Condition{{
		Type:   datav1alpha1.ConditionReady,
		Status: metav1.ConditionFalse,
		Reason: "DependencyNotFound",
	}}
	product := testProduct("combined-catalog")
	product.Spec.Inputs = []datav1alpha1.InputPort{{
		Name: "customers",
		ProductRef: datav1alpha1.ProductReference{
			Name:   dependency.Name,
			Output: "query",
		},
	}}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product, dependency).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: product.Name, Namespace: product.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile product with unready dependency: %v", err)
	}

	updated := &datav1alpha1.DataProduct{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: product.Name, Namespace: product.Namespace,
	}, updated); err != nil {
		t.Fatalf("read reconciled product: %v", err)
	}

	condition := readyCondition(t, updated)
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("Ready status = %s, want %s", condition.Status, metav1.ConditionFalse)
	}
	if condition.Reason != "DependencyNotReady" {
		t.Fatalf("Ready reason = %q, want %q", condition.Reason, "DependencyNotReady")
	}
	if condition.Message != `Input "customers" references DataProduct products/customer-data, which is not Ready.` {
		t.Fatalf("Ready message = %q", condition.Message)
	}
}

func TestReconcileReportsMissingDependencyOutput(t *testing.T) {
	t.Parallel()

	dependency := testProduct("customer-data")
	dependency.Status.Conditions = []metav1.Condition{{
		Type:               datav1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: dependency.Generation,
		Reason:             "DependenciesReady",
	}}
	product := testProduct("combined-catalog")
	product.Spec.Inputs = []datav1alpha1.InputPort{{
		Name: "customers",
		ProductRef: datav1alpha1.ProductReference{
			Name:   dependency.Name,
			Output: "events",
		},
	}}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product, dependency).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: product.Name, Namespace: product.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile product with missing dependency output: %v", err)
	}

	updated := &datav1alpha1.DataProduct{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: product.Name, Namespace: product.Namespace,
	}, updated); err != nil {
		t.Fatalf("read reconciled product: %v", err)
	}

	condition := readyCondition(t, updated)
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("Ready status = %s, want %s", condition.Status, metav1.ConditionFalse)
	}
	if condition.Reason != "OutputNotFound" {
		t.Fatalf("Ready reason = %q, want %q", condition.Reason, "OutputNotFound")
	}
	if condition.Message != `Input "customers" references missing output "events" on DataProduct products/customer-data.` {
		t.Fatalf("Ready message = %q", condition.Message)
	}
}

func TestReconcileRejectsStaleReadyDependency(t *testing.T) {
	t.Parallel()

	dependency := testProduct("customer-data")
	dependency.Status.Conditions = []metav1.Condition{{
		Type:               datav1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: dependency.Generation - 1,
		Reason:             "DependenciesReady",
	}}
	product := testProduct("combined-catalog")
	product.Spec.Inputs = []datav1alpha1.InputPort{{
		Name: "customers",
		ProductRef: datav1alpha1.ProductReference{
			Name:   dependency.Name,
			Output: "query",
		},
	}}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product, dependency).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: product.Name, Namespace: product.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile product with stale dependency status: %v", err)
	}

	updated := &datav1alpha1.DataProduct{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: product.Name, Namespace: product.Namespace,
	}, updated); err != nil {
		t.Fatalf("read reconciled product: %v", err)
	}
	condition := readyCondition(t, updated)
	if condition.Status != metav1.ConditionFalse || condition.Reason != "DependencyNotReady" {
		t.Fatalf(
			"Ready condition = %s/%s, want %s/%s",
			condition.Status,
			condition.Reason,
			metav1.ConditionFalse,
			"DependencyNotReady",
		)
	}
}

func TestDependencyEventsEnqueueConsumingProducts(t *testing.T) {
	t.Parallel()

	producer := testProduct("customer-data")
	consumer := testProduct("combined-catalog")
	consumer.Spec.Inputs = []datav1alpha1.InputPort{{
		Name: "customers",
		ProductRef: datav1alpha1.ProductReference{
			Name:   producer.Name,
			Output: "query",
		},
	}}
	unrelated := testProduct("orders")
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(producer, consumer, unrelated).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	requests := reconciler.requestsForDependency(context.Background(), producer)

	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	want := types.NamespacedName{Name: consumer.Name, Namespace: consumer.Namespace}
	if requests[0].NamespacedName != want {
		t.Fatalf("request = %s, want %s", requests[0].NamespacedName, want)
	}
}

func TestReconcileDoesNotWriteUnchangedReadyStatus(t *testing.T) {
	t.Parallel()

	product := testProduct("catalog")
	product.ResourceVersion = "7"
	product.Status.ObservedGeneration = product.Generation
	product.Status.Conditions = []metav1.Condition{{
		Type:               datav1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: product.Generation,
		LastTransitionTime: metav1.Unix(1_788_033_600, 0),
		Reason:             "DependenciesReady",
		Message:            "All referenced data products and output ports are ready.",
	}}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product).
		Build()
	reconciler := &DataProductReconciler{Client: client, Scheme: scheme}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: product.Name, Namespace: product.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile product with unchanged status: %v", err)
	}

	updated := &datav1alpha1.DataProduct{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name: product.Name, Namespace: product.Namespace,
	}, updated); err != nil {
		t.Fatalf("read reconciled product: %v", err)
	}
	if updated.ResourceVersion != "7" {
		t.Fatalf("resource version = %q, want unchanged %q", updated.ResourceVersion, "7")
	}
}

func testProduct(name string) *datav1alpha1.DataProduct {
	return &datav1alpha1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "products", Generation: 3},
		Spec: datav1alpha1.DataProductSpec{
			ID:          "urn:devantler:data-product:" + name,
			Name:        name,
			Description: "A product used by controller tests.",
			Version:     "v0.1.0",
			Owner:       datav1alpha1.ProductOwner{Name: "Data team"},
			Outputs: []datav1alpha1.OutputPort{{
				Name:        "query",
				Protocol:    datav1alpha1.ProtocolOpenAPI,
				URL:         "https://example.test/query",
				ContractURL: "https://example.test/openapi.json",
			}},
		},
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register data-product API: %v", err)
	}

	return scheme
}

func readyCondition(t *testing.T, product *datav1alpha1.DataProduct) metav1.Condition {
	t.Helper()

	for _, condition := range product.Status.Conditions {
		if condition.Type == datav1alpha1.ConditionReady {
			return condition
		}
	}

	t.Fatal("Ready condition not found")

	return metav1.Condition{}
}
