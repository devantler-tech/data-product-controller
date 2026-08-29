// Package v1alpha1 contains the first public data-product API.
//
// +kubebuilder:object:generate=true
// +groupName=data.devantler.tech
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion identifies the data-product API group and version.
	GroupVersion = schema.GroupVersion{Group: "data.devantler.tech", Version: "v1alpha1"}

	// SchemeBuilder registers this package's API types.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme registers this package's API types with a runtime scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
