package controller

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestGraphReconciliation protects both zero-read disabled gates and independent current-spec source health.
func TestGraphReconciliation(t *testing.T) {
	t.Parallel()
	product := testProduct("graph-product")
	if err := json.Unmarshal(
		[]byte(
			`{"source":{"adapter":"arangodb/v1","engine":{"apiVersion":"engine-provider/v1","type":"graph","provider":"native"},"resourceRef":{"apiVersion":"database.arangodb.com/v1","kind":"ArangoDeployment","name":"lineage"},"connectionSecretRef":{"name":"lineage-reader"}},"inputs":[{"name":"missing","productRef":{"name":"missing","output":"query"}}]}`,
		),
		&product.Spec,
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../tests/source/fixtures/arango-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	cluster := &unstructured.Unstructured{}
	if err := cluster.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "lineage-reader",
		Namespace: "products",
		OwnerReferences: []metav1.OwnerReference{
			{
				APIVersion: "database.arangodb.com/v1",
				Kind:       "ArangoDeployment",
				Name:       "lineage",
				UID:        "graph-uid",
			},
		},
		Annotations: map[string]string{
			"data.devantler.tech/arango-publication": "v1",
			"data.devantler.tech/arango-user":        "catalog-reader",
			"data.devantler.tech/arango-database":    "catalog",
			"data.devantler.tech/arango-graph":       "lineage",
			"data.devantler.tech/arango-access":      "read-only",
			"data.devantler.tech/arango-collections": "products,relations",
		},
	}}
	r, store := provisionedReconciler(t, product, cluster, secret)
	reader := &providerCountingReader{Reader: store}
	r.SourceReader = reader
	check := func(reason string) {
		t.Helper()
		result, err := r.Reconcile(
			t.Context(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(product)},
		)
		if err != nil || result.RequeueAfter != 30*time.Second {
			t.Fatalf("reconcile=%+v/%v", result, err)
		}
		if err := store.Get(t.Context(), client.ObjectKeyFromObject(product), product); err != nil {
			t.Fatal(err)
		}
		condition := meta.FindStatusCondition(
			product.Status.Conditions,
			datav1alpha1.ConditionSourceReady,
		)
		if condition == nil || condition.Reason != reason ||
			condition.ObservedGeneration != product.Generation {
			t.Fatalf("source=%+v want %s", condition, reason)
		}
		want := reason
		if reason == "SourceReady" {
			want = "DependencyNotFound"
		}
		if got := readyCondition(t, product); got.Reason != want {
			t.Fatalf("aggregate readiness=%+v want %s", got, want)
		}
	}
	check("EngineProviderFeatureDisabled")
	r.EngineProvidersEnabled = func(context.Context) bool { return true }
	r.SourcesEnabled = func(context.Context) bool { return false }
	check("SourceFeatureDisabled")
	if reader.calls.Load() != 0 {
		t.Fatal("disabled Graph observation read external resources")
	}
	r.SourcesEnabled = func(context.Context) bool { return true }
	check("SourceReady")
	version := product.ResourceVersion
	check("SourceReady")
	if product.ResourceVersion != version {
		t.Fatal("unchanged Graph observation rewrote status")
	}
	_ = unstructured.SetNestedField(cluster.Object, "old", "status", "appliedVersion")
	if err := store.Update(t.Context(), cluster); err != nil {
		t.Fatal(err)
	}
	check("SourceNotReady")
	_ = unstructured.SetNestedField(
		cluster.Object,
		"226d7bb3a616e388ca182310e0881c2d31bb8b43925a0f6ee624f3d80e77ea94",
		"status",
		"appliedVersion",
	)
	if err := store.Update(t.Context(), cluster); err != nil {
		t.Fatal(err)
	}
	check("SourceReady")
}
