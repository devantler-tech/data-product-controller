package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	provisionerv1 "github.com/devantler-tech/data-product-controller/internal/provisioner/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

// TestProviderStatusSurvivesDependencyReadErrors preserves independent source health and removal during API failure.
func TestProviderStatusSurvivesDependencyReadErrors(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"new source", "recovered source", "source removed", "engine selection removed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			product := testProduct("sql-product")
			product.Generation = 2
			product.Spec.Source = &datav1alpha1.ProvisionedSource{
				Adapter: "cnpg/v1",
				Engine: &datav1alpha1.EngineSelection{
					APIVersion: "engine-provider/v1", Type: "sql", Provider: "native",
				},
			}
			product.Spec.Inputs = []datav1alpha1.InputPort{
				{
					Name:       "upstream",
					ProductRef: datav1alpha1.ProductReference{Name: "producer", Output: "query"},
				},
			}
			setReadiness(product, metav1.ConditionTrue, "DependenciesReady", "Previously ready")
			if mode != "new source" {
				meta.SetStatusCondition(&product.Status.Conditions, metav1.Condition{
					Type:               datav1alpha1.ConditionSourceReady,
					Status:             metav1.ConditionFalse,
					Reason:             "SourceNotReady",
					Message:            "Previous observation",
					ObservedGeneration: 1,
				})
			}
			remove := mode == "source removed" || mode == "engine selection removed"
			switch mode {
			case "source removed":
				product.Spec.Source = nil
			case "engine selection removed":
				product.Spec.Source.Engine = nil
				product.Spec.Source.Adapter = "crossplane/v1"
			}
			scheme := testScheme(t)
			store := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&datav1alpha1.DataProduct{}).WithObjects(product).Build()
			r := &DataProductReconciler{
				Client: interceptor.NewClient(store, interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if key.Name == "producer" {
							return apierrors.NewForbidden(
								datav1alpha1.GroupVersion.WithResource("dataproducts").
									GroupResource(),
								key.Name,
								errors.New("sensitive sentinel"),
							)
						}
						return c.Get(ctx, key, obj, opts...)
					},
				}),
				Scheme: scheme, SourceProvider: readySourceProvider{},
				SourcesEnabled:         func(context.Context) bool { return true },
				EngineProvidersEnabled: func(context.Context) bool { return true },
			}
			key := client.ObjectKeyFromObject(product)
			if _, err := r.Reconcile(
				t.Context(),
				ctrl.Request{NamespacedName: key},
			); !apierrors.IsForbidden(
				err,
			) {
				t.Fatalf("dependency failure must remain retryable: %v", err)
			}
			if err := store.Get(t.Context(), key, product); err != nil {
				t.Fatal(err)
			}
			condition := meta.FindStatusCondition(
				product.Status.Conditions,
				datav1alpha1.ConditionSourceReady,
			)
			if remove && condition != nil {
				t.Fatalf("removed engine retained source status: %+v", condition)
			}
			if !remove &&
				(condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != product.Generation) {
				t.Fatalf("fresh source observation was discarded: %+v", condition)
			}
			ready := readyCondition(t, product)
			if ready.Status != metav1.ConditionFalse || ready.Reason != "DependencyUnavailable" ||
				strings.Contains(ready.Message, "sensitive sentinel") {
				t.Fatalf("dependency failure retained readiness or leaked details: %+v", ready)
			}
		})
	}
}

// readySourceProvider isolates status persistence from the separately tested engine API observation.
type readySourceProvider struct{}

// Observe supplies a successful source observation while the product-dependency API fails.
func (readySourceProvider) Observe(
	context.Context,
	string,
	datav1alpha1.ProvisionedSource,
) provisionerv1.Observation {
	return provisionerv1.Observation{
		Ready:   true,
		Reason:  "SourceReady",
		Message: "Observed test source",
	}
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
