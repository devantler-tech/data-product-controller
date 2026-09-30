package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	datav1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/piprate/json-gold/ld"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const dcat = "http://www.w3.org/ns/dcat#"

// TestCatalogIndependentConsumer discovers an actual query through an offline RDF consumer.
func TestCatalogIndependentConsumer(t *testing.T) {
	t.Parallel()
	demo := httptest.NewTLSServer(demoproduct.NewHandler())
	t.Cleanup(demo.Close)
	product := fixture()
	product.Spec.Outputs[0].URL = demo.URL + "/api/observations"
	product.Spec.Outputs[0].ContractURL = demo.URL + "/openapi.json"
	response := request(t, reader(t, product), true, "urn:example:catalog")
	if response.Code != http.StatusOK {
		t.Fatalf("catalog status %d: %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") != "application/ld+json" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("catalog lacks JSON-LD or fresh-response headers")
	}
	rdf := asRDF(t, response.Body.Bytes())
	for _, triple := range []string{
		`<urn:example:catalog> <` + dcat + `dataset> <urn:example:data> .`,
		`<urn:example:data> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <` + dcat + `Dataset> .`,
		`<urn:example:data> <http://purl.org/dc/terms/identifier> "urn:example:data" .`,
		`<urn:example:data> <http://purl.org/dc/terms/title> "Harbour observations" .`,
		`<urn:example:data> <` + dcat + `version> "v1.0.0" .`,
	} {
		if !strings.Contains(rdf, triple) {
			t.Fatalf("missing independently expanded triple %s\n%s", triple, rdf)
		}
	}
	distribution := linkedIRI(t, rdf, "urn:example:data", dcat+"distribution")
	service := linkedIRI(t, rdf, distribution, dcat+"accessService")
	if linkedIRI(t, rdf, service, dcat+"servesDataset") != "urn:example:data" ||
		linkedIRI(t, rdf, service, dcat+"endpointDescription") != demo.URL+"/openapi.json" ||
		linkedIRI(t, rdf, "urn:example:catalog", dcat+"service") != service {
		t.Fatal("dataset, catalog and service relationships disagree")
	}
	endpoint := linkedIRI(t, rdf, service, dcat+"endpointURL")
	if endpoint != demo.URL+"/api/observations" {
		t.Fatal("discovered endpoint is outside the test-owned TLS server")
	}
	if linkedIRI(t, rdf, distribution, dcat+"accessURL") != endpoint {
		t.Fatal("service-backed distribution lost its access URL")
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		endpoint+"?station=Nordhavn",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G704 -- The discovered URL is asserted to equal this test's owned TLS server above.
	result, err := demo.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Body.Close() }()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), `"station":"nordhavn"`) {
		t.Fatalf("discovered query failed: %d %s", result.StatusCode, body)
	}
	for _, forbidden := range []string{"downloadURL", "accessRights", "hasPolicy", "license", "private-sentinel", "Deployment", "ready"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("invented or private metadata: %s", forbidden)
		}
	}
}

// TestCatalogFlagAndPublisherOptIn prevents a disabled feature or undeclared capability from being exported.
func TestCatalogFlagAndPublisherOptIn(t *testing.T) {
	t.Parallel()
	spy := &listSpy{err: errors.New("reader must not be called")}
	if got := request(t, spy, false, ""); got.Code != http.StatusNotFound || spy.calls != 0 {
		t.Fatalf("disabled catalog accessed Kubernetes: status=%d calls=%d", got.Code, spy.calls)
	}
	p := fixture()
	p.Annotations = nil
	if got := request(
		t,
		reader(t, p),
		true,
		"urn:example:catalog",
	); got.Code != http.StatusOK ||
		strings.Contains(got.Body.String(), p.Spec.ID) {
		t.Fatalf("non-dataset capability exported: %d %s", got.Code, got.Body)
	}
	for _, value := range []string{"", "dataset", " Dataset", "DataService"} {
		p.Annotations = map[string]string{"data.devantler.tech/dcat-type": value}
		if got := request(
			t,
			reader(t, p),
			true,
			"urn:example:catalog",
		); got.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unsupported profile %q: %d", value, got.Code)
		}
	}
}

// TestCatalogRejectsAmbiguousGraphIdentity prevents silent RDF merging across products and entity kinds.
func TestCatalogRejectsAmbiguousGraphIdentity(t *testing.T) {
	t.Parallel()
	a, b := fixture(), fixture()
	b.Name = "second"
	if got := request(
		t,
		reader(t, a, b),
		true,
		"urn:example:catalog",
	); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate product ID accepted: %d", got.Code)
	}
	if got := request(
		t,
		reader(t, a),
		true,
		a.Spec.ID,
	); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("catalog and dataset share identity: %d", got.Code)
	}
	response := request(t, reader(t, a), true, "urn:example:catalog")
	distribution := linkedIRI(t, asRDF(t, response.Body.Bytes()), a.Spec.ID, dcat+"distribution")
	service := linkedIRI(t, asRDF(t, response.Body.Bytes()), distribution, dcat+"accessService")
	for _, id := range []string{distribution, service} {
		if !strings.HasPrefix(id, "https://devantler.tech/.well-known/data-product/") {
			t.Fatalf("node identity is outside the owned namespace: %s", id)
		}
		if got := request(t, reader(t, a), true, id); got.Code != http.StatusUnprocessableEntity {
			t.Fatalf("catalog and output share identity: %d", got.Code)
		}
		b.Spec.ID = id
		if got := request(
			t,
			reader(t, a, b),
			true,
			"urn:example:catalog",
		); got.Code != http.StatusUnprocessableEntity {
			t.Fatalf("dataset and output share identity: %d", got.Code)
		}
	}
	b.Spec.ID = distribution
	if got := request(
		t,
		reader(t, a, b),
		true,
		"urn:example:catalog",
	); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("dataset and distribution share identity: %d", got.Code)
	}
}

// TestCatalogReadBounds refuses failed, cancelled and truncated lists without returning partial success.
func TestCatalogReadBounds(t *testing.T) {
	t.Parallel()
	for _, spy := range []*listSpy{
		{err: errors.New("private-sentinel backend failure")},
		{err: context.DeadlineExceeded},
		{continuation: "next-page"},
		{count: 257},
	} {
		response := request(t, spy, true, "urn:example:catalog")
		if response.Code < 400 || strings.Contains(response.Body.String(), "private-sentinel") {
			t.Fatalf("failed/partial catalog returned: %d %s", response.Code, response.Body)
		}
		if spy.limit != 16 || spy.deadline <= 0 || spy.deadline > 5*time.Second {
			t.Fatalf("unbounded Kubernetes read: limit=%d deadline=%v", spy.limit, spy.deadline)
		}
	}
}

// TestCatalogIdentityStability separates stable RDF identity from current deployment metadata.
func TestCatalogIdentityStability(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"urn:example:data", "urn:example:data:", "https://example.test/data?edition=1#identity"} {
		p := fixture()
		p.Spec.ID = id
		original := request(t, reader(t, p), true, "urn:example:catalog")
		rdf := asRDF(t, original.Body.Bytes())
		distribution := linkedIRI(t, rdf, id, dcat+"distribution")
		service := linkedIRI(t, rdf, distribution, dcat+"accessService")
		p.Name, p.Namespace, p.Spec.Version = "renamed", "moved", "v2.0.0"
		p.Spec.Outputs[0].URL = "https://rotated.example.test/query"
		updated := asRDF(
			t,
			request(t, reader(t, p), true, "urn:example:other-catalog").Body.Bytes(),
		)
		if got := linkedIRI(t, updated, id, dcat+"distribution"); got != distribution {
			t.Fatalf("deployment change altered distribution identity: %s != %s", got, distribution)
		}
		if linkedIRI(t, updated, distribution, dcat+"accessService") != service ||
			distribution == service {
			t.Fatal("service identity changed or collided with its distribution")
		}
		p.Spec.Outputs[0].Name = "another-output"
		renamed := asRDF(t, request(t, reader(t, p), true, "urn:example:catalog").Body.Bytes())
		if linkedIRI(t, renamed, id, dcat+"distribution") == distribution {
			t.Fatal("different outputs share identity")
		}
	}
}

// TestCatalogOrderingAndLiteralMetadata preserves hostile text as literals and exports every declared interface.
func TestCatalogOrderingAndLiteralMetadata(t *testing.T) {
	t.Parallel()
	p := fixture()
	p.Spec.Name = `Øresund <script>alert(1)</script> {"@id":"https://attacker.test/"}`
	p.Spec.Owner.URL, p.Spec.DocumentationURL = "", ""
	p.Spec.Outputs = nil
	for i, protocol := range []datav1.OutputProtocol{datav1.ProtocolOpenAPI, datav1.ProtocolAsyncAPI, datav1.ProtocolGraphQL, datav1.ProtocolDCAT, datav1.ProtocolArrowFlight} {
		p.Spec.Outputs = append(
			p.Spec.Outputs,
			datav1.OutputPort{
				Name:        fmt.Sprintf("query-%d", i),
				Protocol:    protocol,
				URL:         "https://example.test/query",
				ContractURL: "https://example.test/contract",
				MediaType:   "application/json; charset=utf-8",
			},
		)
	}
	first := request(t, reader(t, p), true, "urn:example:catalog")
	if first.Code != http.StatusOK {
		t.Fatal(first.Body.String())
	}
	if strings.Contains(first.Body.String(), "<script>") {
		t.Fatal("JSON response did not escape publisher-controlled HTML")
	}
	rdf := asRDF(t, first.Body.Bytes())
	if strings.Count(rdf, "<"+dcat+"DataService>") != 5 ||
		strings.Contains(rdf, "<https://attacker.test/>") ||
		strings.Contains(rdf, "homepage") ||
		strings.Contains(rdf, "landingPage") {
		t.Fatalf("lost interface or corrupted optional/literal metadata: %s", rdf)
	}
	if !strings.Contains(rdf, `"application/json; charset=utf-8"`) ||
		!strings.Contains(rdf, "Øresund") {
		t.Fatal("descriptive media type or Unicode title was lost")
	}
	for left, right := 0, len(p.Spec.Outputs)-1; left < right; left, right = left+1, right-1 {
		p.Spec.Outputs[left], p.Spec.Outputs[right] = p.Spec.Outputs[right], p.Spec.Outputs[left]
	}
	second := request(t, reader(t, p), true, "urn:example:catalog")
	if first.Body.String() != second.Body.String() {
		t.Fatal("output order changed the catalog")
	}
	// Removing opt-in withdraws the dataset even though the resource remains present.
	p.Annotations = nil
	withdrawn := request(t, reader(t, p), true, "urn:example:catalog")
	if strings.Contains(withdrawn.Body.String(), p.Spec.ID) {
		t.Fatal("withdrawn dataset remains discoverable")
	}
}

// TestCatalogRejectsInvalidLinksAndOversizedMetadata prevents malformed or unbounded graphs.
func TestCatalogRejectsInvalidLinksAndOversizedMetadata(t *testing.T) {
	t.Parallel()
	credentialURL := (&url.URL{
		Scheme: "https", Host: "example.test", User: url.UserPassword("test-user", "test-password"),
	}).String()
	for _, change := range []func(*datav1.DataProduct){
		func(p *datav1.DataProduct) { p.Spec.ID = "relative" },
		func(p *datav1.DataProduct) { p.Spec.Outputs[0].URL = credentialURL + "/query" },
		func(p *datav1.DataProduct) { p.Spec.Outputs[0].ContractURL = "http://example.test/contract" },
		func(p *datav1.DataProduct) { p.Spec.Owner.URL = "javascript:alert(1)" },
		func(p *datav1.DataProduct) { p.Spec.Description = strings.Repeat("x", 16*1024+1) },
		func(p *datav1.DataProduct) { p.Spec.Outputs = append(p.Spec.Outputs, p.Spec.Outputs[0]) },
		func(p *datav1.DataProduct) { p.Spec.Outputs = make([]datav1.OutputPort, 1025) },
	} {
		p := fixture()
		change(p)
		got := request(t, reader(t, p), true, "urn:example:catalog")
		if got.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid metadata returned %d: %s", got.Code, got.Body)
		}
	}
	products := make([]*datav1.DataProduct, 40)
	for i := range products {
		p := fixture()
		p.Name = fmt.Sprintf("product-%d", i)
		p.Spec.ID = fmt.Sprintf("urn:example:data:%d", i)
		p.Spec.Description = strings.Repeat("<", 16000)
		products[i] = p
	}
	if got := request(
		t,
		reader(t, products...),
		true,
		"urn:example:catalog",
	); got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("encoded response bound not enforced: %d", got.Code)
	}
	if got := request(t, reader(t), true, ""); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("enabled catalog with missing identity: %d", got.Code)
	}
	for _, id := range []string{"/relative", "_:blank", credentialURL, "urn:missing-namespace", "urn::catalog", "urn:a:data", "urn:-invalid:data", "urn:example:data%GG", "urn:example:data[bad]", "urn:example:data?plain", "https://example.test/has space"} {
		if _, err := catalog.NewHandler(nil, catalog.Options{ID: id}); err == nil {
			t.Fatalf("invalid catalog ID accepted: %s", id)
		}
	}
}

// TestSharedOwnerPageDoesNotAssertAgentIdentity preserves distinct teams using a common support page.
func TestSharedOwnerPageDoesNotAssertAgentIdentity(t *testing.T) {
	t.Parallel()
	a, b := fixture(), fixture()
	b.Name = "second"
	b.Spec.ID = "urn:example:second"
	b.Spec.Owner.Name = "Another team"
	rdf := asRDF(t, request(t, reader(t, a, b), true, "urn:example:catalog").Body.Bytes())
	if strings.Contains(rdf, "http://xmlns.com/foaf/0.1/homepage") ||
		strings.Count(rdf, "<http://xmlns.com/foaf/0.1/page> <https://example.test/team>") != 2 {
		t.Fatal("shared support page asserts publisher identity or loses a team")
	}
}

type listSpy struct {
	client.Reader
	err          error
	continuation string
	count, calls int
	limit        int64
	deadline     time.Duration
}

func (s *listSpy) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	s.calls++
	s.limit = (&client.ListOptions{}).ApplyOptions(opts).Limit
	if deadline, ok := ctx.Deadline(); ok {
		s.deadline = time.Until(deadline)
	}
	products, ok := list.(*datav1.DataProductList)
	if !ok {
		return errors.New("unexpected list kind")
	}
	products.Continue = s.continuation
	products.Items = make([]datav1.DataProduct, s.count)
	return s.err
}

func fixture() *datav1.DataProduct {
	return &datav1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "harbour",
			Namespace:   "products",
			Annotations: map[string]string{"data.devantler.tech/dcat-type": "Dataset"},
		},
		Spec: datav1.DataProductSpec{
			ID:          "urn:example:data",
			Name:        "Harbour observations",
			Description: "Public marine observations.",
			Version:     "v1.0.0",
			Owner: datav1.ProductOwner{
				Name: "Marine team",
				URL:  "https://example.test/team",
			},
			DocumentationURL: "https://example.test/docs",
			Outputs: []datav1.OutputPort{
				{
					Name:        "query",
					Protocol:    datav1.ProtocolOpenAPI,
					URL:         "https://example.test/query",
					ContractURL: "https://example.test/openapi.json",
					MediaType:   "application/json",
				},
			},
			Source: &datav1.ProvisionedSource{
				ConnectionSecretRef: datav1.ConnectionSecretReference{Name: "private-sentinel"},
			},
		},
	}
}

func reader(t *testing.T, products ...*datav1.DataProduct) client.Reader {
	t.Helper()
	// controller-runtime's fake client ignores Limit; model real API pagination explicitly.
	pages := [][]datav1.DataProduct{{}}
	for i, product := range products {
		if i > 0 && i%16 == 0 {
			pages = append(pages, nil)
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], *product.DeepCopy())
	}
	return &pageReader{t: t, pages: pages}
}

func request(
	t *testing.T,
	source client.Reader,
	enabled bool,
	id string,
) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := catalog.NewHandler(
		source,
		catalog.Options{ID: id, Enabled: func(context.Context) bool { return enabled }},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"https://attacker.test/api/v1/catalog",
		nil,
	)
	req.Header.Set("X-Forwarded-Host", "attacker.test")
	handler.ServeHTTP(response, req)
	return response
}

type offlineLoader struct{}

func (offlineLoader) LoadDocument(string) (*ld.RemoteDocument, error) {
	return nil, errors.New("remote JSON-LD resolution is forbidden")
}

func asRDF(t *testing.T, body []byte) string {
	t.Helper()
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	options := ld.NewJsonLdOptions("")
	options.DocumentLoader = offlineLoader{}
	options.Format = "application/n-quads"
	result, err := ld.NewJsonLdProcessor().ToRDF(document, options)
	if err != nil {
		t.Fatalf("independent offline JSON-LD consumer: %v", err)
	}
	rdf, ok := result.(string)
	if !ok {
		t.Fatalf("unexpected RDF result %T", result)
	}
	return rdf
}

func linkedIRI(t *testing.T, rdf, subject, predicate string) string {
	t.Helper()
	pattern := regexp.MustCompile(
		fmt.Sprintf(
			`<%s> <%s> <([^>]+)> \.`,
			regexp.QuoteMeta(subject),
			regexp.QuoteMeta(predicate),
		),
	)
	match := pattern.FindStringSubmatch(rdf)
	if len(match) != 2 {
		t.Fatalf("missing IRI relationship %s %s in\n%s", subject, predicate, rdf)
	}
	return match[1]
}
