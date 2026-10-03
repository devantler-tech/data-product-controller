package v1

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// TestEngineReaderRefusesFullSecretNegotiation exercises production client negotiation and preserves caller middleware.
func TestEngineReaderRefusesFullSecretNegotiation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                 string
		partial, unavailable bool
	}{
		{"metadata supported", true, false},
		{"full object only", false, false},
		{"representation unavailable", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster, metadata := cnpgFixture(t)
			var sources, secrets, fullObjects, wrapped atomic.Int32
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					var value any
					switch r.URL.Path {
					case "/apis/postgresql.cnpg.io/v1/namespaces/products/clusters/warehouse":
						sources.Add(1)
						value = cluster
					case "/api/v1/namespaces/products/secrets/warehouse-app":
						secrets.Add(1)
						switch {
						case tc.partial:
							value = metadata
						case !tc.unavailable && permitsFullJSON(r.Header.Get("Accept")):
							fullObjects.Add(1)
							value = &corev1.Secret{
								TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
								ObjectMeta: metadata.ObjectMeta,
								Data: map[string][]byte{
									"password": []byte("synthetic-full-secret-marker"),
								},
							}
						default:
							w.WriteHeader(http.StatusNotAcceptable)
							value = &metav1.Status{
								TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
								Status:   "Failure",
								Code:     http.StatusNotAcceptable,
							}
						}
					default:
						t.Errorf("unexpected API read: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if err := json.NewEncoder(w).Encode(value); err != nil {
						t.Error(err)
					}
				}),
			)
			t.Cleanup(server.Close)
			config := &rest.Config{Host: server.URL, Timeout: 17 * time.Second}
			config.Wrap(func(next http.RoundTripper) http.RoundTripper {
				return engineTestTransport(func(r *http.Request) (*http.Response, error) {
					wrapped.Add(1)
					deadline, bounded := r.Context().Deadline()
					if !bounded || time.Until(deadline) > 5100*time.Millisecond {
						t.Error("production HTTP request lacks the five-second reader bound")
					}
					if strings.Contains(r.URL.Path, "/clusters/") &&
						strings.Contains(r.Header.Get("Accept"), "as=PartialObjectMetadata") {
						t.Error("source representation changed to metadata")
					}
					return next.RoundTrip(r)
				})
			})
			reader, err := NewEngineReader(config)
			if err != nil {
				t.Fatal(err)
			}
			if config.Timeout != 17*time.Second {
				t.Fatal("caller configuration mutated")
			}
			for range 2 {
				got := (&CloudNativePG{Reader: reader}).Observe(
					t.Context(),
					"products",
					cnpgSource(),
				)
				if got.Ready != tc.partial || (!tc.partial && got.Reason != "SourceUnavailable") {
					t.Errorf("observation=%+v; metadata supported=%v", got, tc.partial)
				}
			}
			if sources.Load() != 2 || secrets.Load() != 2 || fullObjects.Load() != 0 ||
				wrapped.Load() != 4 {
				t.Fatalf(
					"sources=%d metadata=%d full=%d middleware=%d; want two fresh read pairs with no full-object response or retry",
					sources.Load(),
					secrets.Load(),
					fullObjects.Load(),
					wrapped.Load(),
				)
			}
		})
	}
}

// permitsFullJSON models standards-compliant servers that require explicit fallback negotiation.
func permitsFullJSON(accept string) bool {
	for _, entry := range strings.Split(accept, ",") {
		media, parameters, err := mime.ParseMediaType(strings.TrimSpace(entry))
		if err == nil && media == "application/json" && parameters["as"] == "" {
			return true
		}
	}
	return false
}

type engineTestTransport func(*http.Request) (*http.Response, error)

// RoundTrip preserves the wrapped transport while recording actual requests.
func (f engineTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestMetadataTransportLeavesOtherRequestsIntact prevents the privacy guard from narrowing source or list representations.
func TestMetadataTransportLeavesOtherRequestsIntact(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, accept string
		metadata             bool
	}{
		{"single metadata GET", http.MethodGet, metadataOnlyAccept + ",application/json", true},
		{"quoted parameters", http.MethodGet, `application/json;as="PartialObjectMetadata";g="meta.k8s.io";v="v1",application/json`, true},
		{"source GET", http.MethodGet, "application/json", false},
		{"metadata list", http.MethodGet, "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1,application/json", false},
		{"different API", http.MethodGet, "application/json;as=PartialObjectMetadata;g=other.example;v=v1,application/json", false},
		{"POST", http.MethodPost, metadataOnlyAccept + ",application/json", false},
		{"invalid header", http.MethodGet, "invalid;as=PartialObjectMetadata", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request, err := http.NewRequestWithContext(
				t.Context(),
				tc.method,
				"https://api.example.test/object",
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept", tc.accept)
			transport := metadataOnlyTransport{
				next: engineTestTransport(func(forwarded *http.Request) (*http.Response, error) {
					want := tc.accept
					if tc.metadata {
						want = metadataOnlyAccept
					}
					if forwarded.Header.Get("Accept") != want || forwarded.Method != tc.method ||
						forwarded.Context() != request.Context() {
						t.Error("forwarded request representation or context changed unexpectedly")
					}
					if tc.metadata && forwarded == request {
						t.Error("metadata request was not cloned")
					}
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				}),
			}
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if request.Header.Get("Accept") != tc.accept {
				t.Fatal("original request headers mutated")
			}
		})
	}
}
