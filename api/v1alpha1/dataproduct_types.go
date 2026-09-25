package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// ConditionReady reports whether a product and all referenced inputs are ready.
	ConditionReady = "Ready"
	// ConditionConnectorReady reports the last observed connector workload readiness.
	ConditionConnectorReady = "ConnectorReady"
	// ConditionContractsReady reports the selected contracts' independently observed reachability.
	ConditionContractsReady = "ContractsReady"
	// ConditionCompositionReady reports graph and declared contract compatibility.
	ConditionCompositionReady = "CompositionReady"
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

	// Contract optionally requires a stable version and protocol from the producer.
	Contract *InputContract `json:"contract,omitempty"`
}

// InputContract declares compatibility with the producer's product contract version.
type InputContract struct {
	// MinimumVersion accepts stable versions at least this new within the same major.
	// Major-zero contracts require an exact version match.
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
	MinimumVersion string `json:"minimumVersion"`
	// Protocol must match the selected output's declared contract family.
	Protocol OutputProtocol `json:"protocol"`
}

// InputStatus records a direct lineage edge observed during composition checks.
type InputStatus struct {
	// Name identifies the consuming input port.
	Name string `json:"name"`
	// ProductRef identifies the producer with an explicit namespace and output.
	ProductRef ProductReference `json:"productRef"`
	// ProductID is the producer's stable identity when it could be observed.
	ProductID string `json:"productID,omitempty"`
	// ObservedGeneration identifies the producer revision used for this edge.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Version is the producer's declared product contract version.
	Version string `json:"version,omitempty"`
	// Owner names the producer's accountable team.
	Owner *ProductOwner `json:"owner,omitempty"`
	// Output contains the selected public interface metadata, never records or credentials.
	Output *OutputPort `json:"output,omitempty"`
	// Ready reports whether this direct input satisfies its contract and producer readiness.
	Ready bool `json:"ready"`
	// Reason explains this direct input's last observation.
	Reason string `json:"reason"`
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

// ProvisionedResourceReference identifies a custom resource in the product's namespace.
type ProvisionedResourceReference struct {
	// APIVersion selects a custom-resource API group and version; core resources are not provisioners.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?/[a-z][a-z0-9]*$`
	APIVersion string `json:"apiVersion"`
	// Kind is the provisioner's namespaced custom-resource kind.
	// +kubebuilder:validation:Pattern=`^[A-Z][A-Za-z0-9]*$`
	Kind string `json:"kind"`
	// Name identifies the provisioner-owned resource.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`
}

// ConnectionSecretReference identifies credentials consumed directly by the product workload.
type ConnectionSecretReference struct {
	// Name identifies the published connection Secret in the product's namespace.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`
}

// ProvisionedSource observes a source whose creation, credentials, and deletion belong to a provisioner.
type ProvisionedSource struct {
	// Adapter selects a versioned readiness contract.
	// +kubebuilder:validation:Enum=crossplane/v1
	Adapter string `json:"adapter"`
	// ResourceRef points to the provisioner-owned resource; the controller never writes it.
	ResourceRef ProvisionedResourceReference `json:"resourceRef"`
	// ConnectionSecretRef names the connection contract. Only Secret metadata is requested.
	ConnectionSecretRef ConnectionSecretReference `json:"connectionSecretRef"`
}

// ConnectorResourceReference selects a Deployment in the product's namespace.
type ConnectorResourceReference struct {
	// APIVersion selects the supported workload API.
	// +kubebuilder:validation:Enum=apps/v1
	APIVersion string `json:"apiVersion"`
	// Kind selects the supported workload kind.
	// +kubebuilder:validation:Enum=Deployment
	Kind string `json:"kind"`
	// Name identifies the independently owned connector Deployment.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`
	// Namespace must be omitted or empty; scope is always the product's namespace.
	// +kubebuilder:validation:MaxLength=0
	Namespace string `json:"namespace,omitempty"`
}

// Connector observes an independently operated data-plane workload.
type Connector struct {
	// Adapter selects a versioned workload-readiness contract.
	// +kubebuilder:validation:Enum=deployment/v1
	Adapter string `json:"adapter"`
	// ResourceRef identifies the workload without granting lifecycle ownership.
	ResourceRef ConnectorResourceReference `json:"resourceRef"`
}

// ContractCheck binds a published output to an independently operated contract-probe Deployment.
type ContractCheck struct {
	// Output selects the product's named output and its current contractUrl.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Output string `json:"output"`
	// ResourceRef selects the same-namespace probe Deployment; no lifecycle ownership is granted.
	ResourceRef ConnectorResourceReference `json:"resourceRef"`
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

	// Source optionally requires a provisioned resource and its published connection Secret to be ready.
	Source *ProvisionedSource `json:"source,omitempty"`

	// Connector optionally requires full current-generation workload availability.
	Connector *Connector `json:"connector,omitempty"`

	// ContractChecks optionally require reachability of selected published contracts.
	// +kubebuilder:validation:MaxItems=8
	// +listType=map
	// +listMapKey=output
	ContractChecks []ContractCheck `json:"contractChecks,omitempty"`
}

// DataProductStatus reports observed composition and readiness.
type DataProductStatus struct {
	// ObservedGeneration is the generation reflected in Conditions.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions report readiness and actionable dependency failures.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Inputs records observed direct lineage; CompositionReady carries its consumer generation.
	// +listType=map
	// +listMapKey=name
	Inputs []InputStatus `json:"inputs,omitempty"`
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
