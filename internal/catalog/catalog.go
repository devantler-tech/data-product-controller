// Package catalog projects explicitly published dataset metadata as DCAT 3 JSON-LD.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	datav1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	profileAnnotation = "data.devantler.tech/dcat-type"
	maxProducts       = 256
	pageSize          = 16
	maxOutputs        = 1024
	maxMetadata       = 1 << 20
	maxResponse       = 2 << 20
)

var urnPattern = regexp.MustCompile(
	`^urn:[A-Za-z0-9][A-Za-z0-9-]{0,30}[A-Za-z0-9]:[A-Za-z0-9:._-]+$`,
)

// Options binds the deployment's stable catalog identity and default-off release gate.
type Options struct {
	ID      string
	Enabled func(context.Context) bool
}

// NewHandler constructs a read-only catalog endpoint.
func NewHandler(reader client.Reader, options Options) (http.Handler, error) {
	if options.ID != "" && !validIRI(options.ID) {
		return nil, errors.New(
			"DCAT_CATALOG_ID must be an absolute HTTPS or URN identifier without credentials or whitespace",
		)
	}
	active := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if options.Enabled == nil || !options.Enabled(r.Context()) {
			http.NotFound(w, r)
			return
		}
		if options.ID == "" {
			http.Error(
				w,
				"Configure DCAT_CATALOG_ID before enabling the catalog.",
				http.StatusServiceUnavailable,
			)
			return
		}
		select {
		case active <- struct{}{}:
			defer func() { <-active }()
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Catalog is busy. Retry shortly.", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		products, status, err := readProducts(ctx, reader)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		document, err := project(options.ID, products)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		body, err := json.Marshal(document)
		if err != nil || len(body)+1 > maxResponse {
			http.Error(
				w,
				"Catalog exceeds the 2-MiB response limit; no partial catalog is returned.",
				http.StatusRequestEntityTooLarge,
			)
			return
		}
		w.Header().Set("Content-Type", "application/ld+json")
		// #nosec G705 -- json.Marshal HTML-escapes this immutable JSON document; the exact
		// checked bytes are served as application/ld+json with nosniff, never interpreted as HTML.
		_, _ = w.Write(append(body, '\n'))
	}), nil
}

type reference struct {
	ID string `json:"@id"`
}

// readProducts discards private fields after each small API page. A failed later page never
// publishes an incomplete snapshot, and repeated continuation tokens cannot keep a request alive.
func readProducts(
	ctx context.Context,
	reader client.Reader,
) ([]datav1.DataProductSpec, int, error) {
	var products []datav1.DataProductSpec
	continuation := ""
	scanned, metadata, outputs := 0, 0, 0
	for page := 0; page <= maxProducts/pageSize; page++ {
		list := &datav1.DataProductList{}
		limit := min(pageSize, maxProducts-scanned+1)
		if err := reader.List(
			ctx,
			list,
			client.Limit(int64(limit)),
			client.Continue(continuation),
		); err != nil ||
			ctx.Err() != nil {
			return nil, http.StatusServiceUnavailable, errors.New(
				"unable to read the catalog. Retry after the product API recovers",
			)
		}
		scanned += len(list.Items)
		if len(list.Items) > limit || scanned > maxProducts {
			return nil, http.StatusRequestEntityTooLarge, errors.New(
				"catalog exceeds the 256-product scan limit; no partial catalog is returned",
			)
		}
		for _, product := range list.Items {
			profile, declared := product.Annotations[profileAnnotation]
			if !declared {
				continue
			}
			if profile != "Dataset" {
				return nil, http.StatusUnprocessableEntity, errors.New(
					"catalog profile must be Dataset; remove the annotation to exclude a product",
				)
			}
			spec := product.Spec
			if err := validateMetadata(spec, &metadata); err != nil {
				return nil, http.StatusUnprocessableEntity, err
			}
			outputs += len(spec.Outputs)
			if outputs > maxOutputs {
				return nil, http.StatusUnprocessableEntity, errors.New(
					"catalog exceeds the 1,024-output limit",
				)
			}
			products = append(products, datav1.DataProductSpec{
				ID:               spec.ID,
				Name:             spec.Name,
				Description:      spec.Description,
				Version:          spec.Version,
				Owner:            spec.Owner,
				DocumentationURL: spec.DocumentationURL,
				Outputs:          slices.Clone(spec.Outputs),
			})
		}
		if list.Continue == "" {
			return products, 0, nil
		}
		continuation = list.Continue
	}
	return nil, http.StatusRequestEntityTooLarge, errors.New(
		"catalog exceeds the bounded page scan; no partial catalog is returned",
	)
}

type catalogDocument struct {
	Context  map[string]string `json:"@context"`
	ID       string            `json:"@id"`
	Type     string            `json:"@type"`
	Datasets []dataset         `json:"dcat:dataset"`
	Services []service         `json:"dcat:service"`
}

type dataset struct {
	ID            string         `json:"@id"`
	Type          []string       `json:"@type"`
	Identifier    string         `json:"dcterms:identifier"`
	Title         string         `json:"dcterms:title"`
	Description   string         `json:"dcterms:description"`
	Version       string         `json:"dcat:version"`
	Publisher     publisher      `json:"dcterms:publisher"`
	LandingPage   *reference     `json:"dcat:landingPage,omitempty"`
	Distributions []distribution `json:"dcat:distribution"`
}

type publisher struct {
	Type string     `json:"@type"`
	Name string     `json:"foaf:name"`
	Page *reference `json:"foaf:page,omitempty"`
}

type distribution struct {
	ID            string    `json:"@id"`
	Type          string    `json:"@type"`
	Title         string    `json:"dcterms:title"`
	AccessURL     reference `json:"dcat:accessURL"`
	AccessService reference `json:"dcat:accessService"`
	Format        string    `json:"dcterms:format,omitempty"`
}

type service struct {
	ID       string    `json:"@id"`
	Type     []string  `json:"@type"`
	Title    string    `json:"dcterms:title"`
	Endpoint reference `json:"dcat:endpointURL"`
	Contract reference `json:"dcat:endpointDescription"`
	Dataset  reference `json:"dcat:servesDataset"`
}

// project copies only public descriptor fields and rejects any ambiguous graph as a whole.
func project(id string, products []datav1.DataProductSpec) (catalogDocument, error) {
	document := catalogDocument{
		Context: map[string]string{
			"dcat":    "http://www.w3.org/ns/dcat#",
			"dcterms": "http://purl.org/dc/terms/",
			"foaf":    "http://xmlns.com/foaf/0.1/",
		},
		ID:       id,
		Type:     "dcat:Catalog",
		Datasets: []dataset{},
		Services: []service{},
	}
	slices.SortFunc(
		products,
		func(a, b datav1.DataProductSpec) int { return strings.Compare(a.ID, b.ID) },
	)
	identities := map[string]bool{id: true}
	for _, spec := range products {
		if !validIRI(spec.ID) || identities[spec.ID] {
			return document, errors.New(
				"catalog contains an invalid or conflicting product identity",
			)
		}
		identities[spec.ID] = true
		entry := dataset{
			ID:          spec.ID,
			Identifier:  spec.ID,
			Type:        []string{"dcat:Dataset", "dcat:Resource"},
			Title:       spec.Name,
			Description: spec.Description,
			Version:     spec.Version,
			Publisher: publisher{
				Type: "foaf:Agent",
				Name: spec.Owner.Name,
				Page: optionalReference(spec.Owner.URL),
			},
			LandingPage:   optionalReference(spec.DocumentationURL),
			Distributions: []distribution{},
		}
		ports := slices.Clone(spec.Outputs)
		slices.SortFunc(
			ports,
			func(a, b datav1.OutputPort) int { return strings.Compare(a.Name, b.Name) },
		)
		for _, port := range ports {
			distributionID := entityID("distribution", spec.ID, port.Name)
			serviceID := entityID("service", spec.ID, port.Name)
			if identities[distributionID] || identities[serviceID] {
				return document, errors.New("catalog contains conflicting output identities")
			}
			identities[distributionID], identities[serviceID] = true, true
			entry.Distributions = append(entry.Distributions, distribution{
				ID:    distributionID,
				Type:  "dcat:Distribution",
				Title: port.Name,
				AccessURL: reference{
					port.URL,
				},
				AccessService: reference{serviceID},
				Format:        port.MediaType,
			})
			document.Services = append(document.Services, service{
				ID:    serviceID,
				Type:  []string{"dcat:DataService", "dcat:Resource"},
				Title: port.Name,
				Endpoint: reference{
					port.URL,
				},
				Contract: reference{port.ContractURL},
				Dataset:  reference{spec.ID},
			})
		}
		document.Datasets = append(document.Datasets, entry)
	}
	return document, nil
}

// entityID binds a node to its kind, stable product ID and output name, never a deployment address.
func entityID(kind, product, output string) string {
	tuple, _ := json.Marshal([]string{kind, product, output})
	return fmt.Sprintf(
		"https://devantler.tech/.well-known/data-product/%s/%x",
		kind,
		sha256.Sum256(tuple),
	)
}

func optionalReference(value string) *reference {
	if value == "" {
		return nil
	}
	return &reference{ID: value}
}

// validateMetadata bounds public data before building an RDF graph; backend diagnostics are never echoed.
func validateMetadata(spec datav1.DataProductSpec, size *int) error {
	if spec.Name == "" || spec.Description == "" || spec.Version == "" || spec.Owner.Name == "" ||
		len(spec.Outputs) == 0 ||
		len(spec.Outputs) > maxOutputs {
		return errors.New(
			"dataset profile requires identity, title, description, version, owner and bounded named outputs",
		)
	}
	fields := []string{
		spec.ID,
		spec.Name,
		spec.Description,
		spec.Version,
		spec.Owner.Name,
		spec.Owner.URL,
		spec.DocumentationURL,
	}
	for _, value := range []string{spec.Owner.URL, spec.DocumentationURL} {
		if value != "" && !validHTTPS(value) {
			return errors.New(
				"catalog documentation and owner links must be absolute public HTTPS URLs",
			)
		}
	}
	for _, port := range spec.Outputs {
		if port.Name == "" || !validHTTPS(port.URL) || !validHTTPS(port.ContractURL) {
			return errors.New(
				"catalog outputs require names and absolute public HTTPS endpoint and contract URLs",
			)
		}
		fields = append(fields, port.Name, port.URL, port.ContractURL, port.MediaType)
	}
	for _, value := range fields {
		*size += len(value)
		if len(value) > 16*1024 || *size > maxMetadata || !utf8.ValidString(value) {
			return errors.New(
				"catalog exceeds the 16-KiB field or 1-MiB metadata limit, or contains invalid UTF-8",
			)
		}
	}
	return nil
}

// validIRI permits portable HTTPS/URN identities, including fragments, without JSON-LD blank or relative nodes.
func validIRI(value string) bool {
	if value == "" || len(value) > 2048 || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "\\<>\"{}|^`") ||
		strings.IndexFunc(
			value,
			func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) },
		) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil {
		return false
	}
	if parsed.Scheme == "urn" {
		return urnPattern.MatchString(value)
	}
	return parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.Opaque == ""
}

func validHTTPS(
	value string,
) bool {
	return strings.HasPrefix(value, "https://") && validIRI(value)
}
