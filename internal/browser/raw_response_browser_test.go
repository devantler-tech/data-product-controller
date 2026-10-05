//go:build browser

package browser_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestRegistryRawResponseAdmission preserves raw tokens on initial, continuation and exact responses.
func TestRegistryRawResponseAdmission(t *testing.T) {
	for _, discovery := range []bool{false, true} {
		for _, scope := range []string{"first", "more", "exact"} {
			if !discovery && scope != "first" {
				continue
			}
			for _, mutation := range []string{"duplicate envelope", "duplicate nested", "rounded fraction"} {
				if scope == "exact" && mutation == "duplicate envelope" {
					continue
				}
				t.Run(
					fmt.Sprintf("discovery=%t/%s/%s", discovery, scope, mutation),
					func(t *testing.T) {
						scheme := runtime.NewScheme()
						if err := data.AddToScheme(scheme); err != nil {
							t.Fatal(err)
						}
						assets := registry.NewHandler(
							fake.NewClientBuilder().WithScheme(scheme).Build(),
						)
						first := discoveryProduct("harbour")
						second := discoveryProduct("summary")
						envelope := func(p map[string]any, cursor string) string {
							value := map[string]any{"products": []any{p}}
							if discovery {
								value["apiVersion"] = "data-product-discovery/v1"
								value["rejected"] = 0
								value["continue"] = cursor
							}
							wire, err := json.Marshal(value)
							if err != nil {
								t.Fatal(err)
							}
							return string(wire)
						}
						mutate := func(wire string) string {
							switch mutation {
							case "duplicate envelope":
								if discovery {
									return strings.Replace(
										wire,
										`"continue":`,
										`"cont\u0069nue":"earlier-cursor","continue":`,
										1,
									)
								}
								return strings.Replace(
									wire,
									`"products":`,
									`"prod\u0075cts":[],"products":`,
									1,
								)
							case "duplicate nested":
								return strings.Replace(
									wire,
									`"generation":3`,
									`"gener\u0061tion":3,"generation":3`,
									1,
								)
							default:
								return strings.Replace(
									wire,
									`"generation":3`,
									`"generation":3.0000000000000000001`,
									1,
								)
							}
						}
						server := httptest.NewServer(
							http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
								w.Header().Set("Content-Type", "application/json")
								switch req.URL.Path {
								case "/api/v1/ui-config":
									_, _ = fmt.Fprintf(
										w,
										`{"discoveryEnabled":%t,"uiContractEnabled":false,"uiAppearanceEnabled":false}`,
										discovery,
									)
								case "/api/v1/products", "/api/v2/products":
									body := envelope(first, "")
									if scope == "first" {
										body = mutate(body)
									}
									if scope == "more" {
										body = envelope(first, "second")
										if req.URL.Query().Get("continue") != "" {
											body = mutate(envelope(second, ""))
										}
									}
									_, _ = w.Write([]byte(body))
								case "/api/v2/products/products/harbour":
									wire, err := json.Marshal(first)
									if err != nil {
										t.Error(err)
										return
									}
									_, _ = w.Write([]byte(mutate(string(wire))))
								default:
									assets.ServeHTTP(w, req)
								}
							}),
						)
						defer server.Close()
						target := server.URL
						if scope == "exact" {
							target += "?product=products%2Fharbour"
						}
						page := contractBrowser(t).MustPage().MustNavigate(target).MustWaitLoad()
						page.MustElement("#refresh-products").MustWait(`()=>!this.disabled`)
						switch scope {
						case "first":
							if page.MustEval(`()=>document.querySelectorAll('.product-card').length`).
								Int() !=
								0 ||
								page.MustElement("#product-count").MustText() != "Unavailable" {
								t.Error("malformed raw inventory was admitted")
							}
						case "more":
							page.MustElement("#load-more").MustWaitVisible().MustClick()
							page.MustElement("#load-more").MustWait(`()=>!this.disabled`)
							if page.MustEval(`()=>document.querySelectorAll('.product-card').length`).
								Int() !=
								1 ||
								!page.MustElement("#load-more").MustVisible() ||
								strings.Contains(page.MustElement("#discovery-scope").MustText(), "All products") {
								t.Error(
									"malformed continuation replaced the retained partial inventory",
								)
							}
						case "exact":
							page.MustElement("#selection-status").
								MustWait(`()=>(!this.hidden && !this.textContent.includes('Loading')) || !document.querySelector('#product-metadata').hidden`)
							if !page.MustEval(`()=>document.querySelector('#product-metadata').hidden && document.querySelector('#descriptor-actions').hidden && document.querySelector('#interfaces-detail').hidden && document.querySelector('iframe').hidden && !document.querySelector('iframe').hasAttribute('src')`).
								Bool() {
								t.Error(
									"malformed exact response retained product metadata or access",
								)
							}
						}
					},
				)
			}
		}
	}
}

// TestTraceRawResponseAdmission catches duplicate fields and rounded graph numbers before export.
func TestTraceRawResponseAdmission(t *testing.T) {
	override := &atomic.Value{}
	server, _, _, _ := lineageFixture(t, override)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Froot").
		MustWaitLoad()
	page.MustElement("#trace-inputs").MustWaitVisible()
	canonical := `{"apiVersion":"data-product-lineage/v1","root":"products/root","complete":true,"issues":[],"nodes":[{"key":"products/root","state":"ready","generation":3,"observedGeneration":3},{"key":"products/upstream","state":"ready","generation":3,"observedGeneration":3}],"edges":[{"from":"products/root","to":"products/upstream","input":"observations","output":"query","depth":1,"state":"resolved","compatibility":"compatible"}]}`
	for _, tc := range []struct{ old, new string }{
		{`"complete":true`, `"compl\u0065te":false,"complete":true`},
		{`"state":"ready"`, `"st\u0061te":"stale","state":"ready"`},
		{`"generation":3`, `"generation":3.0000000000000000001`},
		{`"depth":1`, `"depth":1.0000000000000000001`},
	} {
		t.Run(tc.new, func(t *testing.T) {
			override.Store([]byte(strings.Replace(canonical, tc.old, tc.new, 1)))
			page.MustElement("#trace-inputs").MustClick()
			page.MustElement("#trace-inputs").MustWait(`()=>!this.disabled`)
			if !page.MustEval(`()=>document.querySelector('#trace-result').hidden && document.querySelector('#save-trace').hidden`).
				Bool() {
				t.Error("malformed raw trace retained rendering or download")
			}
		})
	}
}
