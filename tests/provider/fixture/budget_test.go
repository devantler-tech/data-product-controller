package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProviderPhaseBudget checks deadline clamping and rejects incomplete observations.
func TestProviderPhaseBudget(t *testing.T) {
	helper, err := filepath.Abs("../budget.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, commands string
		wantSuccess    bool
	}{
		{"valid phase", `phase acceptance 30; remaining`, true},
		{"zero budget", `phase acceptance 0`, false},
		{"negative budget", `phase acceptance -1`, false},
		{"invalid budget", `phase acceptance invalid`, false},
		{"expired outer budget", `work_deadline=0; phase acceptance 30`, false},
		{"clamped to outer deadline", `work_deadline=$((SECONDS+5)); phase acceptance 60; test "$phase_deadline" -eq "$work_deadline"`, true},
		{"partial failed observation", `phase_deadline=0; plausible() { echo '{"ready":true}'; return 7; }; wait_for acceptance plausible`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(
				ctx,
				"bash",
				"-c",
				`set -euo pipefail; work_deadline=$((SECONDS+60)); test_dir=$1; started_at=$SECONDS; source "$2"; `+tt.commands,
				"budget-test",
				t.TempDir(),
				helper,
			)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("budget helper did not terminate")
			}
			if (err == nil) != tt.wantSuccess {
				t.Fatalf("success = %v, want %v: %s", err == nil, tt.wantSuccess, output)
			}
			if !tt.wantSuccess && strings.Contains(string(output), "PASS:") {
				t.Fatal("incomplete evidence was reported as passing")
			}
		})
	}
}

// TestProviderBudgetRejectsShellFunctions prevents assertions from bypassing process deadlines.
func TestProviderBudgetRejectsShellFunctions(t *testing.T) {
	helper, err := filepath.Abs("../budget.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c",
		`set -euo pipefail; work_deadline=$((SECONDS+60)); test_dir=$1; started_at=$SECONDS; source "$2"; phase acceptance 30; kube() { echo must-not-run; }; bounded kube exec writer`,
		"budget-test", t.TempDir(), helper)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(output), "bounded requires an external executable") {
		t.Fatalf("expected explicit rejection before timeout executes: %v: %s", err, output)
	}
	if strings.Contains(string(output), "must-not-run") {
		t.Fatal("shell function executed outside the timeout boundary")
	}
}
