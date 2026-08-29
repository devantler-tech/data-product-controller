// Package v1alpha1 contains the first public data-product API.
//
// +kubebuilder:object:generate=true
// +groupName=data.devantler.tech
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion identifies the data-product API group and version.
	GroupVersion = schema.GroupVersion{Group: "data.devantler.tech", Version: "v1alpha1"}

	// SchemeBuilder registers this package's API types.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme registers this package's API types with a runtime scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(target *runtime.Scheme) error {
	target.AddKnownTypes(GroupVersion, &DataProduct{}, &DataProductList{})
	metav1.AddToGroupVersion(target, GroupVersion)

	return nil
}
