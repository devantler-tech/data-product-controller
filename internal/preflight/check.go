// Package preflight checks publisher-selected local metadata without contacting a cluster.
package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
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
	documents        []document
	sources          []SourceSummary
	details          []BundleDiagnostic
	counts           DiagnosticCounts
	failure          Position
	productFeatures  []ProductFeatures
	plan             *ReviewPlan
}

// Check is the offline publisher boundary.
func Check(ctx context.Context, in io.Reader, namespace string) Report {
	return CheckFiles(ctx, []io.Reader{in}, namespace)
}

// checkSelected shares admission, projection and dependency budgets across the entire selection.
func checkSelected(ctx context.Context, sources []io.Reader, namespace string) Report {
	report := Report{
		APIVersion: "data-product-preflight/v1", Valid: true, Complete: true,
		RequiredFeatures: []string{}, Diagnostics: []Diagnostic{},
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		report.add(0, "ValidationLimit", "")
		return report
	}
	if namespace != "" && len(validation.IsDNS1123Label(namespace)) != 0 {
		report.add(0, "InvalidNamespace", "metadata.namespace")
		return report
	}
	documents, summaries, code, number, failure := readSelected(ctx, sources)
	report.sources = summaries
	report.failure = failure
	if code != "" {
		report.add(number, code, "")
		return report
	}
	report.documents = documents
	report.Products = len(documents)
	validator, err := loadAdmission()
	if err != nil {
		report.add(0, "ValidationUnavailable", "")
		return report
	}
	budget := int64(celconfig.RuntimeCELCostBudget)
	names, ids := make(map[string]bool), make(map[string]bool)
	required := make(map[string]bool)
	for index := range documents {
		if ctx.Err() != nil {
			report.add(index+1, "ValidationLimit", "")
			return report
		}
		doc := &documents[index]
		product := &doc.product
		if product.Namespace == "" {
			product.Namespace = namespace
		}
		if product.Namespace == "" {
			report.add(index+1, "NamespaceRequired", "metadata.namespace")
			continue
		}
		if len(validation.IsDNS1123Label(product.Namespace)) != 0 ||
			len(validation.IsDNS1123Subdomain(product.Name)) != 0 {
			report.add(index+1, "AdmissionInvalid", "metadata")
			continue
		}
		metadataErrors := apivalidation.ValidateObjectMeta(
			&product.ObjectMeta,
			true,
			apivalidation.NameIsDNSSubdomain,
			field.NewPath("metadata"),
		)
		if len(metadataErrors) != 0 {
			report.addAdmission(
				index+1,
				"metadata",
				admissionFindings(metadataErrors, validator.schema, "InvalidField"),
			)
			continue
		}
		// The local command validates declarations, never an imported observation.
		product.Status = data.DataProductStatus{}
		product.Generation = 0
		metadata, ok := doc.object["metadata"].(map[string]any)
		if !ok {
			report.add(index+1, "AdmissionInvalid", "metadata")
			continue
		}
		metadata["namespace"] = product.Namespace
		delete(doc.object, "status")
		findings, remaining := validator.validate(ctx, doc.object, budget)
		budget = remaining
		if len(findings) > 0 {
			report.addAdmission(index+1, "spec", findings)
			continue
		}
		key := product.Namespace + "/" + product.Name
		if names[key] || ids[product.Spec.ID] {
			report.add(index+1, "IdentityConflict", "metadata")
			continue
		}
		names[key], ids[product.Spec.ID] = true, true
		if product.Spec.UI != nil && !registry.ValidPublicationUI(*product.Spec.UI) {
			report.add(index+1, "InvalidUI", "spec.ui")
			continue
		}
		if _, err := registry.PublicationPreview(product); err != nil {
			report.add(index+1, "InvalidPublicMetadata", "spec")
			continue
		}
		observeDeclarations(&report, index+1, product, required)
		features := declaredFeatures(product)
		report.productFeatures = append(
			report.productFeatures,
			ProductFeatures{
				Source:           doc.origin.Source,
				Document:         doc.origin.Document,
				RequiredFeatures: features,
			},
		)
	}
	for feature := range required {
		report.RequiredFeatures = append(report.RequiredFeatures, feature)
	}
	sort.Strings(report.RequiredFeatures)
	if report.Valid {
		checkGraph(ctx, &report, documents)
	}
	if report.Valid && report.Complete {
		report.plan = reviewPlan(documents)
		for index := range documents {
			encoded, err := registry.PublicationPreview(&documents[index].product)
			if err != nil {
				report.add(index+1, "InvalidPublicMetadata", "spec")
				report.Descriptors = nil
				break
			}
			report.Descriptors = append(report.Descriptors, json.RawMessage(encoded))
		}
		encoded, err := json.Marshal(report)
		if err != nil || len(encoded) > maxReportJSONBytes {
			report.Descriptors = nil
			report.add(0, "PreviewLimit", "")
		}
	}
	return report
}

// add bounds diagnostic volume and uses only fixed public messages and known paths.
func (r *Report) add(document int, code, path string) {
	pointer := ""
	if path != "" {
		pointer = "/" + strings.ReplaceAll(path, ".", "/")
	}
	r.addAt(document, "error", code, path, pointer)
}

// observeDeclarations collects deployment requirements and checks declared contract output names.
func observeDeclarations(
	report *Report,
	number int,
	product *data.DataProduct,
	required map[string]bool,
) {
	for _, feature := range declaredFeatures(product) {
		required[feature] = true
	}
	if len(product.Spec.ContractChecks) != 0 {
		outputs := make(map[string]bool)
		for _, output := range product.Spec.Outputs {
			outputs[output.Name] = true
		}
		for index, check := range product.Spec.ContractChecks {
			if !outputs[check.Output] {
				report.addAt(
					number,
					"error",
					"ContractOutputNotFound",
					"spec.contractChecks",
					fmt.Sprintf("/spec/contractChecks/%d/output", index),
				)
			}
		}
	}
}

// declaredFeatures returns sorted gate requirements without enabling them or consulting live state.
func declaredFeatures(product *data.DataProduct) []string {
	required := map[string]bool{}
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
	result := []string{}
	for feature := range required {
		result = append(result, feature)
	}
	sort.Strings(result)
	return result
}

// diagnosticMessage maps internal finding codes to fixed text that contains no submitted values.
func diagnosticMessage(code string) string {
	messages := map[string]string{
		"NoProducts":             "Provide at least one DataProduct document.",
		"SourceLimit":            "Select at most 32 local input files.",
		"DocumentLimit":          "The selection exceeds 4096 physical documents.",
		"ReadFailed":             "The selected input could not be read.",
		"InputLimit":             "The selected inputs exceed 2 MiB in total.",
		"ProductLimit":           "The bundle exceeds 256 products.",
		"DepthLimit":             "The document exceeds 64 nesting levels.",
		"InvalidDocument":        "Use unambiguous YAML or JSON without aliases, duplicate keys or trailing content.",
		"UnsupportedResource":    "Use data.devantler.tech/v1alpha1 DataProduct documents only.",
		"UnknownField":           "Remove fields outside the delivered DataProduct API.",
		"AdmissionInvalid":       "The declaration does not satisfy the delivered API schema or cross-field rules.",
		"InvalidField":           "The field does not satisfy the delivered API schema.",
		"RequiredField":          "Provide this required field.",
		"DuplicateListItem":      "List items must have unique declared identities.",
		"CrossFieldInvalid":      "The declaration does not satisfy the delivered cross-field rules.",
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
