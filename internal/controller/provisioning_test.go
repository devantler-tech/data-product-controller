package controller

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProvisionedSourceDefaultsOff(t *testing.T) {
	t.Parallel()

	product := testProduct("provisioned")
	if err := json.Unmarshal(
		[]byte(
			`{"source":{"adapter":"crossplane/v1","resourceRef":{"apiVersion":"database.example.org/v1alpha1","kind":"Database","name":"warehouse"},"connectionSecretRef":{"name":"warehouse-connection"}}}`,
		),
		&product.Spec,
	); err != nil {
		t.Fatal(err)
	}
	scheme := testScheme(t)
	store := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).WithObjects(product).Build()
	reconciler := &DataProductReconciler{Client: store, Scheme: scheme}
	key := client.ObjectKeyFromObject(product)
	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	condition := readyCondition(t, product)
	if condition.Status != metav1.ConditionFalse || condition.Reason != "SourceFeatureDisabled" {
		t.Fatalf(
			"source with default-off flag reported %s/%s; want False/SourceFeatureDisabled",
			condition.Status,
			condition.Reason,
		)
	}
}

func TestProvisionedSourceReadiness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                     string
		mutate                   func(*unstructured.Unstructured, *corev1.Secret)
		omitResource, omitSecret bool
		want                     string
	}{
		{name: "ready", want: "DependenciesReady"},
		{name: "missing resource", omitResource: true, want: "SourceNotFound"},
		{name: "missing connection", omitSecret: true, want: "ConnectionNotPublished"},
		{
			name:   "unrelated connection",
			mutate: func(_ *unstructured.Unstructured, s *corev1.Secret) { s.OwnerReferences = nil },
			want:   "ConnectionOwnerMismatch",
		},
		{
			name:   "recreated source",
			mutate: func(r *unstructured.Unstructured, _ *corev1.Secret) { r.SetUID("new-source-uid") },
			want:   "ConnectionOwnerMismatch",
		},
		{
			name:   "unpublished reference",
			mutate: func(r *unstructured.Unstructured, _ *corev1.Secret) { delete(r.Object, "spec") },
			want:   "ConnectionReferenceMismatch",
		},
		{
			name:   "no conditions",
			mutate: func(r *unstructured.Unstructured, _ *corev1.Secret) { delete(r.Object, "status") },
			want:   "SourceNotReady",
		},
		{name: "not ready", mutate: func(r *unstructured.Unstructured, _ *corev1.Secret) {
			r.Object["status"] = map[string]any{
				"conditions": []any{
					map[string]any{
						"type":    "Ready",
						"status":  "False",
						"message": "private provider detail",
					},
				},
			}
		}, want: "SourceNotReady"},
		{name: "stale generation", mutate: func(r *unstructured.Unstructured, _ *corev1.Secret) {
			_ = unstructured.SetNestedField(r.Object, int64(2), "status", "observedGeneration")
		}, want: "SourceNotReady"},
		{name: "deleting resource", mutate: func(r *unstructured.Unstructured, _ *corev1.Secret) {
			now := metav1.Now()
			r.SetDeletionTimestamp(&now)
			r.SetFinalizers([]string{"provisioner.example.org/retain"})
		}, want: "SourceDeleting"},
		{name: "deleting connection", mutate: func(_ *unstructured.Unstructured, s *corev1.Secret) {
			now := metav1.Now()
			s.DeletionTimestamp = &now
			s.Finalizers = []string{"provisioner.example.org/retain"}
		}, want: "ConnectionNotPublished"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			product, resource, secret := provisionedFixture()
			if test.mutate != nil {
				test.mutate(resource, secret)
			}
			objects := []client.Object{product}
			if !test.omitResource {
				objects = append(objects, resource)
			}
			if !test.omitSecret {
				objects = append(objects, secret)
			}
			reconciler, store := provisionedReconciler(t, objects...)
			key := client.ObjectKeyFromObject(product)
			result, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Get(t.Context(), key, product); err != nil {
				t.Fatal(err)
			}
			condition := readyCondition(t, product)
			if condition.Reason != test.want {
				t.Fatalf(
					"condition = %s/%s, want %s",
					condition.Status,
					condition.Reason,
					test.want,
				)
			}
			if (condition.Status == metav1.ConditionTrue) != (test.want == "DependenciesReady") {
				t.Fatalf("unexpected readiness: %s", condition.Status)
			}
			if result.RequeueAfter <= 0 || result.RequeueAfter > time.Minute {
				t.Fatalf("external readiness must refresh within a minute: %v", result)
			}
			if condition.Message == "private provider detail" {
				t.Fatal("provider detail leaked into public status")
			}
		})
	}
}

func TestProvisionedSourceOpenFeatureStates(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			product, resource, secret := provisionedFixture()
			reconciler, store := provisionedReconciler(t, product, resource, secret)
			flags, err := featureflag.NewClient(
				t.Name(),
				featureflag.NewProvider(map[string]bool{"provisioned-sources": enabled}),
			)
			if err != nil {
				t.Fatal(err)
			}
			reconciler.SourcesEnabled = func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "provisioned-sources") }
			key := client.ObjectKeyFromObject(product)
			if _, err := reconciler.Reconcile(
				t.Context(),
				ctrl.Request{NamespacedName: key},
			); err != nil {
				t.Fatal(err)
			}
			if err := store.Get(t.Context(), key, product); err != nil {
				t.Fatal(err)
			}
			if got := readyCondition(t, product); (got.Status == metav1.ConditionTrue) != enabled {
				t.Fatalf("flag %t: condition = %+v", enabled, got)
			}
		})
	}
}

func TestProvisionedSourceStillRequiresInputsAndKeepsPolling(t *testing.T) {
	t.Parallel()
	product, resource, secret := provisionedFixture()
	if err := json.Unmarshal(
		[]byte(
			`{"inputs":[{"name":"upstream","productRef":{"name":"producer","output":"query"}}]}`,
		),
		&product.Spec,
	); err != nil {
		t.Fatal(err)
	}
	reconciler, store := provisionedReconciler(t, product, resource, secret)
	key := client.ObjectKeyFromObject(product)
	result, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	if got := readyCondition(
		t,
		product,
	); got.Reason != "DependencyNotFound" ||
		result.RequeueAfter != 30*time.Second {
		t.Fatalf("healthy source with missing input: condition=%+v result=%+v", got, result)
	}
	producer := testProduct("producer")
	setReadiness(producer, metav1.ConditionTrue, "DependenciesReady", "Ready")
	if err := store.Create(t.Context(), producer); err != nil {
		t.Fatal(err)
	}
	result, err = reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	if got := readyCondition(
		t,
		product,
	); got.Status != metav1.ConditionTrue ||
		result.RequeueAfter != 30*time.Second {
		t.Fatalf("recovered input: condition=%+v result=%+v", got, result)
	}
}

func TestProvisionedSourceRecoveryRotationAndRetention(t *testing.T) {
	t.Parallel()
	product, resource, secret := provisionedFixture()
	reconciler, store := provisionedReconciler(t, product, resource)
	key := client.ObjectKeyFromObject(product)
	reconcile := func() {
		t.Helper()
		if _, err := reconciler.Reconcile(
			t.Context(),
			ctrl.Request{NamespacedName: key},
		); err != nil {
			t.Fatal(err)
		}
		if err := store.Get(t.Context(), key, product); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	if got := readyCondition(t, product).Reason; got != "ConnectionNotPublished" {
		t.Fatalf("missing connection = %s", got)
	}
	if err := store.Create(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if got := readyCondition(t, product).Status; got != metav1.ConditionTrue {
		t.Fatalf("published connection = %s", got)
	}
	version := product.ResourceVersion
	secret.Data["password"] = []byte("rotated-private-password")
	if err := store.Update(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if product.ResourceVersion != version {
		t.Fatal("credential rotation must not rewrite public product status")
	}
	if err := store.Delete(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	retained := &corev1.Secret{}
	if err := store.Get(t.Context(), client.ObjectKeyFromObject(secret), retained); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secret, retained) {
		t.Fatal("controller mutated the connection Secret")
	}
	retainedResource := resource.DeepCopy()
	if err := store.Get(
		t.Context(),
		client.ObjectKeyFromObject(resource),
		retainedResource,
	); err != nil {
		t.Fatal(err)
	}
	if len(retainedResource.GetOwnerReferences()) != 0 ||
		len(retainedResource.GetFinalizers()) != 0 {
		t.Fatal("controller took ownership of the external source")
	}
}

func provisionedFixture() (*datav1alpha1.DataProduct, *unstructured.Unstructured, *corev1.Secret) {
	product := testProduct("provisioned")
	product.Spec.Source = &datav1alpha1.ProvisionedSource{
		Adapter: "crossplane/v1",
		ResourceRef: datav1alpha1.ProvisionedResourceReference{
			APIVersion: "database.example.org/v1alpha1",
			Kind:       "Database",
			Name:       "warehouse",
		},
		ConnectionSecretRef: datav1alpha1.ConnectionSecretReference{Name: "warehouse-connection"},
	}
	resource := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "database.example.org/v1alpha1",
		"kind":       "Database",
		"metadata": map[string]any{
			"name":       "warehouse",
			"namespace":  "products",
			"generation": int64(3),
			"uid":        "source-uid",
		},
		"spec": map[string]any{
			"writeConnectionSecretToRef": map[string]any{"name": "warehouse-connection"},
		},
		"status": map[string]any{"conditions": []any{
			map[string]any{
				"type":   "Ready",
				"status": "True",
			},
			map[string]any{"type": "Synced", "status": "True"},
		}},
	}}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "warehouse-connection", Namespace: "products"},
		Data:       map[string][]byte{"password": []byte("private-password")},
	}
	secret.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "database.example.org/v1alpha1",
			Kind:       "Database",
			Name:       "warehouse",
			UID:        "source-uid",
		},
	}
	return product, resource, secret
}

func provisionedReconciler(
	t *testing.T,
	objects ...client.Object,
) (*DataProductReconciler, client.Client) {
	t.Helper()
	scheme := testScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mapper := meta.NewDefaultRESTMapper(
		[]schema.GroupVersion{
			{Group: "database.example.org", Version: "v1alpha1"},
			datav1alpha1.GroupVersion,
			corev1.SchemeGroupVersion,
		},
	)
	mapper.Add(
		schema.GroupVersionKind{
			Group:   "database.example.org",
			Version: "v1alpha1",
			Kind:    "Database",
		},
		meta.RESTScopeNamespace,
	)
	mapper.Add(datav1alpha1.GroupVersion.WithKind("DataProduct"), meta.RESTScopeNamespace)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("Secret"), meta.RESTScopeNamespace)
	store := fake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(mapper).
		WithStatusSubresource(&datav1alpha1.DataProduct{}).WithObjects(objects...).Build()
	return &DataProductReconciler{
		Client:         store,
		Scheme:         scheme,
		SourceReader:   store,
		SourcesEnabled: func(context.Context) bool { return true },
	}, store
}
