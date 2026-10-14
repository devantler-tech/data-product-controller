package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestColdDeploymentReader uses the actual uncached client before any discovery or cache warming.
func TestColdDeploymentReader(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"healthy", "before", "during", "deadline"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			var reads atomic.Int32
			entered := make(chan struct{}, 1)
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet ||
						r.URL.Path != "/apis/apps/v1/namespaces/products/deployments/probe" {
						t.Errorf(
							"unexpected discovery or broader read: %s %s",
							r.Method,
							r.URL.Path,
						)
						w.WriteHeader(http.StatusNotFound)
						return
					}
					reads.Add(1)
					entered <- struct{}{}
					if boundary == "during" || boundary == "deadline" {
						<-r.Context().Done()
						return
					}
					workload := &appsv1.Deployment{}
					fixture := completedDeploymentReader{finish: func(context.Context) {}}
					if err := fixture.Get(
						r.Context(),
						client.ObjectKey{Namespace: "products", Name: "probe"},
						workload,
					); err != nil {
						t.Error(err)
						return
					}
					workload.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(workload); err != nil {
						t.Error(err)
					}
				}),
			)
			defer server.Close()
			config := &rest.Config{Host: server.URL, Timeout: time.Hour}
			reader, err := NewDeploymentReader(config)
			if err != nil {
				t.Fatal(err)
			}
			if config.Timeout != time.Hour {
				t.Fatal("mutated caller transport configuration")
			}
			observer := &Deployment{Reader: reader}
			ctx, cancel := context.WithCancel(t.Context())
			if boundary == "deadline" {
				ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
			}
			defer cancel()
			if boundary == "before" {
				cancel()
			}
			ref := data.ConnectorResourceReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "probe",
			}
			done := make(chan Observation, 1)
			go func() {
				done <- observer.Observe(ctx, "products", data.Connector{Adapter: "deployment/v1", ResourceRef: ref})
			}()
			if boundary == "during" {
				select {
				case <-entered:
					cancel()
				case <-time.After(time.Second):
					t.Fatal("exact Deployment read never started")
				}
			}
			select {
			case got := <-done:
				if got.Ready != (boundary == "healthy") {
					t.Fatalf("%s observation: %+v", boundary, got)
				}
			case <-time.After(time.Second):
				t.Fatal("cold read escaped the caller's cancellation bound")
			}
			if boundary == "before" && reads.Load() != 0 {
				t.Fatal("cancelled cold observation made a Kubernetes request")
			}
			if boundary == "healthy" {
				<-entered
				got := observer.ObserveContract(
					t.Context(),
					"products",
					ref,
					"https://example.test/schema",
				)
				if !got.Ready || reads.Load() != 2 {
					t.Fatalf(
						"contract observation was not a fresh exact read: %+v reads=%d",
						got,
						reads.Load(),
					)
				}
			}
		})
	}
	if _, err := NewDeploymentReader(nil); err == nil {
		t.Fatal("missing Kubernetes configuration was accepted")
	}
}
