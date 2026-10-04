package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestEveryExplicitSelectionIsChecked covers all selected files, duplicate declarations and input failures.
func TestEveryExplicitSelectionIsChecked(t *testing.T) {
	first := writeManifest(t, validManifest)
	second := writeManifest(t, strings.ReplaceAll(validManifest, "harbour", "coast"))
	var output bytes.Buffer
	code, err := run(
		context.Background(),
		[]string{"--file", first, "--file", second, "--format", "json"},
		"true",
		&output,
	)
	var report struct {
		Products   int
		APIVersion string
	}
	if err != nil || code != 0 {
		t.Fatalf("selected bundle failed: %d %v", code, err)
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Products != 2 || report.APIVersion != "data-product-preflight/v1" {
		t.Fatalf("a selected file was omitted: %s", output.String())
	}
	output.Reset()
	code, err = run(
		context.Background(),
		[]string{"--file", first, "--file", first, "--format", "json"},
		"true",
		&output,
	)
	if code != 1 || err != nil || !strings.Contains(output.String(), "IdentityConflict") {
		t.Fatalf("duplicate selections escaped validation: %d %v %s", code, err, output.String())
	}
	output.Reset()
	code, err = run(
		context.Background(),
		[]string{"--file", "/missing-selected-input", "--file", second},
		"true",
		&output,
	)
	if code != 1 || output.Len() != 0 && !strings.Contains(output.String(), "ReadFailed") {
		t.Fatalf("unreadable earlier selection was ignored: %d %v %s", code, err, output.String())
	}
}

// TestExplicitV2ReportAndDisabledBundle keeps v1 the default and evaluates the gate before input access.
func TestExplicitV2ReportAndDisabledBundle(t *testing.T) {
	path := writeManifest(t, validManifest)
	var output bytes.Buffer
	args := []string{"--file", path, "--report-version", "v2", "--format", "json"}
	code, err := run(context.Background(), args, "true", &output)
	if code != 0 || err != nil || !strings.Contains(output.String(), "data-product-preflight/v2") {
		t.Fatalf("v2 workflow unavailable: %d %v %s", code, err, output.String())
	}
	output.Reset()
	code, err = run(context.Background(), args, "false", &output)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "disabled") || output.Len() != 0 {
		t.Fatalf("disabled bundle read input: %d %v", code, err)
	}
}
