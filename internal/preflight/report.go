package preflight

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// Position identifies an explicit selection and physical YAML document without filenames.
type Position struct {
	Source   int `json:"source"`
	Document int `json:"document"`
	Line     int `json:"line"`
	Column   int `json:"column"`
}

// SourceSummary counts documents, including empty documents, in one selected source.
type SourceSummary struct {
	Source    int `json:"source"`
	Documents int `json:"documents"`
	Products  int `json:"products"`
}

// WitnessStep identifies a declared dependency without submitted diagnostic values.
type WitnessStep struct {
	Source   int    `json:"source"`
	Document int    `json:"document"`
	Path     string `json:"path"`
}

// BundleDiagnostic is a fixed public finding with bounded source coordinates.
type BundleDiagnostic struct {
	Position
	Severity         string        `json:"severity"`
	Code             string        `json:"code"`
	Path             string        `json:"path"`
	Message          string        `json:"message"`
	Witness          []WitnessStep `json:"witness"`
	WitnessTruncated bool          `json:"witnessTruncated"`
}

// DiagnosticCounts includes findings omitted from the displayed diagnostic limit.
type DiagnosticCounts struct {
	Total    int `json:"total"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Omitted  int `json:"omitted"`
}

// ProductFeatures names operational requirements, never enabling any feature.
type ProductFeatures struct {
	Source           int      `json:"source"`
	Document         int      `json:"document"`
	RequiredFeatures []string `json:"requiredFeatures"`
}

// PlanProduct is a public identity and its declaration position.
type PlanProduct struct {
	Key      string `json:"key"`
	Source   int    `json:"source"`
	Document int    `json:"document"`
}

// PlanEdge describes one named declared input, not a runtime observation.
type PlanEdge struct {
	Consumer   string `json:"consumer"`
	Producer   string `json:"producer"`
	Input      string `json:"input"`
	InputIndex int    `json:"inputIndex"`
	Output     string `json:"output"`
	Source     int    `json:"source"`
	Document   int    `json:"document"`
}

// ReviewPlan is a deterministic static aid; it executes nothing and grants no access.
type ReviewPlan struct {
	Order []PlanProduct `json:"order"`
	Edges []PlanEdge    `json:"edges"`
}

// BundleReport is the explicitly selected v2 publisher report.
type BundleReport struct {
	APIVersion       string             `json:"apiVersion"`
	Valid            bool               `json:"valid"`
	Complete         bool               `json:"complete"`
	Products         int                `json:"products"`
	Sources          []SourceSummary    `json:"sources"`
	RequiredFeatures []string           `json:"requiredFeatures"`
	ProductFeatures  []ProductFeatures  `json:"productFeatures"`
	Diagnostics      []BundleDiagnostic `json:"diagnostics"`
	DiagnosticCounts DiagnosticCounts   `json:"diagnosticCounts"`
	Descriptors      []json.RawMessage  `json:"descriptors"`
	Plan             *ReviewPlan        `json:"plan"`
}

// CheckBundle checks only caller-supplied readers under one shared validation budget.
func CheckBundle(ctx context.Context, sources []io.Reader, namespace string) BundleReport {
	r := checkSelected(ctx, sources, namespace)
	return bundleReport(r)
}

func bundleReport(r Report) BundleReport {
	result := BundleReport{
		APIVersion: "data-product-preflight/v2", Valid: r.Valid, Complete: r.Complete,
		Products: r.Products, Sources: r.sources, RequiredFeatures: r.RequiredFeatures,
		ProductFeatures: r.productFeatures, Diagnostics: r.details, DiagnosticCounts: r.counts,
		Descriptors: []json.RawMessage{},
	}
	if result.Sources == nil {
		result.Sources = []SourceSummary{}
	}
	if result.ProductFeatures == nil {
		result.ProductFeatures = []ProductFeatures{}
	}
	// An interrupted engine may not finalize its legacy union. The v2 union always
	// describes exactly the retained, checked per-product requirements.
	required := map[string]bool{}
	for _, product := range result.ProductFeatures {
		for _, feature := range product.RequiredFeatures {
			required[feature] = true
		}
	}
	result.RequiredFeatures = []string{}
	for feature := range required {
		result.RequiredFeatures = append(result.RequiredFeatures, feature)
	}
	sort.Strings(result.RequiredFeatures)
	if result.Diagnostics == nil {
		result.Diagnostics = []BundleDiagnostic{}
	}
	result.Products = 0
	for _, source := range result.Sources {
		result.Products += source.Products
	}
	if r.Valid && r.Complete {
		result.Descriptors = r.Descriptors
		result.Plan = r.plan
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxInputBytes {
		r.add(0, "PreviewLimit", "")
		result.Valid = false
		result.Complete = false
		result.Descriptors = []json.RawMessage{}
		result.Plan = nil
		result.Diagnostics = r.details
		result.DiagnosticCounts = r.counts
	}
	return result
}

// CheckFiles preserves the v1 report while checking all explicitly selected readers.
func CheckFiles(ctx context.Context, sources []io.Reader, namespace string) Report {
	r := checkSelected(ctx, sources, namespace)
	// Raw declarations are evaluation state, never a public report or loggable result.
	r.documents = nil
	return r
}

func (r *Report) addDetail(
	document int,
	severity, code, path string,
	witness []WitnessStep,
	truncated bool,
) {
	if severity == "error" {
		r.Valid = false
		r.counts.Errors++
	} else {
		r.counts.Warnings++
	}
	r.Complete = false
	r.counts.Total++
	if len(r.details) >= 128 {
		r.counts.Omitted++
		return
	}
	position := r.failure
	if document > 0 && document <= len(r.documents) {
		doc := r.documents[document-1]
		position = doc.origin
		for candidate := path; candidate != ""; {
			if at, ok := doc.fields[candidate]; ok {
				position = at
				break
			}
			cut := strings.LastIndex(candidate, "/")
			if cut < 0 {
				break
			}
			candidate = candidate[:cut]
		}
	}
	if witness == nil {
		witness = []WitnessStep{}
	}
	r.details = append(
		r.details,
		BundleDiagnostic{
			Position:         position,
			Severity:         severity,
			Code:             code,
			Path:             path,
			Message:          diagnosticMessage(code),
			Witness:          witness,
			WitnessTruncated: truncated,
		},
	)
}

func (r *Report) addAt(document int, severity, code, legacyPath, path string) {
	r.appendLegacy(document, severity, code, legacyPath)
	r.addDetail(document, severity, code, path, nil, false)
}

func (r *Report) appendLegacy(document int, severity, code, path string) {
	if len(r.Diagnostics) < 128 {
		r.Diagnostics = append(
			r.Diagnostics,
			Diagnostic{
				Document: document,
				Severity: severity,
				Code:     code,
				Path:     path,
				Message:  diagnosticMessage(code),
			},
		)
	}
}

func (r *Report) addAdmission(document int, legacyPath string, findings []finding) {
	legacyCode := "AdmissionInvalid"
	if findings[0].code == "UnknownField" {
		legacyCode = "UnknownField"
	}
	r.appendLegacy(document, "error", legacyCode, legacyPath)
	for _, finding := range findings {
		r.addDetail(document, "error", finding.code, finding.path, nil, false)
	}
}
