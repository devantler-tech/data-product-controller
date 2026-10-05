package v1

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewDeploymentReader makes exact-name uncached Deployment reads without lazy API discovery.
func NewDeploymentReader(config *rest.Config) (client.Reader, error) {
	if config == nil {
		return nil, fmt.Errorf("connector Kubernetes configuration is required")
	}
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register connector Deployment API: %w", err)
	}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{appsv1.SchemeGroupVersion})
	mapper.Add(appsv1.SchemeGroupVersion.WithKind("Deployment"), meta.RESTScopeNamespace)
	bounded := rest.CopyConfig(config)
	bounded.Timeout = 5 * time.Second
	reader, err := client.New(bounded, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, fmt.Errorf("create connector reader: %w", err)
	}
	return reader, nil
}
