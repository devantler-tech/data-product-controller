package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	datav1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCatalogPagination requires a complete bounded snapshot, including failures after useful metadata.
func TestCatalogPagination(t *testing.T) {
	t.Parallel()
	first, second := fixture(), fixture()
	second.Name, second.Spec.ID = "second", "urn:example:second"
	for _, tc := range []struct {
		name string
		fail int
		want int
	}{
		{"complete", 0, http.StatusOK}, {"later API failure", 2, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := &pageReader{
				t:     t,
				pages: [][]datav1.DataProduct{{*second}, {*first}},
				fail:  tc.fail,
			}
			result := request(t, source, true, "urn:example:catalog")
			if result.Code != tc.want || source.calls != 2 {
				t.Fatalf("status=%d calls=%d: %s", result.Code, source.calls, result.Body)
			}
			if tc.fail != 0 {
				if strings.Contains(result.Body.String(), first.Spec.ID) ||
					strings.Contains(result.Body.String(), second.Spec.ID) {
					t.Fatal("partial metadata escaped after a later API failure")
				}
			} else {
				other := &pageReader{t: t, pages: [][]datav1.DataProduct{{*first, *second}}}
				if result.Body.String() != request(
					t,
					other,
					true,
					"urn:example:catalog",
				).Body.String() {
					t.Fatal("page or object ordering changed the exported graph")
				}
			}
		})
	}
	for _, count := range []int{256, 257} {
		var pages [][]datav1.DataProduct
		for n := 0; n < count; n += 16 {
			pages = append(pages, make([]datav1.DataProduct, min(16, count-n)))
		}
		source := &pageReader{t: t, pages: pages}
		result := request(t, source, true, "urn:example:catalog")
		want := http.StatusOK
		if count > 256 {
			want = http.StatusRequestEntityTooLarge
		}
		if result.Code != want || source.calls > 17 {
			t.Fatalf("scan count %d: status=%d calls=%d", count, result.Code, source.calls)
		}
	}
}

// TestCatalogAggregateBounds prevents many individually valid publishers bypassing total limits.
func TestCatalogAggregateBounds(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"metadata", "outputs"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var products []*datav1.DataProduct
			for i := 0; i < 70; i++ {
				p := fixture()
				p.Name = fmt.Sprintf("product-%d", i)
				p.Spec.ID = fmt.Sprintf("urn:example:data:%d", i)
				if kind == "metadata" {
					p.Spec.Description = strings.Repeat("x", 16000)
				} else {
					for j := 1; j < 16; j++ {
						port := p.Spec.Outputs[0]
						port.Name = fmt.Sprintf("query-%d", j)
						p.Spec.Outputs = append(p.Spec.Outputs, port)
					}
				}
				products = append(products, p)
			}
			result := request(t, reader(t, products...), true, "urn:example:catalog")
			if result.Code != http.StatusUnprocessableEntity {
				t.Fatalf("aggregate %s bound: %d", kind, result.Code)
			}
		})
	}
}

// TestCatalogBusyBound caps simultaneous API decodes and releases capacity when the build finishes.
func TestCatalogBusyBound(t *testing.T) {
	t.Parallel()
	source := &blockingReader{entered: make(chan struct{}), release: make(chan struct{})}
	handler, err := catalogHandler(source)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/catalog", nil),
		)
	}()
	<-source.entered
	busy := httptest.NewRecorder()
	handler.ServeHTTP(
		busy,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/catalog", nil),
	)
	close(source.release)
	<-done
	if busy.Code != http.StatusServiceUnavailable || source.calls.Load() != 1 {
		t.Fatalf(
			"concurrent catalog was not bounded: status=%d reads=%d",
			busy.Code,
			source.calls.Load(),
		)
	}
	if busy.Header().Get("Retry-After") == "" {
		t.Fatal("busy response lacks retry guidance")
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(
		result,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/catalog", nil),
	)
	if result.Code != http.StatusOK {
		t.Fatalf("catalog capacity did not recover: %d", result.Code)
	}
}

type blockingReader struct {
	client.Reader
	entered, release chan struct{}
	calls            atomic.Int32
}

func catalogHandler(source client.Reader) (http.Handler, error) {
	return catalog.NewHandler(
		source,
		catalog.Options{
			ID:      "urn:example:catalog",
			Enabled: func(context.Context) bool { return true },
		},
	)
}

func (r *blockingReader) List(
	ctx context.Context,
	_ client.ObjectList,
	_ ...client.ListOption,
) error {
	if r.calls.Add(1) == 1 {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type pageReader struct {
	client.Reader
	t           *testing.T
	pages       [][]datav1.DataProduct
	calls, fail int
}

func (r *pageReader) List(
	_ context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	options := (&client.ListOptions{}).ApplyOptions(opts)
	want := ""
	if r.calls > 0 {
		want = fmt.Sprintf("page-%d", r.calls)
	}
	scanned := 0
	for _, page := range r.pages[:r.calls] {
		scanned += len(page)
	}
	if options.Limit != int64(min(16, 257-scanned)) || options.Continue != want {
		r.t.Errorf("unbounded or incorrect continuation: %+v", options)
	}
	r.calls++
	if r.calls == r.fail {
		return errors.New("private-sentinel: backend failed")
	}
	if r.calls > len(r.pages) {
		return errors.New("too many page requests")
	}
	products, ok := list.(*datav1.DataProductList)
	if !ok {
		return errors.New("wrong list kind")
	}
	products.Items = r.pages[r.calls-1]
	if r.calls < len(r.pages) {
		products.Continue = fmt.Sprintf("page-%d", r.calls)
	}
	return nil
}
