package preflight

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	data "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// This builds the single example file outside the module to prove its independent boundary.
func TestIndependentPublisherReportReader(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	binary := filepath.Join(directory, "reader")
	code, err := os.ReadFile("../../docs/examples/preflight-client/main.go")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.WriteFile("main.go", code, 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- executable is the test toolchain; arguments are this test's private temporary paths.
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, source)
	build.Dir = directory
	build.Env = append(os.Environ(), "GO111MODULE=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("independent standard-library build: %v %s", err, output)
	}
	read := func(t *testing.T, data []byte, want bool) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "report.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G204 -- executable is the just-built owned example; only a private fixture path is passed.
		command := exec.CommandContext(t.Context(), binary, path)
		output, err := command.CombinedOutput()
		if (err == nil) != want {
			t.Fatalf("reader result=%v expected success=%t output=%s", err, want, output)
		}
		if !want && strings.Contains(string(output), "PRIVATE_") {
			t.Fatal("reader printed rejected content")
		}
	}
	p, c := product("producer"), dependent("consumer", "producer")
	complete := selected(t, bundle(t, p, c))
	framed, err := json.Marshal(reportAtPayloadSize(t, maxInputBytes-1))
	if err != nil {
		t.Fatal(err)
	}
	framed = append(framed, '\n')
	if len(framed) != maxInputBytes {
		t.Fatalf("expected the exact saved-report limit, got %d", len(framed))
	}
	read(t, framed, true)
	read(t, append(framed, ' '), false)
	many := product("many")
	for index := 0; index < 9; index++ {
		output := many.Spec.Outputs[0]
		output.Name = fmt.Sprintf("output-%d", index)
		many.Spec.Outputs = append(many.Spec.Outputs, output)
	}
	unicodeTitle := product("unicode")
	unicodeTitle.Spec.UI = &data.ProductUI{
		URL:   "https://ui.example.test/view",
		Title: strings.Repeat("ø", 200),
	}
	deleting := product("deleting")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	for index, report := range []BundleReport{complete, selected(t, bundle(t, c)), selected(t, "apiVersion: ["), selected(t, bundle(t, many)), selected(t, bundle(t, unicodeTitle)), selected(t, bundle(t, deleting))} {
		t.Run(fmt.Sprintf("emitted-%d", index), func(t *testing.T) {
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			read(t, encoded, true)
		})
	}
	mutations := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing boolean", func(v map[string]any) { delete(v, "valid") }},
		{"null array", func(v map[string]any) { v["sources"] = nil }},
		{"aggregate document limit", func(v map[string]any) {
			for _, source := range reportArray(t, v["sources"]) {
				reportObject(t, source)["documents"] = float64(4097)
			}
		}},
		{"wrong product count", func(v map[string]any) { v["products"] = float64(1) }},
		{"duplicate stable ID", func(v map[string]any) {
			descriptors := reportArray(t, v["descriptors"])
			reportObject(t, descriptors[1])["id"] = reportObject(t, descriptors[0])["id"]
		}},
		{
			"false diagnostic count",
			func(v map[string]any) { reportObject(t, v["diagnosticCounts"])["total"] = float64(1) },
		},
		{"nested private owner", func(v map[string]any) {
			reportObject(t, reportObject(t, reportArray(t, v["descriptors"])[0])["owner"])["PRIVATE_FIELD"] = "PRIVATE_VALUE"
		}},
		{
			"claimed preview readiness",
			func(v map[string]any) { reportObject(t, reportArray(t, v["descriptors"])[0])["ready"] = true },
		},
		{"consumer before producer", func(v map[string]any) {
			order := reportArray(t, reportObject(t, v["plan"])["order"])
			order[0], order[1] = order[1], order[0]
		}},
		{"foreign plan member", func(v map[string]any) {
			reportObject(t, reportArray(t, reportObject(t, v["plan"])["order"])[0])["key"] = "products/foreign"
		}},
		{"foreign output", func(v map[string]any) {
			reportObject(t, reportArray(t, reportObject(t, v["plan"])["edges"])[0])["output"] = "missing"
		}},
		{"omitted edge", func(v map[string]any) { reportObject(t, v["plan"])["edges"] = []any{} }},
		{"repeated provenance", func(v map[string]any) {
			order := reportArray(t, reportObject(t, v["plan"])["order"])
			reportObject(t, order[1])["document"] = reportObject(t, order[0])["document"]
		}},
		{"false feature union", func(v map[string]any) { v["requiredFeatures"] = []any{} }},
		{
			"omitted visible composition",
			func(v map[string]any) { omitReportFeature(t, v, "composition") },
		},
		{
			"null nested descriptor",
			func(v map[string]any) { reportObject(t, reportArray(t, v["descriptors"])[0])["owner"] = nil },
		},
		{"foreign namespace edge", func(v map[string]any) {
			descriptors := reportArray(t, v["descriptors"])
			reportObject(t, descriptors[0])["namespace"] = "foreign"
			reportObject(t, reportObject(t, reportArray(t, reportObject(t, descriptors[1])["inputs"])[0])["productRef"])["namespace"] = "foreign"
			reportObject(t, reportArray(t, reportObject(t, v["plan"])["order"])[0])["key"] = "foreign/producer"
			reportObject(t, reportArray(t, reportObject(t, v["plan"])["edges"])[0])["producer"] = "foreign/producer"
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			value := reportValue(t, complete)
			mutation.change(value)
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			read(t, encoded, false)
		})
	}
	value := reportValue(t, selected(t, bundle(t, p), bundle(t, c)))
	sources := reportArray(t, value["sources"])
	reportObject(t, sources[0])["products"] = float64(0)
	reportObject(t, sources[1])["products"] = float64(2)
	reportObject(t, sources[1])["documents"] = float64(2)
	badCounts, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("per-source counts", func(t *testing.T) { read(t, badCounts, false) })
	encoded, err := json.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"duplicate field":         []byte(strings.Replace(string(encoded), "\"valid\":true", "\"valid\":true,\"valid\":false", 1)),
		"case-varied field":       []byte(strings.Replace(string(encoded), "\"valid\":true", "\"Valid\":true", 1)),
		"trailing value":          append(append([]byte{}, encoded...), []byte(" {}")...),
		"invalid UTF-8":           []byte("{\"apiVersion\":\"\xff\"}"),
		"oversized":               []byte(strings.Repeat(" ", (2<<20)+1)),
		"unpaired high surrogate": []byte(strings.Replace(string(encoded), `"displayName":"Example"`, `"displayName":"\ud800"`, 1)),
		"unpaired low surrogate":  []byte(strings.Replace(string(encoded), `"displayName":"Example"`, `"displayName":"\udc00"`, 1)),
	} {
		t.Run(name, func(t *testing.T) { read(t, data, false) })
	}
	for _, label := range []string{`"Label �"`, `"Label \ufffd"`, `"Wave \ud83c\udf0a"`, `"Literal \\ud800"`} {
		wire := strings.Replace(
			string(encoded),
			`"displayName":"Example"`,
			`"displayName":`+label,
			1,
		)
		if wire == string(encoded) {
			t.Fatal("Unicode mutation did not reach the report")
		}
		t.Run(label, func(t *testing.T) { read(t, []byte(wire), true) })
	}
	for _, version := range []string{"data-product-ui/v1", "data-product-ui/v2"} {
		p := product("with-ui")
		p.Spec.UI = &data.ProductUI{
			URL:   "https://ui.example.test/view",
			Title: "Product",
			Contract: &data.UIContract{
				APIVersion:   version,
				HostOrigins:  []data.UIHostOrigin{"https://host.example.test"},
				Capabilities: []data.UICapability{},
			},
		}
		report := selected(t, bundle(t, p))
		original, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		read(t, original, true)
		features := []string{"ui-contract"}
		if version == "data-product-ui/v2" {
			features = append(features, "ui-appearance")
		}
		for _, feature := range features {
			t.Run(version+"/"+feature, func(t *testing.T) {
				value := reportValue(t, report)
				omitReportFeature(t, value, feature)
				wire, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				read(t, wire, false)
			})
		}
	}
}

// omitReportFeature preserves the old union and provenance checks while removing one visible requirement.
func omitReportFeature(t *testing.T, value map[string]any, removed string) {
	t.Helper()
	for _, entry := range reportArray(t, value["productFeatures"]) {
		product := reportObject(t, entry)
		retained := []any{}
		for _, feature := range reportArray(t, product["requiredFeatures"]) {
			if feature != removed {
				retained = append(retained, feature)
			}
		}
		product["requiredFeatures"] = retained
	}
	retained := []any{}
	for _, feature := range reportArray(t, value["requiredFeatures"]) {
		if feature != removed {
			retained = append(retained, feature)
		}
	}
	value["requiredFeatures"] = retained
}
