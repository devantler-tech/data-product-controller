package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{"apiVersion":"data.devantler.tech/v1alpha1","kind":"DataProduct","metadata":{"name":"harbour","namespace":"products"},"spec":{"id":"urn:example:harbour","name":"Harbour","description":"Public observations","version":"v1.0.0","owner":{"name":"Example team"},"outputs":[{"name":"observations","protocol":"OpenAPI","url":"https://api.example.test/observations","contractUrl":"https://api.example.test/openapi.json"}]}}`

func TestDisabledReadsNoSelectedFile(t *testing.T) {
	for _, setting := range []string{"", "false", "invalid"} {
		t.Run(setting, func(t *testing.T) {
			var out bytes.Buffer
			code, err := run(
				context.Background(),
				[]string{"--file", "/missing-file"},
				setting,
				&out,
			)
			if err == nil || code == 0 || out.Len() != 0 {
				t.Fatalf("disabled command wrote output: code=%d error=%v", code, err)
			}
			if setting != "invalid" &&
				!strings.Contains(err.Error(), "publisher preflight is disabled") {
				t.Fatalf("input was read before flag evaluation: %v", err)
			}
		})
	}
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "product.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnabledCommandAndExitSemantics(t *testing.T) {
	path := writeManifest(t, validManifest)
	var out bytes.Buffer
	code, err := run(
		context.Background(),
		[]string{"--file", path, "--format", "json"},
		"true",
		&out,
	)
	if err != nil || code != 0 {
		t.Fatalf("valid command failed: %d %v %s", code, err, out.String())
	}
	var report struct {
		APIVersion      string
		Valid, Complete bool
		Descriptors     []json.RawMessage
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.APIVersion != "data-product-preflight/v1" || !report.Valid || !report.Complete ||
		len(report.Descriptors) != 1 {
		t.Fatalf("invalid report: %s", out.String())
	}
	out.Reset()
	code, err = run(context.Background(), []string{"--file", path}, "true", &out)
	if err != nil || code != 0 || !strings.Contains(out.String(), "1 product") ||
		!strings.Contains(out.String(), "declarations") {
		t.Fatalf("text result: %d %v %s", code, err, out.String())
	}
}

func TestInvalidReportIsSanitizedAndFails(t *testing.T) {
	path := writeManifest(
		t,
		strings.Replace(
			validManifest,
			"\"spec\":{",
			"\"spec\":{\"PRIVATE_VALUE\":\"private secret\",",
			1,
		),
	)
	var out bytes.Buffer
	code, err := run(
		context.Background(),
		[]string{"--file", path, "--format", "json"},
		"true",
		&out,
	)
	if code != 1 || err != nil {
		t.Fatalf("invalid declaration exit: %d %v", code, err)
	}
	if strings.Contains(out.String(), "PRIVATE_VALUE") ||
		strings.Contains(out.String(), "private secret") ||
		strings.Contains(out.String(), "\"descriptors\"") {
		t.Fatalf("report leaked rejected content: %s", out.String())
	}
	if !strings.Contains(out.String(), "UnknownField") {
		t.Fatalf("missing bounded finding: %s", out.String())
	}
}

func TestUnresolvedBundleHasDistinctExit(t *testing.T) {
	path := writeManifest(t, strings.Replace(
		validManifest,
		"\"outputs\":[",
		"\"inputs\":[{\"name\":\"upstream\",\"productRef\":{\"name\":\"absent\",\"output\":\"records\"}}],\"outputs\":[",
		1,
	))
	var out bytes.Buffer
	code, err := run(
		context.Background(),
		[]string{"--file", path, "--format", "json"},
		"true",
		&out,
	)
	if code != 2 || err != nil || !strings.Contains(out.String(), "ProducerUnresolved") ||
		strings.Contains(out.String(), "\"descriptors\"") {
		t.Fatalf("missing producer treated as complete: %d %v %s", code, err, out.String())
	}
}

func TestFileAndUsageErrorsAreBounded(t *testing.T) {
	for _, args := range [][]string{nil, {"--file", t.TempDir()}, {"--file", "https://example.test/product"}, {"--file", "PRIVATE_PATH"}, {"--file", "missing", "--format", "bad"}, {"--unexpected", "PRIVATE_VALUE"}} {
		var out bytes.Buffer
		code, err := run(context.Background(), args, "true", &out)
		if err == nil || code != 1 || out.Len() != 0 || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("unsafe usage error: %d %v", code, err)
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

func TestIncompleteReportWriteFails(t *testing.T) {
	path := writeManifest(t, validManifest)
	code, err := run(context.Background(), []string{"--file", path}, "true", shortWriter{})
	if code != 1 || err == nil {
		t.Fatalf("partial report was treated as success: %d %v", code, err)
	}
}
