package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestProviderRetryNeverStartsAfterExpiry checks both initial and retried phase boundaries.
func TestProviderRetryNeverStartsAfterExpiry(t *testing.T) {
	for _, boundary := range []string{"initial", "retry"} {
		t.Run(boundary, func(t *testing.T) {
			outputDir := t.TempDir()
			script := `set -euo pipefail
work_deadline=60; test_dir=$1; started_at=$SECONDS
source "$2"
calls=0; phase_deadline=60
if [[ $3 == initial ]]; then phase_deadline=0; fi
attempt() { calls=$((calls+1)); return 1; }
sleep() { phase_deadline=0; }
if wait_for expired attempt; then exit 7; fi
if [[ $3 == initial ]]; then test "$calls" -eq 0; else test "$calls" -eq 1; fi
`
			cmd := exec.Command(
				"bash",
				"-c",
				script,
				"--",
				outputDir,
				filepath.Join("..", "budget.sh"),
				boundary,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("expired %s callback ran: %s", boundary, output)
			}
		})
	}
}
