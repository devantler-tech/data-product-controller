// Package dataspace projects public DCAT metadata and explicit provider bindings offline.
package dataspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

const (
	contextURL       = "https://w3id.org/dspace/2025/1/context.jsonld"
	maxCatalog       = 2 << 20
	maxBindings      = 1 << 20
	maxDatasets      = 256
	maxDistributions = 1024
)

var urnPattern = regexp.MustCompile(
	`^urn:[A-Za-z0-9][A-Za-z0-9-]{0,30}[A-Za-z0-9]:[A-Za-z0-9:._-]+$`,
)

// Export validates both complete inputs before returning an encoded DSP catalog.
func Export(catalog, bindings io.Reader) ([]byte, error) {
	var source sourceCatalog
	if err := decode(catalog, maxCatalog, &source); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var binding bindingsDocument
	if err := decode(bindings, maxBindings, &binding); err != nil {
		return nil, fmt.Errorf("bindings: %w", err)
	}
	index, ids, err := source.index()
	if err != nil {
		return nil, err
	}
	if binding.Version != "dsp-catalog/v1" || binding.Datasets == nil ||
		len(binding.Datasets) > maxDatasets {
		return nil, errors.New(
			"bindings require version dsp-catalog/v1 and at most 256 explicitly selected datasets",
		)
	}
	if !validID(binding.Participant) {
		return nil, errors.New("bindings require a public participant identifier")
	}
	for _, id := range []string{binding.ID, binding.Service.ID} {
		if err := claim(ids, id); err != nil {
			return nil, err
		}
	}
	if ids[binding.Participant] {
		return nil, errors.New("participant identity conflicts with a catalog resource")
	}
	ids[binding.Participant] = true
	if !publicURL(binding.Service.Endpoint, true) {
		return nil, errors.New(
			"service requires an HTTPS base URL without credentials, query or fragment",
		)
	}
	service := map[string]any{
		"@id":         binding.Service.ID,
		"@type":       "DataService",
		"endpointURL": binding.Service.Endpoint,
	}
	result := map[string]any{
		"@context":      []string{contextURL},
		"@id":           binding.ID,
		"@type":         "Catalog",
		"participantId": binding.Participant,
		"service":       []any{service},
	}
	var datasets []any
	selected := map[string]bool{}
	total := 0
	for _, b := range binding.Datasets {
		src, ok := index[b.ID]
		if !ok || selected[b.ID] {
			return nil, errors.New("binding selects a missing or repeated dataset")
		}
		selected[b.ID] = true
		total += len(b.Distributions)
		if len(b.Distributions) == 0 || total > maxDistributions {
			return nil, errors.New("select 1 to 1,024 distributions across the catalog")
		}
		if len(b.Offers) == 0 || len(b.Offers) > 16 {
			return nil, errors.New("each dataset requires 1 to 16 provider offers")
		}
		for _, offer := range b.Offers {
			if err := claim(ids, offer.ID); err != nil {
				return nil, err
			}
			if err := offer.validate(binding.Participant); err != nil {
				return nil, err
			}
		}
		outputs := map[string]bool{}
		for _, d := range src.Distributions {
			outputs[d.ID] = true
		}
		var distributions []any
		for _, d := range b.Distributions {
			if !outputs[d.SourceID] {
				return nil, errors.New("binding selects a missing or repeated source distribution")
			}
			delete(outputs, d.SourceID)
			if err := claim(ids, d.ID); err != nil {
				return nil, err
			}
			if !vocabularyIRI(d.Format) {
				return nil, errors.New(
					"transfer format must be an explicit absolute vocabulary IRI",
				)
			}
			distributions = append(
				distributions,
				map[string]any{
					"@id":           d.ID,
					"@type":         "Distribution",
					"format":        d.Format,
					"accessService": service,
				},
			)
		}
		datasets = append(
			datasets,
			map[string]any{
				"@id":             src.ID,
				"@type":           "Dataset",
				"dct:title":       src.Title,
				"dct:description": src.Description,
				"dcat:version":    src.Version,
				"hasPolicy":       b.Offers,
				"distribution":    distributions,
			},
		)
	}
	if len(datasets) > 0 {
		result["dataset"] = datasets
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode catalog: %w", err)
	}
	if len(body)+1 > maxCatalog {
		return nil, errors.New("encoded catalog exceeds 2 MiB")
	}
	return append(body, '\n'), nil
}

type bindingsDocument struct {
	Version     string `json:"version"`
	ID          string `json:"catalogId"`
	Participant string `json:"participantId"`
	Service     struct {
		ID       string `json:"id"`
		Endpoint string `json:"endpointURL"`
	} `json:"service"`
	Datasets []datasetBinding `json:"datasets"`
}

type datasetBinding struct {
	ID            string                `json:"id"`
	Distributions []distributionBinding `json:"distributions"`
	Offers        []offer               `json:"offers"`
}

type distributionBinding struct {
	SourceID string `json:"sourceId"`
	ID       string `json:"id"`
	Format   string `json:"format"`
}

type offer struct {
	ID          string `json:"@id"`
	Type        string `json:"@type"`
	Assigner    string `json:"assigner"`
	Permission  []rule `json:"permission,omitempty"`
	Prohibition []rule `json:"prohibition,omitempty"`
	Obligation  []rule `json:"obligation,omitempty"`
}

type rule struct {
	Action      string       `json:"action"`
	Constraints []constraint `json:"constraint,omitempty"`
}

type constraint struct {
	Left     string `json:"leftOperand"`
	Operator string `json:"operator"`
	Right    string `json:"rightOperand"`
}

// validate accepts only the documented target-free policy subset with its explicit assigner.
func (o offer) validate(participant string) error {
	if o.Type != "Offer" || o.Assigner != participant || len(o.Permission)+len(o.Prohibition) == 0 {
		return errors.New(
			"each offer requires type Offer, matching assigner, and permissions or prohibitions",
		)
	}
	for _, rules := range [][]rule{o.Permission, o.Prohibition, o.Obligation} {
		if rules != nil && (len(rules) == 0 || len(rules) > 32) {
			return errors.New("offer rule categories require 1 to 32 rules")
		}
		for _, r := range rules {
			if r.Action != "use" && !vocabularyIRI(r.Action) {
				return errors.New("rule action must be use or an absolute vocabulary IRI")
			}
			if r.Constraints != nil && (len(r.Constraints) == 0 || len(r.Constraints) > 16) {
				return errors.New("rules support 1 to 16 atomic constraints when present")
			}
			for _, c := range r.Constraints {
				if !vocabularyIRI(c.Left) || c.Right == "" ||
					!slices.Contains(
						[]string{
							"eq",
							"gt",
							"gteq",
							"lt",
							"lteq",
							"neq",
							"isA",
							"hasPart",
							"isPartOf",
							"isAllOf",
							"isAnyOf",
							"isNoneOf",
						},
						c.Operator,
					) {
					return errors.New(
						"unsupported atomic constraint; provide an absolute left operand, standard operator and nonempty string right operand",
					)
				}
			}
		}
	}
	return nil
}

// claim rejects invalid or reused identities across the source and exported resources.
func claim(ids map[string]bool, id string) error {
	if !validID(id) || ids[id] {
		return errors.New("invalid or conflicting resource identity")
	}
	ids[id] = true
	return nil
}

// validID implements the catalog's restricted HTTPS and URN identity profile.
func validID(s string) bool {
	return len(s) <= 2048 && (urnPattern.MatchString(s) || publicURL(s, false))
}

// publicURL validates public HTTPS IRIs, with stricter rules for connector base addresses.
func publicURL(s string, base bool) bool {
	if strings.ContainsAny(s, "<>\"{}|^`\\") ||
		strings.ContainsFunc(
			s,
			func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) },
		) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		strings.ContainsAny(s, " \t\r\n\\") {
		return false
	}
	if strings.ContainsAny(u.RawPath+u.RawQuery+u.RawFragment, "[]") {
		return false
	}
	if base {
		if strings.HasSuffix(u.Host, ":") {
			return false
		}
		if port := u.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return false
			}
		}
	}
	return !base ||
		(u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(s, "#"))
}

// Vocabulary terms can use the HTTP namespaces defined by DCAT and ODRL. They
// identify concepts only; nothing resolves these IRIs or contacts their hosts.
func vocabularyIRI(s string) bool {
	if urnPattern.MatchString(s) {
		return true
	}
	return publicURL(s, false) ||
		(strings.HasPrefix(s, "http://") && publicURL("https://"+strings.TrimPrefix(s, "http://"), false))
}

// decode enforces byte, token and exact field-name limits before typed decoding.
func decode(r io.Reader, limit int64, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return errors.New("unable to read input")
	}
	if int64(len(b)) > limit {
		return errors.New("input exceeds byte limit")
	}
	if err := checkJSON(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return errors.New("input has unsupported fields or value types")
	}
	return nil
}
