package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestConnectorUncachedHTTPObservation exercises the real Kubernetes client codec, named route, and cancellation.
func TestConnectorUncachedHTTPObservation(t *testing.T) {
	t.Parallel()
	product, deployment := connectorFixture(t)
	var mode atomic.Int32
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet ||
			r.URL.Path != "/apis/apps/v1/namespaces/products/deployments/export" ||
			r.URL.RawQuery != "" {
			t.Errorf("unexpected Kubernetes request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if mode.Load() == 3 {
			<-r.Context().Done()
			return
		}
		if mode.Load() == 2 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(
				[]byte(
					`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","message":"sensitive sentinel","code":403}`,
				),
			)
			return
		}
		workload := deployment.DeepCopy()
		workload.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
		if mode.Load() == 1 {
			workload.Status.AvailableReplicas = 0
		}
		if err := json.NewEncoder(w).Encode(workload); err != nil {
			t.Errorf("encode Deployment: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	reconciler, store := connectorReconciler(t, product)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{appsv1.SchemeGroupVersion})
	mapper.Add(appsv1.SchemeGroupVersion.WithKind("Deployment"), meta.RESTScopeNamespace)
	reader, err := client.New(
		&rest.Config{Host: server.URL},
		client.Options{Scheme: reconciler.Scheme, Mapper: mapper},
	)
	if err != nil {
		t.Fatal(err)
	}
	reconciler.ConnectorReader = reader
	for _, test := range []struct {
		mode int32
		want string
	}{{0, "ConnectorReady"}, {1, "ConnectorNotReady"}, {0, "ConnectorReady"}, {2, "ConnectorAccessDenied"}, {3, "ConnectorUnavailable"}, {0, "ConnectorReady"}} {
		mode.Store(test.mode)
		start := time.Now()
		reconcileConnector(t, reconciler, store, product)
		condition := meta.FindStatusCondition(
			product.Status.Conditions,
			datav1alpha1.ConditionConnectorReady,
		)
		if condition == nil || condition.Reason != test.want {
			t.Fatalf("HTTP mode %d: %+v", test.mode, condition)
		}
		if elapsed := time.Since(
			start,
		); test.mode == 3 &&
			(elapsed < 4*time.Second || elapsed > 8*time.Second) {
			t.Fatalf("unbounded or premature timeout: %v", elapsed)
		}
	}
	if reads.Load() != 6 {
		t.Fatalf("want a fresh GET per observation, got %d", reads.Load())
	}
}
