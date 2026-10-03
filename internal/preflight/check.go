// Package preflight checks publisher-selected local metadata without contacting a cluster.
package preflight

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"time"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
)

// Diagnostic contains a bounded code and known field path, never submitted values.
type Diagnostic struct {
	Document int    `json:"document"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// Report describes static declarations, never live readiness or permission.
type Report struct {
	APIVersion       string            `json:"apiVersion"`
	Valid            bool              `json:"valid"`
	Complete         bool              `json:"complete"`
	Products         int               `json:"products"`
	RequiredFeatures []string          `json:"requiredFeatures"`
	Diagnostics      []Diagnostic      `json:"diagnostics"`
	Descriptors      []json.RawMessage `json:"descriptors,omitempty"`
}

// Check is the offline publisher boundary.
func Check(ctx context.Context, in io.Reader, namespace string) Report {
	report := Report{
		APIVersion: "data-product-preflight/v1", Valid: true, Complete: true,
		RequiredFeatures: []string{}, Diagnostics: []Diagnostic{},
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		report.add(0, "error", "ValidationLimit", "")
		return report
	}
	if namespace != "" && len(validation.IsDNS1123Label(namespace)) != 0 {
		report.add(0, "error", "InvalidNamespace", "metadata.namespace")
		return report
	}
	documents, code, number := readDocuments(in)
	if code != "" {
		report.add(number, "error", code, "")
		return report
	}
	report.Products = len(documents)
	validator, err := loadAdmission()
	if err != nil {
		report.add(0, "error", "ValidationUnavailable", "")
		return report
	}
	budget := int64(celconfig.RuntimeCELCostBudget)
	names, ids := make(map[string]bool), make(map[string]bool)
	required := make(map[string]bool)
	for index := range documents {
		if ctx.Err() != nil {
			report.add(index+1, "error", "ValidationLimit", "")
			return report
		}
		doc := &documents[index]
		product := &doc.product
		if product.Namespace == "" {
			product.Namespace = namespace
		}
		if product.Namespace == "" {
			report.add(index+1, "error", "NamespaceRequired", "metadata.namespace")
			continue
		}
		if len(validation.IsDNS1123Label(product.Namespace)) != 0 ||
			len(validation.IsDNS1123Subdomain(product.Name)) != 0 {
			report.add(index+1, "error", "AdmissionInvalid", "metadata")
			continue
		}
		if len(
			apivalidation.ValidateObjectMeta(
				&product.ObjectMeta,
				true,
				apivalidation.NameIsDNSSubdomain,
				field.NewPath("metadata"),
			),
		) != 0 {
			report.add(index+1, "error", "AdmissionInvalid", "metadata")
			continue
		}
		// The local command validates declarations, never an imported observation.
		product.Status = data.DataProductStatus{}
		product.Generation = 0
		metadata, ok := doc.object["metadata"].(map[string]any)
		if !ok {
			report.add(index+1, "error", "AdmissionInvalid", "metadata")
			continue
		}
		metadata["namespace"] = product.Namespace
		delete(doc.object, "status")
		code, budget = validator.validate(ctx, doc.object, budget)
		if code != "" {
			report.add(index+1, "error", code, "spec")
			continue
		}
		key := product.Namespace + "/" + product.Name
		if names[key] || ids[product.Spec.ID] {
			report.add(index+1, "error", "IdentityConflict", "metadata")
			continue
		}
		names[key], ids[product.Spec.ID] = true, true
		if product.Spec.UI != nil && !registry.ValidPublicationUI(*product.Spec.UI) {
			report.add(index+1, "error", "InvalidUI", "spec.ui")
			continue
		}
		if _, err := registry.PublicationPreview(product); err != nil {
			report.add(index+1, "error", "InvalidPublicMetadata", "spec")
			continue
		}
		observeDeclarations(&report, index+1, product, required)
	}
	for feature := range required {
		report.RequiredFeatures = append(report.RequiredFeatures, feature)
	}
	sort.Strings(report.RequiredFeatures)
	if report.Valid {
		checkGraph(ctx, &report, documents)
	}
	if report.Valid && report.Complete {
		for index := range documents {
			encoded, err := registry.PublicationPreview(&documents[index].product)
			if err != nil {
				report.add(index+1, "error", "InvalidPublicMetadata", "spec")
				report.Descriptors = nil
				break
			}
			report.Descriptors = append(report.Descriptors, json.RawMessage(encoded))
		}
		encoded, err := json.Marshal(report)
		if err != nil || len(encoded) > 2<<20 {
			report.Descriptors = nil
			report.add(0, "error", "PreviewLimit", "")
		}
	}
	return report
}

// add bounds diagnostic volume and uses only fixed public messages and known paths.
func (r *Report) add(document int, severity, code, path string) {
	if severity == "error" {
		r.Valid = false
	}
	r.Complete = false
	if len(r.Diagnostics) >= 128 {
		return
	}
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Document: document, Severity: severity, Code: code, Path: path,
		Message: diagnosticMessage(code),
	})
}

func observeDeclarations(
	report *Report,
	number int,
	product *data.DataProduct,
	required map[string]bool,
) {
	if product.Spec.Source != nil {
		required["provisioned-sources"] = true
		if product.Spec.Source.Engine != nil {
			required["engine-providers"] = true
		}
	}
	if product.Spec.Connector != nil {
		required["connector-readiness"] = true
	}
	if len(product.Spec.Inputs) != 0 {
		required["composition"] = true
	}
	if len(product.Spec.ContractChecks) != 0 {
		required["contract-readiness"] = true
		outputs := make(map[string]bool)
		for _, output := range product.Spec.Outputs {
			outputs[output.Name] = true
		}
		for _, check := range product.Spec.ContractChecks {
			if !outputs[check.Output] {
				report.add(number, "error", "ContractOutputNotFound", "spec.contractChecks")
			}
		}
	}
	if product.Spec.UI != nil && product.Spec.UI.Contract != nil {
		required["ui-contract"] = true
		for _, capability := range product.Spec.UI.Contract.Capabilities {
			if capability == "appearance" {
				required["ui-appearance"] = true
			}
		}
	}
	if product.Annotations["data.devantler.tech/dcat-type"] == "Dataset" {
		required["dcat-catalog"] = true
	}
}

func diagnosticMessage(code string) string {
	messages := map[string]string{
		"NoProducts":             "Provide at least one DataProduct document.",
		"ReadFailed":             "The selected input could not be read.",
		"InputLimit":             "The selected input exceeds 2 MiB.",
		"ProductLimit":           "The bundle exceeds 256 products.",
		"DepthLimit":             "The document exceeds 64 nesting levels.",
		"InvalidDocument":        "Use unambiguous YAML or JSON without aliases, duplicate keys or trailing content.",
		"UnsupportedResource":    "Use data.devantler.tech/v1alpha1 DataProduct documents only.",
		"UnknownField":           "Remove fields outside the delivered DataProduct API.",
		"AdmissionInvalid":       "The declaration does not satisfy the delivered API schema or cross-field rules.",
		"NamespaceRequired":      "Set metadata.namespace or explicitly select --namespace.",
		"InvalidNamespace":       "Select a canonical Kubernetes namespace.",
		"IdentityConflict":       "Each namespace/name and stable product ID must be unique in this bundle.",
		"InvalidPublicMetadata":  "Public metadata must satisfy the descriptor URI, field and encoded limits.",
		"InvalidUI":              "The UI declaration must satisfy the portable host, title and capability profile.",
		"ContractOutputNotFound": "Contract checks must select a declared output.",
		"ProducerUnresolved":     "The producer is absent from this local bundle; its live existence and readiness are unverified.",
		"OutputNotFound":         "The supplied producer does not declare the selected output.",
		"ContractIncompatible":   "The supplied output does not satisfy the declared protocol and stable version requirement.",
		"CrossNamespaceInput":    "Composition is limited to the consuming product's namespace.",
		"CompositionCycle":       "The supplied products form a composition cycle.",
		"CompositionLimit":       "The bundle exceeds the composition input or depth budget.",
		"ValidationLimit":        "Validation was canceled or exceeded its time or cost budget.",
		"ValidationUnavailable":  "The embedded admission validator could not be initialized.",
		"PreviewLimit":           "The complete public report exceeds 2 MiB; select a smaller bundle.",
	}
	return messages[code]
}
