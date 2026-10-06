package v1

import (
	"context"
	"fmt"
	"testing"
	"time"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type completedDeploymentReader struct{ finish func(context.Context) }

// Get completes a healthy snapshot after invoking the controlled cancellation boundary.
func (r completedDeploymentReader) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	_ ...client.GetOption,
) error {
	r.finish(ctx)
	deployment, ok := object.(*appsv1.Deployment)
	if !ok {
		return fmt.Errorf("unexpected Deployment read type %T", object)
	}
	*deployment = appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{
					Name:    "contract-probe",
					Command: []string{"/contract-probe"},
					Env: []corev1.EnvVar{
						{Name: "CONTRACT_PROBE_URL", Value: "https://example.test/schema"},
						{Name: "CONTRACT_READINESS_ENABLED", Value: "true"},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/readyz",
								Port: intstr.FromInt32(8081),
							},
						},
					},
				},
			}}},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			AvailableReplicas:  1,
		},
	}
	return nil
}

// List rejects broad reads so the fixture exercises only the declared Deployment.
func (completedDeploymentReader) List(
	context.Context,
	client.ObjectList,
	...client.ListOption,
) error {
	panic("unexpected list")
}

// TestCompletedDeploymentReadHonorsContext checks both public observation entrypoints.
func TestCompletedDeploymentReadHonorsContext(t *testing.T) {
	t.Parallel()
	for _, contract := range []bool{false, true} {
		for _, boundary := range []string{"healthy", "cancelled", "deadline"} {
			t.Run(
				boundary+map[bool]string{false: "Connector", true: "Contract"}[contract],
				func(t *testing.T) {
					t.Parallel()
					ctx, cancel := context.WithCancel(t.Context())
					if boundary == "deadline" {
						ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
					}
					defer cancel()
					observer := Deployment{
						Reader: completedDeploymentReader{
							finish: func(readContext context.Context) {
								if boundary == "cancelled" {
									cancel()
								}
								if boundary == "deadline" {
									<-readContext.Done()
								}
							},
						},
					}
					ref := data.ConnectorResourceReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "probe",
					}
					var got Observation
					prefix := "Connector"
					if contract {
						got = observer.ObserveContract(
							ctx,
							"products",
							ref,
							"https://example.test/schema",
						)
						prefix = "ContractProbe"
					} else {
						got = observer.Observe(
							ctx,
							"products",
							data.Connector{Adapter: "deployment/v1", ResourceRef: ref},
						)
					}
					want := prefix + "Unavailable"
					if boundary == "healthy" {
						want = prefix + "Ready"
					}
					if got.Ready != (boundary == "healthy") || got.Reason != want {
						t.Fatalf("completed %s observation: %+v, want %s", boundary, got, want)
					}
				},
			)
		}
	}
}
