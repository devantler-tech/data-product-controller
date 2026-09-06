package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestConnectorDefaultsOff prevents a declared connector from appearing ready without observation.
func TestConnectorDefaultsOff(t *testing.T) {
	t.Parallel()
	product := testProduct("export")
	if err := json.Unmarshal(
		[]byte(
			`{"connector":{"adapter":"deployment/v1","resourceRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"export"}}}`,
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
	reconciler := &DataProductReconciler{Client: store, Scheme: scheme}
	key := client.ObjectKeyFromObject(product)
	if _, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Ready", "ConnectorReady"} {
		condition := meta.FindStatusCondition(product.Status.Conditions, name)
		if condition == nil || condition.Status != metav1.ConditionFalse ||
			condition.Reason != "ConnectorFeatureDisabled" {
			t.Errorf("%s = %+v, want False/ConnectorFeatureDisabled", name, condition)
		}
	}
}

// TestConnectorObservation rejects incomplete rollouts and unsafe or unavailable references.
func TestConnectorObservation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		mutate  func(*datav1alpha1.DataProduct, *appsv1.Deployment)
		readErr error
		missing bool
		want    string
	}{
		{name: "ready", want: "ConnectorReady"},
		{name: "missing", missing: true, want: "ConnectorNotFound"},
		{name: "denied", readErr: apierrors.NewForbidden(appsv1.Resource("deployments"), "export", errors.New("sensitive sentinel")), want: "ConnectorAccessDenied"},
		{name: "API outage", readErr: errors.New("sensitive sentinel"), want: "ConnectorUnavailable"},
		{name: "cross namespace", mutate: func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.Connector.ResourceRef.Namespace = "other"
		}, want: "ConnectorInvalid"},
		{name: "unsupported adapter", mutate: func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) { p.Spec.Connector.Adapter = "unknown/v1" }, want: "ConnectorInvalid"},
		{name: "unsupported kind", mutate: func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) { p.Spec.Connector.ResourceRef.Kind = "Secret" }, want: "ConnectorInvalid"},
		{name: "unsupported API", mutate: func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.Connector.ResourceRef.APIVersion = "apps/v1beta1"
		}, want: "ConnectorInvalid"},
		{name: "invalid name", mutate: func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.Connector.ResourceRef.Name = "../export"
		}, want: "ConnectorInvalid"},
		{name: "deleting", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			now := metav1.Now()
			d.DeletionTimestamp = &now
			d.Finalizers = []string{"example.org/retain"}
		}, want: "ConnectorDeleting"},
		{name: "zero replicas", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { zero := int32(0); d.Spec.Replicas = &zero }, want: "ConnectorScaledToZero"},
		{name: "stale generation", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Generation++ }, want: "ConnectorStatusStale"},
		{name: "future generation", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.ObservedGeneration++ }, want: "ConnectorStatusStale"},
		{name: "unobserved generation", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Generation = 0
			d.Status.ObservedGeneration = 0
		}, want: "ConnectorStatusStale"},
		{name: "old replica", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.UpdatedReplicas = 1 }, want: "ConnectorNotReady"},
		{name: "surge", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.Replicas = 3 }, want: "ConnectorNotReady"},
		{name: "pod unready", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.ReadyReplicas = 1 }, want: "ConnectorNotReady"},
		{name: "minimum availability only", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.AvailableReplicas = 1 }, want: "ConnectorNotReady"},
		{name: "unavailable replicas", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.UnavailableReplicas = 1 }, want: "ConnectorNotReady"},
		{name: "replicas default to one", mutate: func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Replicas = nil
			d.Status.Replicas = 1
			d.Status.UpdatedReplicas = 1
			d.Status.ReadyReplicas = 1
			d.Status.AvailableReplicas = 1
		}, want: "ConnectorReady"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			product, deployment := connectorFixture(t)
			if test.mutate != nil {
				test.mutate(product, deployment)
			}
			objects := []client.Object{product}
			if !test.missing {
				objects = append(objects, deployment)
			}
			reconciler, store := connectorReconciler(t, objects...)
			reads := 0
			reconciler.ConnectorReader = interceptor.NewClient(store, interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					reads++
					if _, ok := obj.(*appsv1.Deployment); !ok ||
						key != (client.ObjectKey{Namespace: "products", Name: "export"}) {
						t.Fatalf("unexpected workload read: %T %v", obj, key)
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
						t.Fatal("workload read requires a bounded deadline")
					}
					if test.readErr != nil {
						return test.readErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
			})
			result := reconcileConnector(t, reconciler, store, product)
			condition := meta.FindStatusCondition(product.Status.Conditions, "ConnectorReady")
			if condition == nil || condition.Reason != test.want ||
				(condition.Status == metav1.ConditionTrue) != (test.want == "ConnectorReady") ||
				condition.ObservedGeneration != product.Generation {
				t.Fatalf("ConnectorReady = %+v, want %s", condition, test.want)
			}
			if got := readyCondition(
				t,
				product,
			); (got.Status == metav1.ConditionTrue) != (test.want == "ConnectorReady") {
				t.Fatalf("aggregate Ready = %+v", got)
			}
			if strings.Contains(condition.Message, "sensitive sentinel") {
				t.Fatal("private error leaked to status")
			}
			if result.RequeueAfter <= 0 || result.RequeueAfter > time.Minute {
				t.Fatalf("unbounded observation retry: %+v", result)
			}
			if (test.want == "ConnectorInvalid" && reads != 0) ||
				(test.want != "ConnectorInvalid" && reads != 1) {
				t.Fatalf("unexpected reads: %d", reads)
			}
		})
	}
}

// TestConnectorRecoveryAndRemoval exercises registry visibility, condition cleanup, and idle status stability.
func TestConnectorRecoveryAndRemoval(t *testing.T) {
	t.Parallel()
	product, deployment := connectorFixture(t)
	reconciler, store := connectorReconciler(t, product, deployment)
	for _, available := range []int32{0, 2, 0, 2} {
		deployment.Status.AvailableReplicas = available
		if err := store.Status().Update(t.Context(), deployment); err != nil {
			t.Fatal(err)
		}
		reconcileConnector(t, reconciler, store, product)
		response := httptest.NewRecorder()
		registry.NewHandler(store, func(context.Context) bool { return false }).
			ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/products", nil))
		var result struct {
			Products []struct {
				Ready     bool `json:"ready"`
				Readiness struct {
					Reason string `json:"reason"`
				} `json:"readiness"`
			} `json:"products"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || len(result.Products) != 1 ||
			result.Products[0].Ready != (available == 2) {
			t.Fatalf("registry response: %s", response.Body.String())
		}
		if available == 0 && result.Products[0].Readiness.Reason != "ConnectorNotReady" {
			t.Fatalf("registry hid connector failure: %s", response.Body.String())
		}
		// Preserve an old transition time so a remove-and-recreate condition bug
		// cannot hide behind multiple observations within the same clock second.
		for index := range product.Status.Conditions {
			product.Status.Conditions[index].LastTransitionTime = metav1.NewTime(
				time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
			)
		}
		if err := store.Status().Update(t.Context(), product); err != nil {
			t.Fatal(err)
		}
		version := product.ResourceVersion
		reconcileConnector(t, reconciler, store, product)
		if product.ResourceVersion != version {
			t.Fatal("unchanged observation rewrote status")
		}
	}
	product.Spec.Connector = nil
	if err := store.Update(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	result := reconcileConnector(t, reconciler, store, product)
	if meta.FindStatusCondition(product.Status.Conditions, "ConnectorReady") != nil ||
		result.RequeueAfter != 0 ||
		readyCondition(t, product).Status != metav1.ConditionTrue {
		t.Fatalf("reference removal retained connector state: %+v / %+v", product.Status, result)
	}
	if err := store.Delete(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(
		t.Context(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(product)},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(
		t.Context(),
		client.ObjectKeyFromObject(deployment),
		&appsv1.Deployment{},
	); err != nil {
		t.Fatalf("product deletion affected workload: %v", err)
	}
}

// TestConnectorObservationContinuesDuringOtherFailures prevents unrelated blockers from hiding workload changes.
func TestConnectorObservationContinuesDuringOtherFailures(t *testing.T) {
	t.Parallel()
	for _, source := range []bool{false, true} {
		t.Run(map[bool]string{false: "dependency", true: "source"}[source], func(t *testing.T) {
			t.Parallel()
			product, deployment := connectorFixture(t)
			want := "DependencyNotFound"
			product.Spec.Inputs = []datav1alpha1.InputPort{
				{
					Name:       "upstream",
					ProductRef: datav1alpha1.ProductReference{Name: "missing", Output: "query"},
				},
			}
			if source {
				product.Spec.Source = &datav1alpha1.ProvisionedSource{}
				want = "SourceFeatureDisabled"
			}
			reconciler, store := connectorReconciler(t, product, deployment)
			for _, available := range []int32{0, 2} {
				deployment.Status.AvailableReplicas = available
				if err := store.Status().Update(t.Context(), deployment); err != nil {
					t.Fatal(err)
				}
				reconcileConnector(t, reconciler, store, product)
				connector := meta.FindStatusCondition(product.Status.Conditions, "ConnectorReady")
				if readyCondition(t, product).Reason != want || connector == nil ||
					(connector.Status == metav1.ConditionTrue) != (available == 2) {
					t.Fatalf("stale connector or bypassed blocker: %+v", product.Status)
				}
			}
		})
	}
}

// TestConnectorOpenFeatureStates requires disabled flags to prevent all workload reads.
func TestConnectorOpenFeatureStates(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			t.Parallel()
			product, deployment := connectorFixture(t)
			reconciler, store := connectorReconciler(t, product, deployment)
			flags, err := featureflag.NewClient(
				t.Name(),
				featureflag.NewProvider(map[string]bool{"connector-readiness": enabled}),
			)
			if err != nil {
				t.Fatal(err)
			}
			reconciler.ConnectorsEnabled = func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "connector-readiness") }
			if !enabled {
				reconciler.ConnectorReader = interceptor.NewClient(
					store,
					interceptor.Funcs{
						Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
							t.Fatal("disabled flag read workload")
							return nil
						},
					},
				)
			}
			reconcileConnector(t, reconciler, store, product)
			if (readyCondition(t, product).Status == metav1.ConditionTrue) != enabled {
				t.Fatalf("flag %t: %+v", enabled, product.Status)
			}
		})
	}
}

// TestConnectorStatusSurvivesDependencyReadErrors prevents stale health during a simultaneous dependency API failure.
func TestConnectorStatusSurvivesDependencyReadErrors(t *testing.T) {
	t.Parallel()
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "removed"}[remove], func(t *testing.T) {
			t.Parallel()
			product, deployment := connectorFixture(t)
			product.Spec.Inputs = []datav1alpha1.InputPort{
				{
					Name:       "upstream",
					ProductRef: datav1alpha1.ProductReference{Name: "producer", Output: "query"},
				},
			}
			producer := testProduct("producer")
			setReadiness(producer, metav1.ConditionTrue, "DependenciesReady", "Ready")
			reconciler, store := connectorReconciler(t, product, deployment, producer)
			reconcileConnector(t, reconciler, store, product)
			if readyCondition(t, product).Status != metav1.ConditionTrue {
				t.Fatalf("fixture must start ready: %+v", product.Status)
			}
			deployment.Status.AvailableReplicas = 0
			if err := store.Status().Update(t.Context(), deployment); err != nil {
				t.Fatal(err)
			}
			if remove {
				product.Spec.Connector = nil
				if err := store.Update(t.Context(), product); err != nil {
					t.Fatal(err)
				}
			}
			reconciler.Client = interceptor.NewClient(
				store,
				interceptor.Funcs{
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
				},
			)
			key := client.ObjectKeyFromObject(product)
			if _, err := reconciler.Reconcile(
				t.Context(),
				ctrl.Request{NamespacedName: key},
			); err == nil {
				t.Fatal("dependency error must remain retryable")
			}
			if err := store.Get(t.Context(), key, product); err != nil {
				t.Fatal(err)
			}
			condition := meta.FindStatusCondition(product.Status.Conditions, "ConnectorReady")
			if remove && condition != nil {
				t.Fatalf("removed connector retained status: %+v", condition)
			}
			if !remove && (condition == nil || condition.Status != metav1.ConditionFalse) {
				t.Fatalf("failed connector retained healthy status: %+v", condition)
			}
			ready := readyCondition(t, product)
			if ready.Status != metav1.ConditionFalse || ready.Reason != "DependencyUnavailable" ||
				strings.Contains(ready.Message, "sensitive sentinel") {
				t.Fatalf("dependency error retained ready status or leaked detail: %+v", ready)
			}
		})
	}
}

// connectorFixture describes a fully available two-replica workload.
func connectorFixture(t *testing.T) (*datav1alpha1.DataProduct, *appsv1.Deployment) {
	t.Helper()
	product := testProduct("export")
	product.Spec.Connector = &datav1alpha1.Connector{
		Adapter: "deployment/v1",
		ResourceRef: datav1alpha1.ConnectorResourceReference{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "export",
		},
	}
	replicas := int32(2)
	return product, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "export", Namespace: "products", Generation: 3},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 3,
			Replicas:           2,
			UpdatedReplicas:    2,
			ReadyReplicas:      2,
			AvailableReplicas:  2,
		},
	}
}

// connectorReconciler builds a test API store and enables production connector observation.
func connectorReconciler(
	t *testing.T,
	objects ...client.Object,
) (*DataProductReconciler, client.WithWatch) {
	t.Helper()
	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&datav1alpha1.DataProduct{}, &appsv1.Deployment{}).
		WithObjects(objects...).
		Build()
	return &DataProductReconciler{
		Client:            store,
		Scheme:            scheme,
		ConnectorReader:   store,
		ConnectorsEnabled: func(context.Context) bool { return true },
	}, store
}

// reconcileConnector refreshes the product through the production reconciler for lifecycle assertions.
func reconcileConnector(
	t *testing.T,
	reconciler *DataProductReconciler,
	store client.Client,
	product *datav1alpha1.DataProduct,
) ctrl.Result {
	t.Helper()
	key := client.ObjectKeyFromObject(product)
	result, err := reconciler.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Get(t.Context(), key, product); err != nil {
		t.Fatal(err)
	}
	return result
}
