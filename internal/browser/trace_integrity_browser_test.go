//go:build browser

package browser_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/controller"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestTraceSnapshotIntegrity observes admission through the real response, rendered tables and download control.
func TestTraceSnapshotIntegrity(t *testing.T) {
	override := &atomic.Value{}
	server, _, _, _ := lineageFixture(t, override)
	page := contractBrowser(
		t,
	).MustPage().
		MustNavigate(server.URL + "?product=products%2Froot").
		MustWaitLoad()
	page.MustElement("#trace-inputs").MustWaitVisible()
	for _, test := range []struct {
		name, mutation string
		valid          bool
	}{
		{"canonical graph", "", true},
		{"stale current aggregate", `trace.nodes[0].state='stale';`, false},
		{"not-ready old aggregate", `trace.nodes[0].state='not-ready';trace.nodes[0].observedGeneration=2;`, false},
		{"disabled old aggregate", `trace.nodes[0].state='disabled';trace.nodes[0].observedGeneration=2;`, false},
		{"deleting old aggregate", `trace.nodes[0].state='deleting';trace.nodes[0].observedGeneration=2;`, true},
		{"stale current dimension", `trace.nodes[0].health=health(3);trace.nodes[0].health.source.state='stale';`, false},
		{"disabled old dimension", `trace.nodes[0].health=health(3);trace.nodes[0].health.source.state='disabled';trace.nodes[0].health.source.observedGeneration=2;`, false},
		{"not-applicable observed dimension", `trace.nodes[0].health=health(3);trace.nodes[0].health.source.state='not-applicable';`, false},
		{"unobserved absent dimension", `trace.nodes[0].health=health(3);trace.nodes[0].health.source.state='unobserved';trace.nodes[0].health.source.observedGeneration=0;`, true},
		{"independent disabled dimension", `trace.nodes[0].health=health(3);trace.nodes[0].health.source.state='disabled';`, true},
		{"disconnected product", `trace.nodes.push(node('products/unrelated'));`, false},
		{"health generation mismatch", `trace.nodes[0].health=health(2);`, false},
		{"resolved cycle in incomplete trace", `trace.complete=false;trace.issues=['missing'];trace.edges.push({...edge('products/upstream','products/root'),depth:2});`, false},
		{"missing producer compatibility", `trace.complete=false;trace.issues=['missing'];trace.nodes[1]={key:'products/upstream',state:'missing',generation:0,observedGeneration:0};trace.edges[0].state='missing';`, false},
		{"multibyte metadata over byte limit", `trace.nodes[0].displayName='😀'.repeat(4096)+'a';`, false},
		{"multibyte metadata at byte limit", `trace.nodes[0].displayName='😀'.repeat(4096);`, true},
		{"unsupported identity URL", `trace.nodes[0].id='https://user:password@example.test/private';`, false},
		{"unsupported support URL", `trace.nodes[0].owner={name:'Publisher',url:'javascript:alert(1)'};`, false},
		{"canonical public identities", `trace.nodes[0].id='urn:example:root';trace.nodes[1].owner={name:'Publisher',url:'https://localhost/support'};`, true},
		{"failed lookup publisher metadata", `trace.complete=false;trace.issues=['missing'];trace.nodes[1]={key:'products/upstream',state:'missing',generation:0,observedGeneration:0,displayName:'Unobserved owner'};trace.edges[0].state='missing';trace.edges[0].compatibility='not-evaluated';`, false},
		{"failed lookup nonzero generation", `trace.complete=false;trace.issues=['missing'];trace.nodes[1]={key:'products/upstream',state:'missing',generation:7,observedGeneration:7};trace.edges[0].state='missing';trace.edges[0].compatibility='not-evaluated';`, false},
		{"missing edge to observed product", `trace.complete=false;trace.issues=['missing'];trace.edges[0].state='missing';trace.edges[0].compatibility='not-evaluated';`, false},
		{"false namespace boundary", `trace.complete=false;trace.issues=['cross-namespace'];trace.nodes.pop();trace.edges[0].state='cross-namespace';trace.edges[0].compatibility='not-evaluated';`, false},
		{"canonical missing observation", `trace.complete=false;trace.issues=['missing'];trace.nodes[1]={key:'products/upstream',state:'missing',generation:0,observedGeneration:0};trace.edges[0].state='missing';trace.edges[0].compatibility='not-evaluated';`, true},
		{"canonical namespace boundary", `trace.complete=false;trace.issues=['cross-namespace'];trace.nodes.pop();trace.edges[0].to='foreign/producer';trace.edges[0].state='cross-namespace';trace.edges[0].compatibility='not-evaluated';`, true},
		{"impossible direct depth", `trace.edges[0].depth=64;`, false},
		{"impossible nested depth", `trace.nodes.push(node('products/deep'));trace.edges.push({...edge('products/upstream','products/deep'),depth:7});`, false},
		{"shared dependency paths", `trace.nodes.push(node('products/deep'));trace.edges.push({...edge('products/upstream','products/deep'),depth:2},{...edge('products/root','products/deep'),input:'shared'});`, true},
		{"conflicting shared consumer depths", `trace.nodes.push(node('products/branch'),node('products/left'),node('products/right'));trace.edges.push({...edge('products/root','products/branch'),input:'branch'},{...edge('products/branch','products/upstream'),depth:2},{...edge('products/upstream','products/left'),depth:2,input:'left'},{...edge('products/upstream','products/right'),depth:3,input:'right'});`, false},
		{"shared consumer first visited directly", `trace.nodes.push(node('products/branch'),node('products/left'),node('products/right'));trace.edges.push({...edge('products/root','products/branch'),input:'branch'},{...edge('products/branch','products/upstream'),depth:2},{...edge('products/upstream','products/left'),depth:2,input:'left'},{...edge('products/upstream','products/right'),depth:2,input:'right'});`, true},
		{"shared consumer first visited indirectly", `trace.nodes.push(node('products/branch'),node('products/left'),node('products/right'));trace.edges.push({...edge('products/root','products/branch'),input:'branch'},{...edge('products/branch','products/upstream'),depth:2},{...edge('products/upstream','products/left'),depth:3,input:'left'},{...edge('products/upstream','products/right'),depth:3,input:'right'});`, true},
		{"explicit cycle finding", `trace.complete=false;trace.issues=['cycle'];trace.edges.push({...edge('products/upstream','products/root'),depth:2,state:'cycle'});`, true},
		{"evaluated traversal timeout", `trace.complete=false;trace.issues=['timeout'];trace.edges[0].state='timeout';`, true},
		{"no requirement incompatibility", `trace.edges[0].compatibility='contract-incompatible';`, false},
		{"wrong major verdict", `trace.edges[0].requirement=requirement('v1.0.0');trace.nodes[1].version='v2.0.0';`, false},
		{"below minimum verdict", `trace.edges[0].requirement=requirement('v1.1.0');trace.nodes[1].version='v1.0.0';`, false},
		{"major zero different version", `trace.edges[0].requirement=requirement('v0.1.1');trace.nodes[1].version='v0.1.0';`, false},
		{"large exact numeric components", `trace.edges[0].requirement=requirement('v1.9007199254740993.0');trace.nodes[1].version='v1.9007199254740992.0';`, false},
		{"compatible declared upgrade", `trace.edges[0].requirement=requirement('v1.0.0');trace.nodes[1].version='v1.1.0';`, true},
		{"reported incompatibility", `trace.edges[0].requirement=requirement('v1.0.0');trace.nodes[1].version='v2.0.0';trace.edges[0].compatibility='contract-incompatible';`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := page.MustEval(`()=>{
			const node=key=>({key,state:'ready',generation:3,observedGeneration:3});
			const edge=(from,to)=>({from,to,input:'observations',output:'query',depth:1,state:'resolved',compatibility:'compatible'});
			const health=generation=>Object.fromEntries(['source','connector','contracts','composition'].map(key=>[key,{state:'ready',message:'Observed',generation,observedGeneration:generation}]));
			const requirement=minimumVersion=>({protocol:'OpenAPI',minimumVersion});
			const trace={apiVersion:'data-product-lineage/v1',root:'products/root',complete:true,issues:[],nodes:[node('products/root'),node('products/upstream')],edges:[edge('products/root','products/upstream')]};
			` + test.mutation + `return JSON.stringify(trace);}`).Str()
			override.Store([]byte(body))
			page.MustElement("#trace-inputs").MustClick()
			page.MustElement("#trace-inputs").MustWait(`()=>!this.disabled`)
			admitted := page.MustEval(`()=>!document.querySelector('#trace-result').hidden && !document.querySelector('#save-trace').hidden`).
				Bool()
			if admitted != test.valid {
				t.Errorf(
					"snapshot admitted=%v, want %v; status=%s",
					admitted,
					test.valid,
					page.MustElement("#trace-status").MustText(),
				)
			}
		})
	}
}

// TestTraceMaximumDepth uses the canonical Go producer's terminal-boundary precedence.
func TestTraceMaximumDepth(t *testing.T) {
	for _, namespace := range []string{"products", "foreign"} {
		t.Run(namespace, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := data.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			objects := make([]client.Object, 0, 64)
			for depth := 0; depth < 64; depth++ {
				product := workspaceProduct("https://example.test")
				product.Namespace = "products"
				product.Name = fmt.Sprintf("level-%d", depth)
				product.Spec.ID = "urn:example:" + product.Name
				product.Spec.UI = nil
				ref := data.ProductReference{
					Name:   fmt.Sprintf("level-%d", depth+1),
					Output: product.Spec.Outputs[0].Name,
				}
				if depth == 63 {
					ref.Namespace = namespace
				}
				product.Spec.Inputs = []data.InputPort{{Name: "upstream", ProductRef: ref}}
				objects = append(objects, product)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			server := httptest.NewServer(
				registry.NewHandlerWithOptions(reader, registry.HandlerOptions{
					DiscoveryEnabled:   func(context.Context) bool { return true },
					LineageEnabled:     func(context.Context) bool { return true },
					InputCompatibility: controller.DeclaredInputCompatibility,
				}),
			)
			t.Cleanup(server.Close)
			page := contractBrowser(
				t,
			).MustPage().
				MustNavigate(server.URL + "?product=products%2Flevel-0").
				MustWaitLoad()
			page.MustElement("#trace-inputs").MustWaitVisible().MustClick()
			page.MustElement("#trace-inputs").MustWait(`()=>!this.disabled`)
			if !page.MustEval(`()=>!document.querySelector('#trace-result').hidden && !document.querySelector('#save-trace').hidden`).
				Bool() {
				t.Fatalf(
					"canonical maximum-depth trace rejected: %s",
					page.MustElement("#trace-status").MustText(),
				)
			}
		})
	}
}
