package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAPIOnlyProfileRequiresDataInterfaces preserves the compatibility default and both report versions.
func TestAPIOnlyProfileRequiresDataInterfaces(t *testing.T) {
	withUI := strings.Replace(validManifest, `"spec":{`,
		`"spec":{"ui":{"url":"https://ui.example.test/PRIVATE_UI","title":"PRIVATE_TITLE"},`, 1)
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			for _, fixture := range []struct {
				name, manifest string
				accepted       bool
			}{
				{"data only", validManifest, true},
				{"null UI", strings.Replace(validManifest, `"spec":{`, `"spec":{"ui":null,`, 1), false},
				{"legacy UI", withUI, false},
			} {
				t.Run(fixture.name, func(t *testing.T) {
					file := writeManifest(t, fixture.manifest)
					args := []string{
						"--file",
						file,
						"--format",
						"json",
						"--report-version",
						version,
					}
					var compatibility, explicit bytes.Buffer
					code, err := run(context.Background(), args, "true", &compatibility)
					wantCompatibility := 0
					if fixture.name == "null UI" {
						wantCompatibility = 1
					}
					if code != wantCompatibility || err != nil {
						t.Fatalf(
							"compatibility control failed: %d %v %s",
							code,
							err,
							compatibility.String(),
						)
					}
					code, err = run(
						context.Background(),
						append(args, "--profile", "compatibility"),
						"true",
						&explicit,
					)
					if code != wantCompatibility || err != nil ||
						!bytes.Equal(compatibility.Bytes(), explicit.Bytes()) {
						t.Fatalf("explicit compatibility changed output: %d %v", code, err)
					}
					var output bytes.Buffer
					code, err = run(
						context.Background(),
						append(args, "--profile", "api-only"),
						"true",
						&output,
					)
					if err != nil {
						t.Fatalf("API-only command unavailable: %v", err)
					}
					if fixture.name != "legacy UI" {
						if (code == 0) != fixture.accepted ||
							!bytes.Equal(compatibility.Bytes(), output.Bytes()) {
							t.Fatalf("data-only report changed: %d %s", code, output.String())
						}
						return
					}
					var report struct {
						Valid, Complete bool
						Descriptors     []json.RawMessage
						Plan            any
					}
					if err := json.Unmarshal(output.Bytes(), &report); err != nil {
						t.Fatal(err)
					}
					if code != 1 || report.Valid || report.Complete || len(report.Descriptors) != 0 || report.Plan != nil ||
						!strings.Contains(output.String(), "InvalidUI") ||
						!strings.Contains(output.String(), "Remove spec.ui") ||
						strings.Contains(output.String(), "PRIVATE_") {
						t.Fatalf(
							"legacy UI was accepted or disclosed: %d %s",
							code,
							output.String(),
						)
					}
				})
			}
		})
	}
}

// TestProfileSelectionPrecedesFileAccess rejects unknown profiles and preserves the default-off boundary.
func TestProfileSelectionPrecedesFileAccess(t *testing.T) {
	args := []string{"--file", "PRIVATE_MISSING_FILE", "--profile", "PRIVATE_UNKNOWN_PROFILE"}
	for _, enabled := range []string{"true", "false", ""} {
		var output bytes.Buffer
		code, err := run(context.Background(), args, enabled, &output)
		if code != 1 || err == nil || output.Len() != 0 ||
			strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("unsafe profile selection: %d %v", code, err)
		}
		if enabled == "true" && !strings.Contains(err.Error(), "--profile compatibility|api-only") {
			t.Fatalf("unknown profile did not produce bounded usage: %v", err)
		}
		if enabled != "true" && !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("profile parsed before gate: %v", err)
		}
	}
}
