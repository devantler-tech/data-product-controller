package controller

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestEngineProviderDefaultsOff proves the independent engine release gate blocks observation.
func TestEngineProviderDefaultsOff(t *testing.T) {
	t.Parallel()
	product := testProduct("sql-product")
	if err := json.Unmarshal(
		[]byte(
			`{"source":{"adapter":"cnpg/v1","engine":{"apiVersion":"engine-provider/v1","type":"sql","provider":"native"},"resourceRef":{"apiVersion":"postgresql.cnpg.io/v1","kind":"Cluster","name":"warehouse"},"connectionSecretRef":{"name":"warehouse-app"}}}`,
		),
		&product.Spec,
	); err != nil {
		t.Fatal(err)
	}
	scheme := testScheme(t)
	store := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).
		WithObjects(product).
		Build()
	r := &DataProductReconciler{
		Client:         store,
		Scheme:         scheme,
		SourcesEnabled: func(context.Context) bool { return true },
	}
	key := client.ObjectKeyFromObject(product)
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	got := readyCondition(t, product)
	if got.Status != metav1.ConditionFalse || got.Reason != "EngineProviderFeatureDisabled" {
		t.Fatalf(
			"readiness = %s/%s, want False/EngineProviderFeatureDisabled",
			got.Status,
			got.Reason,
		)
	}
}

// TestProviderReconciliation observes external recovery independently of product dependencies and avoids status churn.
func TestProviderReconciliation(t *testing.T) {
	t.Parallel()
	product := testProduct("sql-product")
	product.Spec.Source = &datav1alpha1.ProvisionedSource{
		Adapter: "cnpg/v1",
		Engine: &datav1alpha1.EngineSelection{
			APIVersion: "engine-provider/v1",
			Type:       "sql",
			Provider:   "native",
		},
		ResourceRef: datav1alpha1.ProvisionedResourceReference{
			APIVersion: "postgresql.cnpg.io/v1",
			Kind:       "Cluster",
			Name:       "warehouse",
		},
		ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{Name: "warehouse-app"},
	}
	cluster := &unstructured.Unstructured{}
	if err := cluster.UnmarshalJSON(
		[]byte(
			`{"apiVersion":"postgresql.cnpg.io/v1","kind":"Cluster","metadata":{"name":"warehouse","namespace":"products","uid":"cluster-uid","generation":3},"spec":{"instances":1},"status":{"instances":1,"readyInstances":1,"currentPrimary":"warehouse-1","targetPrimary":"warehouse-1","conditions":[{"type":"Ready","status":"True"}]}}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "warehouse-app",
			Namespace: "products",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "postgresql.cnpg.io/v1",
					Kind:       "Cluster",
					Name:       "warehouse",
					UID:        "cluster-uid",
				},
			},
		},
		Data: map[string][]byte{"password": []byte("credential-sentinel")},
	}
	r, store := provisionedReconciler(t, product, cluster, secret)
	reader := &providerCountingReader{Reader: store}
	r.SourceReader = reader
	key := client.ObjectKeyFromObject(product)
	check := func(reason string, sourceStatus metav1.ConditionStatus) {
		t.Helper()
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatal(err)
		}
		if result.RequeueAfter != 30*time.Second {
			t.Fatalf("poll=%s", result.RequeueAfter)
		}
		if err := store.Get(t.Context(), key, product); err != nil {
			t.Fatal(err)
		}
		if got := readyCondition(t, product); got.Reason != reason {
			t.Fatalf("readiness=%+v want %s", got, reason)
		}
		source := meta.FindStatusCondition(
			product.Status.Conditions,
			datav1alpha1.ConditionSourceReady,
		)
		if source == nil || source.Status != sourceStatus ||
			source.ObservedGeneration != product.Generation {
			t.Fatalf("source readiness=%+v", source)
		}
		encoded, err := json.Marshal(product.Status)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "credential-sentinel") {
			t.Fatal("credentials exposed in status")
		}
	}
	check("EngineProviderFeatureDisabled", metav1.ConditionFalse)
	r.EngineProvidersEnabled = func(context.Context) bool { return true }
	r.SourcesEnabled = func(context.Context) bool { return false }
	check("SourceFeatureDisabled", metav1.ConditionFalse)
	if reader.calls.Load() != 0 {
		t.Fatal("disabled provider performed API reads")
	}
	r.SourcesEnabled = func(context.Context) bool { return true }
	check("DependenciesReady", metav1.ConditionTrue)
	version := product.ResourceVersion
	check("DependenciesReady", metav1.ConditionTrue)
	if version != product.ResourceVersion {
		t.Fatal("unchanged source rewrote status")
	}
	_ = unstructured.SetNestedField(cluster.Object, int64(0), "status", "readyInstances")
	if err := store.Update(t.Context(), cluster); err != nil {
		t.Fatal(err)
	}
	check("SourceNotReady", metav1.ConditionFalse)
	_ = unstructured.SetNestedField(cluster.Object, int64(1), "status", "readyInstances")
	if err := store.Update(t.Context(), cluster); err != nil {
		t.Fatal(err)
	}
	product.Spec.Inputs = []datav1alpha1.InputPort{
		{
			Name:       "missing",
			ProductRef: datav1alpha1.ProductReference{Name: "missing", Output: "query"},
		},
	}
	if err := store.Update(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	check("DependencyNotFound", metav1.ConditionTrue)
	product.Spec.Source = nil
	if err := store.Update(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	if meta.FindStatusCondition(
		product.Status.Conditions,
		datav1alpha1.ConditionSourceReady,
	) != nil {
		t.Fatal("removed source retained readiness")
	}
}

type providerCountingReader struct {
	client.Reader
	calls atomic.Int64
}

// Get counts external reads so disabled gates cannot silently acquire observation permissions.
func (r *providerCountingReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	options ...client.GetOption,
) error {
	r.calls.Add(1)
	return r.Reader.Get(ctx, key, object, options...)
}
