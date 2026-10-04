package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeyaml "sigs.k8s.io/yaml"
)

// These fixtures assert author-visible results, independently of validator internals.
func product(name string) data.DataProduct {
	return data.DataProduct{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "data.devantler.tech/v1alpha1",
			Kind:       "DataProduct",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "products"},
		Spec: data.DataProductSpec{
			ID: "urn:example:" + name, Name: "Example", Description: "Public observations",
			Version: "v1.0.0", Owner: data.ProductOwner{Name: "Example team"},
			Outputs: []data.OutputPort{
				{
					Name:        "observations",
					Protocol:    data.ProtocolOpenAPI,
					URL:         "https://api.example.test/observations",
					ContractURL: "https://api.example.test/openapi.json",
				},
			},
		},
	}
}

func bundle(t *testing.T, products ...data.DataProduct) string {
	t.Helper()
	var documents []string
	for _, p := range products {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, string(b))
	}
	return strings.Join(documents, "\n---\n")
}

func check(t *testing.T, text string) Report {
	t.Helper()
	return Check(context.Background(), strings.NewReader(text), "")
}

func requireCode(t *testing.T, report Report, code string) {
	t.Helper()
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf(
		"missing %s: valid=%t complete=%t products=%d diagnostics=%+v",
		code,
		report.Valid,
		report.Complete,
		report.Products,
		report.Diagnostics,
	)
}

func TestPreviewCannotTrustSubmittedStatus(t *testing.T) {
	t.Parallel()
	p := product("harbour")
	p.Generation = 42
	p.Status.ObservedGeneration = 42
	p.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionTrue,
		Reason: "SecretMarker", Message: "PRIVATE_RECORD", ObservedGeneration: 42,
		LastTransitionTime: metav1.Now(),
	}}
	p.Spec.Source = &data.ProvisionedSource{
		Adapter: "crossplane/v1",
		ResourceRef: data.ProvisionedResourceReference{
			APIVersion: "storage.example.test/v1",
			Kind:       "Bucket",
			Name:       "private-bucket",
		},
		ConnectionSecretRef: data.ConnectionSecretReference{Name: "private-password"},
	}
	r := check(t, bundle(t, p))
	if !r.Valid || !r.Complete || len(r.Descriptors) != 1 {
		t.Fatalf("valid authored product rejected: %+v", r)
	}
	var preview struct {
		APIVersion         string `json:"apiVersion"`
		Ready              bool   `json:"ready"`
		Generation         int64  `json:"generation"`
		ObservedGeneration int64  `json:"observedGeneration"`
		Health             map[string]struct {
			State string `json:"state"`
		} `json:"health"`
	}
	if err := json.Unmarshal(r.Descriptors[0], &preview); err != nil {
		t.Fatal(err)
	}
	if preview.APIVersion != "data-product-descriptor/v1" || preview.Ready ||
		preview.Generation != 0 || preview.ObservedGeneration != 0 || preview.Health["source"].State != "unobserved" {
		t.Fatalf("preview asserted a live observation: %+v", preview)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"PRIVATE_RECORD", "SecretMarker", "private-bucket", "private-password", "resourceRef", "connectionSecretRef"} {
		if strings.Contains(string(encoded), marker) {
			t.Fatalf("report leaked %s", marker)
		}
	}
	if strings.Join(r.RequiredFeatures, ",") != "provisioned-sources" {
		t.Fatalf("missing source gate: %+v", r)
	}
}

func TestStrictBoundedIngress(t *testing.T) {
	t.Parallel()
	valid := bundle(t, product("one"))
	tests := []struct{ name, input, code string }{
		{"empty", "", "NoProducts"},
		{
			"wrong resource",
			strings.ReplaceAll(valid, "DataProduct", "Secret"),
			"UnsupportedResource",
		},
		{
			"duplicate JSON key",
			strings.Replace(
				valid,
				"\"version\":\"v1.0.0\"",
				"\"version\":\"v1.0.0\",\"version\":\"v2.0.0\"",
				1,
			),
			"InvalidDocument",
		},
		{
			"unknown field",
			strings.Replace(
				valid,
				"\"spec\":{",
				"\"spec\":{\"PRIVATE_FIELD\":\"PRIVATE_VALUE\",",
				1,
			),
			"UnknownField",
		},
		{"malformed", "apiVersion: [PRIVATE_VALUE", "InvalidDocument"},
		{
			"alias",
			"apiVersion: &value data.devantler.tech/v1alpha1\nkind: *value",
			"InvalidDocument",
		},
		{"too much input", strings.Repeat(" ", (2<<20)+1), "InputLimit"},
		{"too deep", strings.Repeat("{\"x\":", 66) + "0" + strings.Repeat("}", 66), "DepthLimit"},
		{"trailing garbage", valid + "\nPRIVATE_VALUE: [", "InvalidDocument"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := check(t, tc.input)
			if r.Valid || len(r.Descriptors) != 0 {
				t.Fatalf("unsafe input accepted: %+v", r)
			}
			requireCode(t, r, tc.code)
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "PRIVATE_") {
				t.Fatal("diagnostic echoed submitted content")
			}
		})
	}
}

func TestGeneratedAdmissionRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*data.DataProduct)
	}{
		{"missing owner", func(p *data.DataProduct) { p.Spec.Owner.Name = "" }},
		{"bad version", func(p *data.DataProduct) { p.Spec.Version = "not-a-version" }},
		{
			"duplicate outputs",
			func(p *data.DataProduct) { p.Spec.Outputs = append(p.Spec.Outputs, p.Spec.Outputs[0]) },
		},
		{"UI cross-field CEL", func(p *data.DataProduct) {
			p.Spec.UI = &data.ProductUI{
				URL: "https://ui.example.test", Title: "Explore",
				Contract: &data.UIContract{
					APIVersion:   "data-product-ui/v1",
					HostOrigins:  []data.UIHostOrigin{"https://host.example.test"},
					Capabilities: []data.UICapability{"appearance"},
				},
			}
		}},
		{"engine matrix CEL", func(p *data.DataProduct) {
			p.Spec.Source = &data.ProvisionedSource{
				Adapter: "crossplane/v1",
				Engine: &data.EngineSelection{
					APIVersion: "engine-provider/v1",
					Type:       "sql",
					Provider:   "native",
				},
				ResourceRef: data.ProvisionedResourceReference{
					APIVersion: "postgresql.cnpg.io/v1",
					Kind:       "Cluster",
					Name:       "source",
				},
				ConnectionSecretRef: data.ConnectionSecretReference{Name: "source-app"},
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := product("one")
			tc.mutate(&p)
			r := check(t, bundle(t, p))
			if r.Valid || len(r.Descriptors) != 0 {
				t.Fatalf("invalid admission accepted: %+v", r)
			}
			requireCode(t, r, "AdmissionInvalid")
		})
	}
	for _, model := range []string{"document", "graph"} {
		t.Run("hybrid "+model, func(t *testing.T) {
			t.Parallel()
			p := product("one")
			p.Spec.Source = &data.ProvisionedSource{
				Adapter: "cnpg-hybrid/v1",
				Engine: &data.EngineSelection{
					APIVersion: "engine-provider/v1",
					Type:       model,
					Provider:   "cnpg-hybrid",
				},
				ResourceRef: data.ProvisionedResourceReference{
					APIVersion: "postgresql.cnpg.io/v1",
					Kind:       "Cluster",
					Name:       "source",
				},
				ConnectionSecretRef: data.ConnectionSecretReference{Name: "reader"},
			}
			r := check(t, bundle(t, p))
			if !r.Valid || !r.Complete {
				t.Fatalf("supported hybrid rejected: %+v", r)
			}
		})
	}
}

func TestBundleIdentityAndNamespace(t *testing.T) {
	t.Parallel()
	p, q := product("one"), product("two")
	for _, tc := range []struct {
		name     string
		products []data.DataProduct
	}{
		{"namespace/name", []data.DataProduct{p, p}},
		{"stable ID", func() []data.DataProduct { q.Spec.ID = p.Spec.ID; return []data.DataProduct{p, q} }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := check(t, bundle(t, tc.products...))
			requireCode(t, r, "IdentityConflict")
			if r.Valid {
				t.Fatal("ambiguous identities accepted")
			}
		})
	}
	p.Namespace = ""
	requireCode(t, check(t, bundle(t, p)), "NamespaceRequired")
	r := Check(context.Background(), strings.NewReader(bundle(t, p)), "selected")
	if !r.Valid || !r.Complete ||
		!strings.Contains(string(r.Descriptors[0]), "\"namespace\":\"selected\"") {
		t.Fatalf("explicit namespace failed: %+v", r)
	}
	requireCode(
		t,
		Check(context.Background(), strings.NewReader(bundle(t, p)), "BAD/NAMESPACE"),
		"InvalidNamespace",
	)
}

func TestPublicPublicationProfile(t *testing.T) {
	t.Parallel()
	credentialURL := (&url.URL{
		Scheme: "https",
		Host:   "api.example.test",
		User:   url.UserPassword("example-user", "example-password"),
	}).String()
	for _, tc := range []struct {
		name, code string
		mutate     func(*data.DataProduct)
	}{
		{"credential URL", "InvalidPublicMetadata", func(p *data.DataProduct) { p.Spec.Outputs[0].URL = credentialURL }},
		{"credential ID", "InvalidPublicMetadata", func(p *data.DataProduct) { p.Spec.ID = credentialURL + "/product" }},
		{"fragment ID", "InvalidPublicMetadata", func(p *data.DataProduct) { p.Spec.ID = "https://api.example.test/product#PRIVATE_VALUE" }},
		{"missing ID host", "AdmissionInvalid", func(p *data.DataProduct) { p.Spec.ID = "https://" }},
		{"zone URL", "InvalidPublicMetadata", func(p *data.DataProduct) { p.Spec.Outputs[0].URL = "https://[fe80::1%25en0]/" }},
		{"field budget", "InvalidPublicMetadata", func(p *data.DataProduct) { p.Spec.Description = strings.Repeat("x", (16<<10)+1) }},
		{"encoded budget", "InvalidPublicMetadata", func(p *data.DataProduct) {
			p.Spec.Name = strings.Repeat("x", 16000)
			p.Spec.Description = strings.Repeat("x", 16000)
			p.Spec.Owner.Name = strings.Repeat("x", 16000)
			p.Spec.Outputs[0].MediaType = strings.Repeat("x", 16000)
			p.Spec.Outputs[0].ContractURL = "https://api.example.test/" + strings.Repeat("x", 16000)
		}},
		{"blank UI title", "InvalidUI", func(p *data.DataProduct) { p.Spec.UI = &data.ProductUI{URL: "https://ui.example.test", Title: "   "} }},
		{"noncanonical origin", "InvalidUI", func(p *data.DataProduct) {
			p.Spec.UI = &data.ProductUI{URL: "https://ui.example.test", Title: "Explore", Contract: &data.UIContract{APIVersion: "data-product-ui/v2", HostOrigins: []data.UIHostOrigin{"https://host.example.test:443"}, Capabilities: []data.UICapability{"status"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := product("one")
			tc.mutate(&p)
			r := check(t, bundle(t, p))
			requireCode(t, r, tc.code)
			if r.Valid || len(r.Descriptors) != 0 {
				t.Fatalf("unsafe publication accepted: %+v", r)
			}
		})
	}
}

func compose(consumer *data.DataProduct, producer string) {
	consumer.Spec.Inputs = []data.InputPort{
		{
			Name:       "upstream",
			ProductRef: data.ProductReference{Name: producer, Output: "observations"},
		},
	}
}

func TestLocalResolutionDoesNotInventExternalEvidence(t *testing.T) {
	t.Parallel()
	p, q := product("one"), product("two")
	compose(&q, p.Name)
	r := check(t, bundle(t, p, q))
	if !r.Valid || !r.Complete || len(r.Descriptors) != 2 {
		t.Fatalf("supplied producer failed: %+v", r)
	}
	r = check(t, bundle(t, q))
	requireCode(t, r, "ProducerUnresolved")
	if !r.Valid || r.Complete || len(r.Descriptors) != 0 {
		t.Fatalf("missing local producer treated as proven: %+v", r)
	}
	q.Spec.Inputs[0].ProductRef.Output = "missing"
	r = check(t, bundle(t, p, q))
	requireCode(t, r, "OutputNotFound")
	if r.Valid {
		t.Fatal("missing supplied output accepted")
	}
	q.Spec.Inputs[0].ProductRef.Namespace = "foreign"
	r = check(t, bundle(t, p, q))
	requireCode(t, r, "CrossNamespaceInput")
	if r.Valid {
		t.Fatal("cross-namespace reference accepted")
	}
}

func TestDeclaredCompatibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		actual, minimum string
		protocol        data.OutputProtocol
		valid           bool
	}{
		{"v1.2.0", "v1.1.0", data.ProtocolOpenAPI, true},
		{"v1.0.0", "v1.1.0", data.ProtocolOpenAPI, false},
		{"v2.0.0", "v1.0.0", data.ProtocolOpenAPI, false},
		{"v0.1.0", "v0.1.0", data.ProtocolOpenAPI, true},
		{"v0.2.0", "v0.1.0", data.ProtocolOpenAPI, false},
		{"v1.2.0-beta", "v1.0.0", data.ProtocolOpenAPI, false},
		{"v1.0.0", "v1.0.0", data.ProtocolGraphQL, false},
	} {
		t.Run(tc.actual+"/"+tc.minimum+"/"+string(tc.protocol), func(t *testing.T) {
			t.Parallel()
			p, q := product("one"), product("two")
			p.Spec.Version = tc.actual
			compose(&q, p.Name)
			q.Spec.Inputs[0].Contract = &data.InputContract{
				MinimumVersion: tc.minimum,
				Protocol:       tc.protocol,
			}
			r := check(t, bundle(t, p, q))
			if r.Valid != tc.valid {
				t.Fatalf("compatibility=%v want %v: %+v", r.Valid, tc.valid, r)
			}
			if !tc.valid {
				requireCode(t, r, "ContractIncompatible")
			}
		})
	}
}

func TestBoundedGraphAndCycles(t *testing.T) {
	t.Parallel()
	p, q := product("one"), product("two")
	compose(&p, q.Name)
	compose(&q, p.Name)
	r := check(t, bundle(t, p, q))
	requireCode(t, r, "CompositionCycle")
	if r.Valid {
		t.Fatal("cycle accepted")
	}
	var chain []data.DataProduct
	for i := range 66 {
		p := product(fmt.Sprintf("p-%d", i))
		if i > 0 {
			compose(&p, fmt.Sprintf("p-%d", i-1))
		}
		chain = append(chain, p)
	}
	requireCode(t, check(t, bundle(t, chain...)), "CompositionLimit")
	var tooMany []data.DataProduct
	for i := range 257 {
		tooMany = append(tooMany, product(fmt.Sprintf("p-%d", i)))
	}
	requireCode(t, check(t, bundle(t, tooMany...)), "ProductLimit")
	// The common producer remains valid when reached through two supplied branches.
	a, b, c, d := product("a"), product("b"), product("c"), product("d")
	compose(&b, a.Name)
	compose(&c, a.Name)
	compose(&d, b.Name)
	d.Spec.Inputs = append(
		d.Spec.Inputs,
		data.InputPort{
			Name:       "other",
			ProductRef: data.ProductReference{Name: c.Name, Output: "observations"},
		},
	)
	if r := check(t, bundle(t, a, b, c, d)); !r.Valid || !r.Complete {
		t.Fatalf("diamond rejected: %+v", r)
	}
}

func TestCompositionDepthMatchesController(t *testing.T) {
	t.Parallel()
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Parallel()
			var chain []data.DataProduct
			for index := range count {
				p := product(fmt.Sprintf("depth-%d", index))
				if index > 0 {
					compose(&p, chain[index-1].Name)
				}
				chain = append(chain, p)
			}
			// Leaf-first order exercises cached suffix depth as well as a full walk.
			r := check(t, bundle(t, chain...))
			if count == 64 {
				if !r.Valid || !r.Complete {
					t.Fatal("supported 64-product chain rejected")
				}
			} else {
				requireCode(t, r, "CompositionLimit")
			}
		})
	}
}

func TestIndependentObservationDeclarations(t *testing.T) {
	t.Parallel()
	p := product("one")
	p.Spec.Connector = &data.Connector{
		Adapter: "deployment/v1",
		ResourceRef: data.ConnectorResourceReference{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "connector",
		},
	}
	p.Spec.ContractChecks = []data.ContractCheck{
		{
			Output: "absent",
			ResourceRef: data.ConnectorResourceReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "probe",
			},
		},
	}
	r := check(t, bundle(t, p))
	requireCode(t, r, "ContractOutputNotFound")
	if r.Valid {
		t.Fatal("unknown contract output accepted")
	}
	p.Spec.ContractChecks[0].Output = "observations"
	r = check(t, bundle(t, p))
	if !r.Valid || !r.Complete {
		t.Fatalf("valid declarations rejected: %+v", r)
	}
	if strings.Join(r.RequiredFeatures, ",") != "connector-readiness,contract-readiness" {
		t.Fatalf("gate explanation wrong: %+v", r.RequiredFeatures)
	}
}

func TestCancellationProducesNoPreview(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Check(ctx, strings.NewReader(bundle(t, product("one"))), "")
	requireCode(t, r, "ValidationLimit")
	if r.Valid || len(r.Descriptors) != 0 {
		t.Fatal("canceled check asserted successful preview")
	}
}

func TestTotalInputBudget(t *testing.T) {
	t.Parallel()
	var products []data.DataProduct
	for i := range 5 {
		p := product(fmt.Sprintf("consumer-%d", i))
		for j := range 205 {
			p.Spec.Inputs = append(p.Spec.Inputs, data.InputPort{
				Name:       fmt.Sprintf("input-%d", j),
				ProductRef: data.ProductReference{Name: "external", Output: "observations"},
			})
		}
		products = append(products, p)
	}
	r := check(t, bundle(t, products...))
	requireCode(t, r, "CompositionLimit")
	if r.Valid || len(r.Descriptors) != 0 {
		t.Fatal("excess total inputs accepted")
	}
}

func TestAuthoredKubernetesMetadata(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*data.DataProduct){
		func(p *data.DataProduct) { p.Labels = map[string]string{"bad/key/extra": "value"} },
		func(p *data.DataProduct) { p.Generation = -1 },
	} {
		p := product("one")
		mutate(&p)
		r := check(t, bundle(t, p))
		requireCode(t, r, "AdmissionInvalid")
		if r.Valid {
			t.Fatal("invalid Kubernetes metadata accepted")
		}
	}
}

func TestKubernetesMetadataFieldCase(t *testing.T) {
	t.Parallel()
	for _, sample := range []string{
		strings.Replace(bundle(t, product("one")), `"name":"one"`, `"Name":"one"`, 1),
		strings.Replace(bundle(t, product("one")), `"name":"one"`, `"name":"one","Name":"other"`, 1),
		strings.Replace(bundle(t, product("one")), `"name":"one"`, `"name":"one","Labels":{"bad/key/extra":"value"}`, 1),
		strings.Replace(bundle(t, product("one")), `"name":"one"`, `"name":"one","Annotations":{"PRIVATE_FIELD":"PRIVATE_VALUE"}`, 1),
	} {
		r := check(t, sample)
		requireCode(t, r, "UnknownField")
		if r.Valid || r.Complete || len(r.Descriptors) != 0 {
			t.Fatalf("case-aliased Kubernetes metadata accepted: %+v", r)
		}
		encoded, err := json.Marshal(r)
		if err != nil || strings.Contains(string(encoded), "PRIVATE_") {
			t.Fatal("unknown metadata field leaked into diagnostics")
		}
	}
}

func TestCustomYAMLTagIsRejected(t *testing.T) {
	t.Parallel()
	r := check(t, "!include "+bundle(t, product("one")))
	requireCode(t, r, "InvalidDocument")
	if r.Valid {
		t.Fatal("custom tagged document accepted")
	}
}

func TestCompleteReportBudget(t *testing.T) {
	// Compile the immutable schema before parallel workload timing. This case exercises
	// the encoded budget; cancellation is independently exercised by its own regression.
	if _, err := loadAdmission(); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	var products []data.DataProduct
	for i := range 256 {
		p := product(fmt.Sprintf("p-%d", i))
		p.Spec.Description = strings.Repeat("x", 7300)
		products = append(products, p)
	}
	r := check(t, bundle(t, products...))
	requireCode(t, r, "PreviewLimit")
	if r.Valid || len(r.Descriptors) != 0 {
		t.Fatal("oversized report emitted partial public previews")
	}
}

func TestYAMLScalarPublication(t *testing.T) {
	t.Parallel()
	input := scalarFixture(t)
	documents, code, _ := readDocuments(strings.NewReader(input))
	if code != "" || len(documents) != 1 {
		t.Fatalf("scalar declarations rejected: %s", code)
	}
	p := documents[0].product
	if p.Generation != 9007199254740993 || p.Spec.Name != "yes" ||
		p.Spec.Description != "2026-10-03" {
		t.Fatal("YAML scalar meaning or integer precision changed")
	}
	r := check(t, input)
	if !r.Valid || !r.Complete || len(r.Descriptors) != 1 {
		t.Fatalf("scalar publication failed: %+v", r)
	}
}

func TestKubernetesYAMLScalarParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, scalar, description string }{
		{"plain yes", "yes", "Observations"},
		{"plain no", "no", "Observations"},
		{"plain on", "On", "Observations"},
		{"plain off", "OFF", "Observations"},
		{"plain y", "Y", "Observations"},
		{"plain n", "n", "Observations"},
		{"mixed-case yes string", "yEs", "Observations"},
		{"mixed-case on string", "oN", "Observations"},
		{"mixed-case no string", "nO", "Observations"},
		{"quoted yes", `"yes"`, "Observations"},
		{"tagged string", "!!str yes", "Observations"},
		{"non-specific boolean string", "! yes", "Observations"},
		{"non-specific number string", "! 42", "Observations"},
		{"non-specific date string", "Example", "! 2026-10-03"},
		{"tagged boolean", "!!bool yes", "Observations"},
		{"quoted tagged boolean", `!!bool "yes"`, "Observations"},
		{"invalid tagged boolean", "!!bool yEs", "Observations"},
		{"plain date", "Example", "2026-10-03"},
		{"tagged date", "Example", "!!timestamp 2026-10-03"},
		{"invalid tagged date", "Example", "!!timestamp banana"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(scalarFixture(t), `name: "yes"`, "name: "+tc.scalar, 1)
			input = strings.Replace(
				input,
				"description: 2026-10-03",
				"description: "+tc.description,
				1,
			)
			encoded, err := kubeyaml.YAMLToJSONStrict([]byte(input))
			r := check(t, input)
			if err != nil {
				requireCode(t, r, "InvalidDocument")
				return
			}
			var deployed data.DataProduct
			if err := json.Unmarshal(encoded, &deployed); err != nil {
				requireCode(t, r, "AdmissionInvalid")
				return
			}
			if !r.Valid || !r.Complete || len(r.Descriptors) != 1 {
				t.Fatalf("Kubernetes-compatible declaration rejected: %+v", r)
			}
			var preview struct{ DisplayName, Description string }
			if err := json.Unmarshal(r.Descriptors[0], &preview); err != nil {
				t.Fatal(err)
			}
			if preview.DisplayName != deployed.Spec.Name ||
				preview.Description != deployed.Spec.Description {
				t.Fatal("preview changed the scalar meaning used by Kubernetes")
			}
		})
	}
}

func scalarFixture(t *testing.T) string {
	t.Helper()
	input, err := os.ReadFile("testdata/scalar.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(input)
}

func TestKubernetesYAMLStringKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"! yes", "! 42", "!!str 42", `"yes"`} {
		input := strings.Replace(
			bundle(t, product("one")),
			`"metadata":{`,
			`"metadata":{"annotations":{`+key+`: "value"},`,
			1,
		)
		r := check(t, input)
		if !r.Valid || !r.Complete {
			t.Fatalf("Kubernetes string key rejected: %+v", r)
		}
	}
}

func TestKubernetesYAMLRejectsNonStringKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"yes", "On", "OFF", "Y", "n"} {
		input := strings.Replace(
			bundle(t, product("one")),
			`"metadata":{`,
			`"metadata":{"annotations":{`+key+`: "value"},`,
			1,
		)
		requireCode(t, check(t, input), "InvalidDocument")
	}
}

func TestKubernetesYAMLNonSpecificGeneration(t *testing.T) {
	t.Parallel()
	input := strings.Replace(
		bundle(t, product("one")),
		`"metadata":{`,
		`"metadata":{"generation": ! 42,`,
		1,
	)
	encoded, err := kubeyaml.YAMLToJSONStrict([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var deployed data.DataProduct
	if err := json.Unmarshal(encoded, &deployed); err == nil {
		t.Fatal("deployment decoder unexpectedly accepted a string generation")
	}
	requireCode(t, check(t, input), "AdmissionInvalid")
}

func TestKubernetesYAMLNullDocumentAlignment(t *testing.T) {
	t.Parallel()
	valid := bundle(t, product("one"))
	for _, input := range []string{
		"---\nnull\n---\n" + valid,
		valid + "\n---\n~\n---\n",
		"---\n---\n" + valid + "\n---\nnull\n---\n" + bundle(t, product("two")),
	} {
		r := check(t, input)
		if !r.Valid || !r.Complete {
			t.Fatalf("empty document changed decoder alignment: %+v", r)
		}
	}
	for _, scalar := range []string{"! null", "! ~", "!"} {
		requireCode(t, check(t, valid+"\n---\n"+scalar+"\n"), "InvalidDocument")
	}
}
