package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLifecyclePhasePreservesTimeAcrossDateImplementations protects the same-second observation boundary.
func TestLifecyclePhasePreservesTimeAcrossDateImplementations(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fractional, seconds, want string
		wantError                       bool
	}{
		{"GNU fractional", "100.500000000", "100", "100.500000000", false},
		{"BSD unsupported nanos", "100.N", "100", "101", false},
		{"invalid clock", "100.N", "not-a-timestamp", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), "bash", "-c", `
source tests/source/source-lifecycle.sh
date() {
  case "$1" in
    +%s.%N) printf '%s\n' "$FRACTIONAL" ;;
    +%s) printf '%s\n' "$SECONDS_VALUE" ;;
    *) return 91 ;;
  esac
}
lifecycle_phase`)
			command.Dir = root
			command.Env = append(os.Environ(), "repo_root="+root, "test_dir="+t.TempDir(),
				"FRACTIONAL="+tc.fractional, "SECONDS_VALUE="+tc.seconds)
			output, err := command.CombinedOutput()
			if (err != nil) != tc.wantError ||
				(!tc.wantError && strings.TrimSpace(string(output)) != tc.want) {
				t.Fatalf("phase=%q error=%v, want=%q error=%v", output, err, tc.want, tc.wantError)
			}
		})
	}
}
