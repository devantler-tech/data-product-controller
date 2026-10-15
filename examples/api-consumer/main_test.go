package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Removing named-output selection or rewriting decimal data breaks this real-handler proof.
func TestConsumesNamedHarbourOutput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/observations" || r.URL.RawQuery != "station=nordhavn" {
			t.Errorf("unexpected data request: %s", r.URL.RequestURI())
		}
		demoproduct.NewHandler().ServeHTTP(w, r)
	}))
	defer server.Close()
	endpoint := server.URL + "/api/observations"
	descriptor := exportedDescriptor(t, endpoint)
	items, err := consume(
		context.Background(),
		bytes.NewReader(descriptor),
		"observations",
		endpoint,
		"nordhavn",
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Station != "nordhavn" || items[0].Temperature != 16.8 ||
		items[0].Salinity != 14.2 {
		t.Fatalf("unexpected measurements: %#v", items)
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one selected query, received %d", requests.Load())
	}
}

// A descriptor never grants network access by declaring an endpoint.
func TestInvalidSelectionMakesNoRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	endpoint := server.URL + "/api/observations"
	original := exportedDescriptor(t, endpoint)
	var descriptor map[string]json.RawMessage
	if err := json.Unmarshal(original, &descriptor); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, outputs, selected, approved string }{
		{"missing", `[]`, "observations", endpoint},
		{
			"duplicate names",
			`[{"name":"observations","protocol":"OpenAPI","mediaType":"application/json","url":"` + endpoint + `","contractUrl":"https://contracts.example/schema"},{"name":"observations","protocol":"OpenAPI","mediaType":"application/json","url":"` + endpoint + `","contractUrl":"https://contracts.example/schema"}]`,
			"observations",
			endpoint,
		},
		{
			"wrong protocol",
			`[{"name":"observations","protocol":"GraphQL","mediaType":"application/json","url":"` + endpoint + `","contractUrl":"https://contracts.example/schema"}]`,
			"observations",
			endpoint,
		},
		{
			"wrong media",
			`[{"name":"observations","protocol":"OpenAPI","mediaType":"text/html","url":"` + endpoint + `","contractUrl":"https://contracts.example/schema"}]`,
			"observations",
			endpoint,
		},
		{"unapproved", string(descriptor["outputs"]), "observations", endpoint + "/other"},
		{"unknown name", string(descriptor["outputs"]), "unknown", endpoint},
		{
			"HTTP",
			`[{"name":"observations","protocol":"OpenAPI","mediaType":"application/json","url":"http://api.example/data","contractUrl":"https://contracts.example/schema"}]`,
			"observations",
			"http://api.example/data",
		},
		{
			"credentials",
			`[{"name":"observations","protocol":"OpenAPI","mediaType":"application/json","url":"https://user:password@api.example/data","contractUrl":"https://contracts.example/schema"}]`,
			"observations",
			"https://user:password@api.example/data",
		},
		{
			"bad contract",
			`[{"name":"observations","protocol":"OpenAPI","mediaType":"application/json","url":"` + endpoint + `","contractUrl":"http://contracts.example/schema"}]`,
			"observations",
			endpoint,
		},
		{
			"encoded path",
			`[{"name":"observations","protocol":"OpenAPI","mediaType":"application/json","url":"https://api.example/%64ata","contractUrl":"https://contracts.example/schema"}]`,
			"observations",
			"https://api.example/%64ata",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor["outputs"] = json.RawMessage(test.outputs)
			data, err := json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			items, err := consume(
				context.Background(),
				bytes.NewReader(data),
				test.selected,
				test.approved,
				"",
				server.Client(),
			)
			if err == nil || items != nil {
				t.Fatal("invalid selection produced data")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("unapproved requests: %d", requests.Load())
	}
}

// Redirect rejection must also hold when the caller's client normally follows redirects.
func TestRedirectsAndCookiesStayWithCaller(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(http.StatusOK) },
		),
	)
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials reached the public example")
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	endpoint := server.URL + "/api/observations"
	client := server.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(
		parsed,
		[]*http.Cookie{
			{
				Name:     "private",
				Value:    "not-for-products",
				Secure:   true,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			},
		},
	)
	client.Jar = jar
	callerPolicy := func(*http.Request, []*http.Request) error { return errors.New("caller redirect policy") }
	client.CheckRedirect = callerPolicy
	items, err := consume(
		context.Background(),
		bytes.NewReader(exportedDescriptor(t, endpoint)),
		"observations",
		endpoint,
		"",
		client,
	)
	if err == nil || items != nil || redirected.Load() != 0 {
		t.Fatal("redirect followed or published data")
	}
	if client.Jar != jar || client.CheckRedirect(nil, nil).Error() != "caller redirect policy" {
		t.Fatal("shared client mutated")
	}
}

// Response failures cannot leak raw errors or publish a valid prefix as complete data.
func TestRejectsIncompleteAndInvalidResponses(t *testing.T) {
	valid := `{"items":[{"station":"nordhavn","temperatureCelsius":16.8,"salinityPsu":14.2,"observedAt":"2026-08-29T12:00:00Z"}]}`
	tests := []struct {
		name, body, contentType string
		status                  int
	}{
		{"missing items", `{}`, "application/json", 200},
		{
			"wrong envelope case",
			strings.Replace(valid, `"items"`, `"Items"`, 1),
			"application/json",
			200,
		},
		{
			"wrong record case",
			strings.Replace(valid, `"station"`, `"Station"`, 1),
			"application/json",
			200,
		},
		{"null items", `{"items":null}`, "application/json", 200},
		{
			"missing measurement",
			`{"items":[{"station":"nordhavn","temperatureCelsius":16.8,"observedAt":"2026-08-29T12:00:00Z"}]}`,
			"application/json",
			200,
		},
		{
			"wrong station",
			strings.Replace(valid, "nordhavn", "refshaleoen", 1),
			"application/json",
			200,
		},
		{
			"duplicate key",
			strings.Replace(
				valid,
				`"temperatureCelsius":16.8`,
				`"temperatureCelsius":16.8,"temperatureCelsius":0`,
				1,
			),
			"application/json",
			200,
		},
		{"trailing JSON", valid + `{}`, "application/json", 200},
		{"truncated JSON", valid[:len(valid)-1], "application/json", 200},
		{
			"unknown field",
			strings.Replace(valid, `"station":`, `"private":"never echo","station":`, 1),
			"application/json",
			200,
		},
		{
			"invalid UTF8",
			strings.Replace(valid, "nordhavn", string([]byte{0xff}), 1),
			"application/json",
			200,
		},
		{"large streamed body", valid + strings.Repeat(" ", (1<<20)+1), "application/json", 200},
		{"non JSON", valid, "text/html", 200},
		{"wrong charset", valid, "application/json; charset=iso-8859-1", 200},
		{"error body", "sensitive diagnostic from publisher", "application/json", 503},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", test.contentType)
					w.WriteHeader(test.status)
					_ = http.NewResponseController(w).Flush()
					_, _ = io.WriteString(w, test.body)
				}),
			)
			defer server.Close()
			endpoint := server.URL + "/api/observations"
			items, err := consume(
				context.Background(),
				bytes.NewReader(exportedDescriptor(t, endpoint)),
				"observations",
				endpoint,
				"nordhavn",
				server.Client(),
			)
			if err == nil || items != nil || err.Error() != errResponse.Error() {
				t.Fatalf("unsafe result: items=%v err=%v", items, err)
			}
		})
	}
}

// TestCancellationAndTLSFailure proves prompt cancellation and trusted TLS are required.
func TestCancellationAndTLSFailure(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if phase == "body" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"items":[`)
						_ = http.NewResponseController(w).Flush()
					}
					if phase == "headers" {
						cancel()
					}
					<-r.Context().Done()
				}),
			)
			defer server.Close()
			endpoint := server.URL + "/api/observations"
			client := server.Client()
			want := errQuery
			if phase == "body" {
				started := make(chan struct{})
				client.Transport = bodyReadTransport{base: client.Transport, started: started}
				want = errResponse
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			start := time.Now()
			items, err := consume(
				ctx,
				bytes.NewReader(exportedDescriptor(t, endpoint)),
				"observations",
				endpoint,
				"",
				client,
			)
			if !errors.Is(err, want) || items != nil || time.Since(start) > time.Second {
				t.Fatal("cancelled query did not stop promptly")
			}
		})
	}
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }),
	)
	defer server.Close()
	endpoint := server.URL + "/api/observations"
	items, err := consume(
		context.Background(),
		bytes.NewReader(exportedDescriptor(t, endpoint)),
		"observations",
		endpoint,
		"",
		http.DefaultClient,
	)
	if !errors.Is(err, errQuery) || items != nil {
		t.Fatal("untrusted TLS certificate was accepted")
	}
}

type partialReader struct{ data []byte }

type bodyReadTransport struct {
	base    http.RoundTripper
	started chan struct{}
}

// RoundTrip exposes the first response-body read so cancellation can target that phase.
func (transport bodyReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err == nil {
		response.Body = &observedBody{ReadCloser: response.Body, started: transport.started}
	}
	return response, err
}

type observedBody struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

// Read signals once before delegating to the real response body.
func (body *observedBody) Read(output []byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	return body.ReadCloser.Read(output)
}

// Read returns valid bytes alongside an error to model an incomplete descriptor read.
func (reader *partialReader) Read(out []byte) (int, error) {
	n := copy(out, reader.data)
	reader.data = reader.data[n:]
	if len(reader.data) == 0 {
		return n, errors.New("private reader diagnostic")
	}
	return n, nil
}

// TestPartialDescriptorReadPublishesNothing rejects failed reads and trailing or oversized data.
func TestPartialDescriptorReadPublishesNothing(t *testing.T) {
	endpoint := "https://api.example/api/observations"
	data := exportedDescriptor(t, endpoint)
	result, err := selectOutput(&partialReader{data: data}, "observations", endpoint)
	if !errors.Is(err, errDescriptor) || result != "" {
		t.Fatal("partial read accepted valid prefix")
	}
	for _, invalid := range [][]byte{append(append([]byte(nil), data...), []byte("{}")...), append(append([]byte(nil), data...), bytes.Repeat([]byte(" "), (64<<10)+1)...)} {
		if result, err := selectOutput(
			bytes.NewReader(invalid),
			"observations",
			endpoint,
		); err == nil ||
			result != "" {
			t.Fatal("invalid descriptor accepted")
		}
	}
}

// Go's default decoder folds field names; a public interface has exact JSON names.
func TestRejectsNonPublicFieldNames(t *testing.T) {
	endpoint := "https://api.example/api/observations"
	data := string(exportedDescriptor(t, endpoint))
	for _, invalid := range []string{
		strings.Replace(data, `"apiVersion"`, `"APIVersion"`, 1),
		strings.Replace(data, `"outputs"`, `"Outputs"`, 1),
		strings.Replace(data, `"protocol"`, `"Protocol"`, 1),
		strings.Replace(data, `"mediaType"`, `"mediaType":"application/json","authorization"`, 1),
	} {
		if endpoint, err := selectOutput(
			strings.NewReader(invalid),
			"observations",
			endpoint,
		); err == nil ||
			endpoint != "" {
			t.Fatal("non-public interface shape accepted")
		}
	}
}

// TestRejectsAmbiguousEndpointAuthorities refuses alternate host and invalid port spellings.
func TestRejectsAmbiguousEndpointAuthorities(t *testing.T) {
	valid := "https://api.example/api/observations"
	data := string(exportedDescriptor(t, valid))
	for _, endpoint := range []string{"https://api.example:/api/observations", "https://api.example:0/api/observations", "https://api.example:65536/api/observations", "https://127.1/api/observations"} {
		input := strings.Replace(data, valid, endpoint, 1)
		if selected, err := selectOutput(
			strings.NewReader(input),
			"observations",
			endpoint,
		); err == nil ||
			selected != "" {
			t.Fatal("ambiguous endpoint authority accepted")
		}
	}
}

// Query re-encoding must never discard independently approved parameters.
func TestQueryBindingMakesNoAlteredRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		demoproduct.NewHandler().ServeHTTP(w, r)
	}))
	defer server.Close()
	for _, query := range []string{"?audience=public;scope=reference", "?station=refshaleoen", "?"} {
		endpoint := server.URL + "/api/observations" + query
		items, err := consume(
			context.Background(),
			bytes.NewReader(exportedDescriptor(t, endpoint)),
			"observations",
			endpoint,
			"nordhavn",
			server.Client(),
		)
		if err == nil || items != nil || requests.Load() != 0 {
			t.Fatal("caller-approved query was changed before access")
		}
	}
}

// Only complete responses within the record budget may become application data.
func TestRecordBudgetAndPartialHTTPResponse(t *testing.T) {
	record := `{"station":"nordhavn","temperatureCelsius":16.8,"salinityPsu":14.2,"observedAt":"2026-08-29T12:00:00Z"}`
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			body := `{"items":[` + strings.TrimSuffix(strings.Repeat(record+",", count), ",") + `]}`
			server := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}),
			)
			defer server.Close()
			endpoint := server.URL + "/api/observations"
			items, err := consume(
				context.Background(),
				bytes.NewReader(exportedDescriptor(t, endpoint)),
				"observations",
				endpoint,
				"",
				server.Client(),
			)
			if count == 64 && (err != nil || len(items) != 64) {
				t.Fatal("valid boundary rejected")
			}
			if count == 65 && (!errors.Is(err, errResponse) || items != nil) {
				t.Fatal("record budget was not enforced")
			}
		})
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		_, _ = io.WriteString(w, `{"items":[`+record+`]}`)
	}))
	defer server.Close()
	endpoint := server.URL + "/api/observations"
	items, err := consume(
		context.Background(),
		bytes.NewReader(exportedDescriptor(t, endpoint)),
		"observations",
		endpoint,
		"",
		server.Client(),
	)
	if !errors.Is(err, errResponse) || items != nil {
		t.Fatal("complete JSON prefix from an incomplete HTTP response was accepted")
	}
}

// Running the example, not only its helper, establishes the application-facing contract.
func TestCommandUsesRegularDescriptorAndPublishesValidatedJSON(t *testing.T) {
	server := httptest.NewTLSServer(demoproduct.NewHandler())
	defer server.Close()
	endpoint := server.URL + "/api/observations"
	path := filepath.Join(t.TempDir(), "descriptor.json")
	if err := os.WriteFile(path, exportedDescriptor(t, endpoint), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--descriptor",
		path,
		"--output",
		"observations",
		"--approved-endpoint",
		endpoint,
		"--station",
		"nordhavn",
	}
	var output bytes.Buffer
	if err := run(context.Background(), args, &output, server.Client()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"temperatureCelsius":16.8`) ||
		strings.Contains(output.String(), "refshaleoen") {
		t.Fatal("command did not publish selected observations")
	}
	output.Reset()
	args[1] = t.TempDir()
	if err := run(
		context.Background(),
		args,
		&output,
		server.Client(),
	); err == nil ||
		output.Len() != 0 {
		t.Fatal("directory selection published data")
	}
}

// The descriptor is produced by the real registry from metadata, rather than a client-shaped fixture.
func exportedDescriptor(t *testing.T, endpoint string) []byte {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	product := &datav1alpha1.DataProduct{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "harbour-observations",
			Namespace:  "products",
			Generation: 1,
		},
		Spec: datav1alpha1.DataProductSpec{
			ID:          "urn:devantler:data-product:harbour-observations",
			Name:        "Harbour observations",
			Description: "Synthetic measurements.",
			Version:     "1.0.0",
			Owner:       datav1alpha1.ProductOwner{Name: "devantler-tech"},
			Outputs: []datav1alpha1.OutputPort{
				{
					Name:        "before",
					Protocol:    datav1alpha1.ProtocolOpenAPI,
					URL:         "https://not-requested.example/before",
					ContractURL: "https://contracts.example/before",
					MediaType:   "application/json",
				},
				{
					Name:        "observations",
					Protocol:    datav1alpha1.ProtocolOpenAPI,
					URL:         endpoint,
					ContractURL: "https://contracts.example/openapi.json",
					MediaType:   "application/json",
				},
				{
					Name:        "after",
					Protocol:    datav1alpha1.ProtocolOpenAPI,
					URL:         "https://not-requested.example/after",
					ContractURL: "https://contracts.example/after",
					MediaType:   "application/json",
				},
			},
		},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(product).Build()
	handler := registry.NewHandlerWithOptions(
		reader,
		registry.HandlerOptions{DiscoveryEnabled: func(context.Context) bool { return true }},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			"https://registry.example/api/v2/products/products/harbour-observations",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("registry descriptor: %d %s", response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}
