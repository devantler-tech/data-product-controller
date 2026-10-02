package controller

import (
	"context"
	"encoding/json"
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

// TestDocumentProviderReconciliation uses the actual registry through both release gates and independent source status.
func TestDocumentProviderReconciliation(t *testing.T) {
	t.Parallel()
	product := testProduct("document-product")
	if err := json.Unmarshal(
		[]byte(
			`{"source":{"adapter":"percona-mongodb/v1","engine":{"apiVersion":"engine-provider/v1","type":"document","provider":"native"},"resourceRef":{"apiVersion":"psmdb.percona.com/v1","kind":"PerconaServerMongoDB","name":"documents"},"connectionSecretRef":{"name":"documents-reader"}},"inputs":[{"name":"missing","productRef":{"name":"missing","output":"query"}}]}`,
		),
		&product.Spec,
	); err != nil {
		t.Fatal(err)
	}
	cluster := &unstructured.Unstructured{}
	if err := cluster.UnmarshalJSON(
		[]byte(
			`{"apiVersion":"psmdb.percona.com/v1","kind":"PerconaServerMongoDB","metadata":{"name":"documents","namespace":"products","uid":"document-uid"},"spec":{"crVersion":"1.23.0","replsets":[{"name":"rs0","size":3}],"users":[{"name":"catalog-reader","db":"admin","passwordSecretRef":{"name":"documents-reader"},"roles":[{"name":"read","db":"catalog"}]}]},"status":{"state":"ready","size":3,"ready":3}}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "documents-reader",
			Namespace: "products",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "psmdb.percona.com/v1",
					Kind:       "PerconaServerMongoDB",
					Name:       "documents",
					UID:        "document-uid",
				},
			},
		},
	}
	r, store := provisionedReconciler(t, product, cluster, secret)
	reader := &providerCountingReader{Reader: store}
	r.SourceReader = reader
	check := func(sourceReason string) {
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
		source := meta.FindStatusCondition(
			product.Status.Conditions,
			datav1alpha1.ConditionSourceReady,
		)
		if source == nil || source.Reason != sourceReason ||
			source.ObservedGeneration != product.Generation {
			t.Fatalf("source=%+v want %s", source, sourceReason)
		}
		wantReady := sourceReason
		if sourceReason == "SourceReady" {
			wantReady = "DependencyNotFound"
		}
		if got := readyCondition(t, product); got.Reason != wantReady {
			t.Fatalf("aggregate readiness=%+v want %s", got, wantReady)
		}
	}
	check("EngineProviderFeatureDisabled")
	r.EngineProvidersEnabled = func(context.Context) bool { return true }
	r.SourcesEnabled = func(context.Context) bool { return false }
	check("SourceFeatureDisabled")
	if reader.calls.Load() != 0 {
		t.Fatal("disabled Document provider performed external reads")
	}
	r.SourcesEnabled = func(context.Context) bool { return true }
	check("SourceReady")
	version := product.ResourceVersion
	check("SourceReady")
	if product.ResourceVersion != version {
		t.Fatal("unchanged Document observation rewrote status")
	}
	_ = unstructured.SetNestedField(cluster.Object, int64(2), "status", "ready")
	if err := store.Update(t.Context(), cluster); err != nil {
		t.Fatal(err)
	}
	check("SourceNotReady")
	_ = unstructured.SetNestedField(cluster.Object, int64(3), "status", "ready")
	if err := store.Update(t.Context(), cluster); err != nil {
		t.Fatal(err)
	}
	check("SourceReady")
}
