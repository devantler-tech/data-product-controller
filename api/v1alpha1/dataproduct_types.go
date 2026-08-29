package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// ConditionReady reports whether a product and all referenced inputs are ready.
	ConditionReady = "Ready"
)

// ProductOwner identifies the team accountable for a data product.
type ProductOwner struct {
	// Name is the owner or team display name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// URL points to current ownership or support information.
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	URL string `json:"url,omitempty"`
}

// ProductReference selects an output port on another DataProduct.
type ProductReference struct {
	// Name is the referenced DataProduct name.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Namespace defaults to the consuming product's namespace.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace string `json:"namespace,omitempty"`

	// Output names the referenced product's output port.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Output string `json:"output"`
}

// InputPort declares a composed input from another data product.
type InputPort struct {
	// Name is unique within the consuming product.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// ProductRef selects the producing data product and output port.
	ProductRef ProductReference `json:"productRef"`
}

// OutputProtocol identifies the machine-readable contract published by an output.
// +kubebuilder:validation:Enum=OpenAPI;AsyncAPI;GraphQL;DCAT;ArrowFlight
type OutputProtocol string

const (
	// ProtocolOpenAPI describes an HTTP API using OpenAPI.
	ProtocolOpenAPI OutputProtocol = "OpenAPI"
	// ProtocolAsyncAPI describes an event interface using AsyncAPI.
	ProtocolAsyncAPI OutputProtocol = "AsyncAPI"
	// ProtocolGraphQL describes a GraphQL endpoint.
	ProtocolGraphQL OutputProtocol = "GraphQL"
	// ProtocolDCAT describes a W3C DCAT catalog or data service.
	ProtocolDCAT OutputProtocol = "DCAT"
	// ProtocolArrowFlight describes an Apache Arrow Flight endpoint.
	ProtocolArrowFlight OutputProtocol = "ArrowFlight"
)

// OutputPort publishes one stable, machine-readable data interface.
type OutputPort struct {
	// Name is unique within the product and is used by composed inputs.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Protocol selects the contract family used by this interface.
	Protocol OutputProtocol `json:"protocol"`

	// URL is the HTTPS runtime endpoint consumers use.
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	URL string `json:"url"`

	// ContractURL is an HTTPS machine-readable OpenAPI, AsyncAPI, GraphQL schema,
	// DCAT document, or Arrow Flight service description.
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	ContractURL string `json:"contractUrl"`

	// MediaType documents the primary response or message representation.
	MediaType string `json:"mediaType,omitempty"`
}

// ProductUI points to an independently deployed product interaction surface.
type ProductUI struct {
	// URL is loaded by compatible hosts in a restricted sandbox.
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	URL string `json:"url"`

	// Title is the accessible label hosts present for the embedded surface.
	// +kubebuilder:validation:MinLength=1
	Title string `json:"title"`
}

// DataProductSpec defines a self-describing and composable data product.
type DataProductSpec struct {
	// ID is a stable URI for the product across clusters and deployments.
	// +kubebuilder:validation:Pattern=`^(https://[^[:space:]]+|urn:[A-Za-z0-9][A-Za-z0-9:._-]+)$`
	ID string `json:"id"`

	// Name is the human-readable product name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Description states what data capability the product provides.
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Version is the product contract's semantic version.
	// +kubebuilder:validation:Pattern=`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`
	Version string `json:"version"`

	// Owner is accountable for availability, quality, and access decisions.
	Owner ProductOwner `json:"owner"`

	// DocumentationURL points to current product documentation.
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	DocumentationURL string `json:"documentationUrl,omitempty"`

	// Inputs compose named output ports from other data products.
	// +listType=map
	// +listMapKey=name
	Inputs []InputPort `json:"inputs,omitempty"`

	// Outputs are the product's published data interfaces.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Outputs []OutputPort `json:"outputs"`

	// UI is an optional independently deployed product interaction surface.
	UI *ProductUI `json:"ui,omitempty"`
}

// DataProductStatus reports observed composition and readiness.
type DataProductStatus struct {
	// ObservedGeneration is the generation reflected in Conditions.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions report readiness and actionable dependency failures.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DataProduct is the control-plane description of one independently operated data capability.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type DataProduct struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DataProductSpec   `json:"spec,omitempty"`
	Status DataProductStatus `json:"status,omitempty"`
}

// DataProductList contains DataProduct resources.
//
// +kubebuilder:object:root=true
type DataProductList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []DataProduct `json:"items"`
}
