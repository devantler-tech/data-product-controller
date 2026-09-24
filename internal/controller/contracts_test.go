package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestDeclaredContractChecksFailClosedByDefault prevents an ignored opt-in check from publishing false readiness.
func TestDeclaredContractChecksFailClosedByDefault(t *testing.T) {
	t.Parallel()
	product := testProduct("export")
	if err := json.Unmarshal(
		[]byte(
			`{"contractChecks":[{"output":"query","resourceRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"contract-probe"}}]}`,
		),
		&product.Spec,
	); err != nil {
		t.Fatal(err)
	}
	reconciler, store := connectorReconciler(t, product)
	result := reconcileConnector(t, reconciler, store, product)
	condition := meta.FindStatusCondition(product.Status.Conditions, "ContractsReady")
	if condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.Reason != "ContractFeatureDisabled" ||
		readyCondition(t, product).Status != metav1.ConditionFalse ||
		result.RequeueAfter == 0 {
		t.Fatalf("declared contract checks were ignored: %+v / %+v", product.Status, result)
	}
}

// TestContractChecksRemainIndependentAndDetach exercises recovery while another source blocks readiness, then reference removal.
func TestContractChecksRemainIndependentAndDetach(t *testing.T) {
	t.Parallel()
	product, deployment := contractFixture(t)
	product.Spec.Source = &datav1alpha1.ProvisionedSource{}
	reconciler, store := connectorReconciler(t, product, deployment)
	reconciler.ContractsEnabled = func(context.Context) bool { return true }
	for _, available := range []int32{0, 2} {
		deployment.Status.AvailableReplicas = available
		if err := store.Status().Update(t.Context(), deployment); err != nil {
			t.Fatal(err)
		}
		reconcileConnector(t, reconciler, store, product)
		condition := meta.FindStatusCondition(product.Status.Conditions, "ContractsReady")
		if condition == nil || (condition.Status == metav1.ConditionTrue) != (available == 2) ||
			readyCondition(t, product).Reason != "SourceFeatureDisabled" {
			t.Fatalf("contract status did not refresh independently: %+v", product.Status)
		}
	}
	product.Spec.Source = nil
	product.Spec.ContractChecks = nil
	if err := store.Update(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	if result := reconcileConnector(
		t,
		reconciler,
		store,
		product,
	); result.RequeueAfter != 0 || meta.FindStatusCondition(product.Status.Conditions, "ContractsReady") != nil ||
		readyCondition(t, product).Status != metav1.ConditionTrue {
		t.Fatalf("removed check still affects readiness: %+v", product.Status)
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
		t.Fatalf("independent probe was affected by deletion: %v", err)
	}
}

// TestContractObservationScopeAndReleaseFlag requires exact-name, bounded reads only when OpenFeature permits them.
func TestContractObservationScopeAndReleaseFlag(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			t.Parallel()
			product, deployment := contractFixture(t)
			reconciler, store := connectorReconciler(t, product, deployment)
			flags, err := featureflag.NewClient(
				t.Name(),
				featureflag.NewProvider(map[string]bool{"contract-readiness": enabled}),
			)
			if err != nil {
				t.Fatal(err)
			}
			reconciler.ContractsEnabled = func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "contract-readiness") }
			reads := 0
			reconciler.ConnectorReader = interceptor.NewClient(
				store,
				interceptor.Funcs{
					Get: func(ctx context.Context, _ client.WithWatch, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
						reads++
						deadline, ok := ctx.Deadline()
						if !enabled || !ok || time.Until(deadline) > 5*time.Second ||
							key != client.ObjectKeyFromObject(deployment) {
							t.Fatal("unbounded, disabled, or incorrectly scoped read")
						}
						if _, ok := obj.(*appsv1.Deployment); !ok {
							t.Fatal("observer read a non-Deployment resource")
						}
						return apierrors.NewForbidden(
							appsv1.Resource("deployments"),
							key.Name,
							errors.New("sensitive sentinel"),
						)
					},
				},
			)
			reconcileConnector(t, reconciler, store, product)
			want := "ContractFeatureDisabled"
			if enabled {
				want = "ContractProbeAccessDenied"
			}
			condition := readyCondition(t, product)
			if condition.Reason != want || strings.Contains(condition.Message, "sentinel") ||
				(!enabled && reads != 0) ||
				(enabled && reads != 1) {
				t.Fatalf("incorrect access/flag behavior: %+v reads=%d", condition, reads)
			}
		})
	}
}

// TestContractCheckRejectsMisconfiguredAndStaleProbes blocks malformed bindings and partial rollouts.
func TestContractCheckRejectsMisconfiguredAndStaleProbes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*datav1alpha1.DataProduct, *appsv1.Deployment)
		reason string
	}{
		{"missing output", func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) { p.Spec.ContractChecks[0].Output = "missing" }, "ContractOutputNotFound"},
		{"cross namespace", func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.ContractChecks[0].ResourceRef.Namespace = "other"
		}, "ContractProbeInvalid"},
		{"unsupported kind", func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.ContractChecks[0].ResourceRef.Kind = "Secret"
		}, "ContractProbeInvalid"},
		{"missing deployment", func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.ContractChecks[0].ResourceRef.Name = "absent"
		}, "ContractProbeNotFound"},
		{"duplicate output", func(p *datav1alpha1.DataProduct, _ *appsv1.Deployment) {
			p.Spec.ContractChecks = append(p.Spec.ContractChecks, p.Spec.ContractChecks[0])
		}, "ContractChecksInvalid"},
		{"stale generation", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.ObservedGeneration-- }, "ContractProbeStatusStale"},
		{"old replicas", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { d.Status.UpdatedReplicas-- }, "ContractProbeNotReady"},
		{"scaled down", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) { *d.Spec.Replicas = 0 }, "ContractProbeScaledToZero"},
		{"wrong container", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].Name = "other"
		}, "ContractProbeConfigurationMismatch"},
		{"disabled execution", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].Env[1].Value = "false"
		}, "ContractProbeConfigurationMismatch"},
		{"indirect URL", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].Env[0].ValueFrom = &corev1.EnvVarSource{}
		}, "ContractProbeConfigurationMismatch"},
		{"missing readiness", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].ReadinessProbe = nil
		}, "ContractProbeConfigurationMismatch"},
		{"liveness as readiness", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Path = "/healthz"
		}, "ContractProbeConfigurationMismatch"},
		{"overridden command", func(_ *datav1alpha1.DataProduct, d *appsv1.Deployment) {
			d.Spec.Template.Spec.Containers[0].Command = []string{"/other"}
		}, "ContractProbeConfigurationMismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			product, deployment := contractFixture(t)
			tc.change(product, deployment)
			reconciler, store := connectorReconciler(t, product, deployment)
			reconciler.ContractsEnabled = func(context.Context) bool { return true }
			reconcileConnector(t, reconciler, store, product)
			if condition := readyCondition(
				t,
				product,
			); condition.Status != metav1.ConditionFalse ||
				condition.Reason != tc.reason {
				t.Fatalf("unexpected readiness: %+v", condition)
			}
		})
	}
}

// TestContractStatusSurvivesDependencyReadFailure prevents API failures from hiding a newly failed or removed check.
func TestContractStatusSurvivesDependencyReadFailure(t *testing.T) {
	t.Parallel()
	product, deployment := contractFixture(t)
	product.Spec.Inputs = []datav1alpha1.InputPort{
		{
			Name:       "upstream",
			ProductRef: datav1alpha1.ProductReference{Name: "producer", Output: "query"},
		},
	}
	producer := testProduct("producer")
	setReadiness(producer, metav1.ConditionTrue, "DependenciesReady", "Ready")
	reconciler, store := connectorReconciler(t, product, deployment, producer)
	reconciler.ContractsEnabled = func(context.Context) bool { return true }
	reconcileConnector(t, reconciler, store, product)
	deployment.Status.ReadyReplicas = 0
	if err := store.Status().Update(t.Context(), deployment); err != nil {
		t.Fatal(err)
	}
	reconciler.Client = interceptor.NewClient(
		store,
		interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if key.Name == "producer" {
					return errors.New("API unavailable")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		},
	)
	for _, remove := range []bool{false, true} {
		if remove {
			product.Spec.ContractChecks = nil
			if err := store.Update(t.Context(), product); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := reconciler.Reconcile(
			t.Context(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(product)},
		); err == nil {
			t.Fatal("dependency error must remain retryable")
		}
		if err := store.Get(t.Context(), client.ObjectKeyFromObject(product), product); err != nil {
			t.Fatal(err)
		}
		condition := meta.FindStatusCondition(product.Status.Conditions, "ContractsReady")
		if (remove && condition != nil) ||
			(!remove && (condition == nil || condition.Status != metav1.ConditionFalse)) ||
			readyCondition(t, product).Reason != "DependencyUnavailable" {
			t.Fatalf("stale independent condition: %+v", product.Status)
		}
	}
}

// TestContractURLBindingAndRecovery prevents a healthy Deployment from certifying an old or different contract.
func TestContractURLBindingAndRecovery(t *testing.T) {
	t.Parallel()
	product, deployment := contractFixture(t)
	reconciler, store := connectorReconciler(t, product, deployment)
	reconciler.ContractsEnabled = func(context.Context) bool { return true }
	for _, tc := range []struct {
		url      string
		replicas int32
		ready    bool
		reason   string
	}{
		{"https://example.com/schema", 2, true, "ContractsReady"},
		{"https://example.com/other", 2, false, "ContractProbeConfigurationMismatch"},
		{"https://example.com/schema", 0, false, "ContractProbeNotReady"},
		{"https://example.com/schema", 2, true, "ContractsReady"},
	} {
		deployment.Spec.Template.Spec.Containers[0].Env[0].Value = tc.url
		if err := store.Update(t.Context(), deployment); err != nil {
			t.Fatal(err)
		}
		deployment.Status.ReadyReplicas = tc.replicas
		if err := store.Status().Update(t.Context(), deployment); err != nil {
			t.Fatal(err)
		}
		reconcileConnector(t, reconciler, store, product)
		condition := meta.FindStatusCondition(product.Status.Conditions, "ContractsReady")
		if condition == nil || (condition.Status == metav1.ConditionTrue) != tc.ready ||
			condition.Reason != tc.reason ||
			(readyCondition(t, product).Status == metav1.ConditionTrue) != tc.ready {
			t.Fatalf("URL %s replicas %d: %+v", tc.url, tc.replicas, product.Status)
		}
	}
}

// contractFixture creates a ready probe with an explicit URL and an independent product reference.
func contractFixture(t *testing.T) (*datav1alpha1.DataProduct, *appsv1.Deployment) {
	t.Helper()
	product, deployment := connectorFixture(t)
	ref := product.Spec.Connector.ResourceRef
	product.Spec.Connector = nil
	product.Spec.Outputs[0].ContractURL = "https://example.com/schema"
	product.Spec.ContractChecks = []datav1alpha1.ContractCheck{
		{Output: product.Spec.Outputs[0].Name, ResourceRef: ref},
	}
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		{Name: "contract-probe", Command: []string{"/contract-probe"}, Env: []corev1.EnvVar{
			{
				Name:  "CONTRACT_PROBE_URL",
				Value: "https://example.com/schema",
			},
			{Name: "CONTRACT_READINESS_ENABLED", Value: "true"},
		}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt32(8081)}}}},
	}
	return product, deployment
}
