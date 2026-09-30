package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandGateAndRealFiles(t *testing.T) {
	// OpenFeature registration is process-wide; exercise these sequentially.
	for _, setting := range []string{"", "false", "invalid", " true "} {
		var out bytes.Buffer
		err := run(
			[]string{"--catalog", "does-not-exist", "--bindings", "does-not-exist"},
			setting,
			&out,
		)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "open input") {
			t.Fatalf("setting %q accessed input or emitted output: %v", setting, err)
		}
	}
	var out bytes.Buffer
	err := run(
		[]string{
			"--catalog",
			"../../docs/examples/dsp-catalog/catalog.json",
			"--bindings",
			"../../docs/examples/dsp-catalog/bindings.json",
		},
		"true",
		&out,
	)
	if err != nil || !strings.Contains(out.String(), `"participantId":"urn:example:provider"`) {
		t.Fatalf("enabled real-file command failed: %v, %s", err, &out)
	}
}

func TestCommandErrorsWriteNoOutput(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"secret":"do-not-disclose"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{},
		{"--catalog", bad},
		{"--catalog", dir, "--bindings", bad},
		{"--catalog", "../../docs/examples/dsp-catalog/catalog.json", "--bindings", bad},
		{"--catalog", "missing", "--bindings", bad},
		{"--catalog", bad, "--bindings", bad, "extra"},
	} {
		var out bytes.Buffer
		err := run(args, "true", &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "do-not-disclose") {
			t.Fatalf("bad invocation emitted output or leaked value: %v, %s", err, &out)
		}
	}
}
