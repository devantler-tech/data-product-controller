package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRegistryForwardUsesResolvedPodPort checks real kubectl output and rejects unsafe listeners.
func TestRegistryForwardUsesResolvedPodPort(t *testing.T) {
	helper, err := filepath.Abs("../budget.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, line, pid string
		wantSuccess     bool
	}{
		{"resolved registry port", "Forwarding from 127.0.0.1:43127 -> 8082", "$$", true},
		{"service port is not the resolved target", "Forwarding from 127.0.0.1:43127 -> 80", "$$", false},
		{"metrics port is not registry", "Forwarding from 127.0.0.1:43127 -> 8080", "$$", false},
		{"wrong pod port", "Forwarding from 127.0.0.1:43127 -> 8081", "$$", false},
		{"non-loopback address", "Forwarding from 0.0.0.0:43127 -> 8080", "$$", false},
		{"zero local port", "Forwarding from 127.0.0.1:0 -> 8080", "$$", false},
		{"invalid local port", "Forwarding from 127.0.0.1:65536 -> 8080", "$$", false},
		{"process unavailable", "Forwarding from 127.0.0.1:43127 -> 8080", "not-a-pid", false},
		{"partial output", "Forwarding from 127.0.0.1:", "$$", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "forward.log"), []byte(tt.line+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", "-c",
				`set -euo pipefail; work_deadline=$((SECONDS+60)); test_dir=$1; started_at=$SECONDS; source "$2"; registry_target_port=8082; registry_forward_pid=`+tt.pid+`; registry_forward_ready; test "$registry_port" = 43127`,
				"forward-test", dir, helper)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil || (err == nil) != tt.wantSuccess {
				t.Fatalf("success = %v, want %v: %s", err == nil, tt.wantSuccess, output)
			}
		})
	}
}

// TestRegistryForwardDiscardsPreviousListener forces asynchronous startup to overlap a stale listener log.
func TestRegistryForwardDiscardsPreviousListener(t *testing.T) {
	helper, err := filepath.Abs("../budget.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "forward.log"), []byte("Forwarding from 127.0.0.1:43127 -> 8082\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stub := `#!/usr/bin/env bash
set -euo pipefail
sleep 0.15
printf '%s\n' 'Forwarding from 127.0.0.1:45209 -> 8082'
sleep 10
`
	if err = os.WriteFile(filepath.Join(dir, "kubectl"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
test_dir=$1; started_at=$SECONDS; work_deadline=$((SECONDS+4)); phase_deadline=$work_deadline
PATH="$test_dir:$PATH"; export PATH
source "$2"
trap 'kill "$registry_forward_pid" 2>/dev/null || true; wait "$registry_forward_pid" 2>/dev/null || true' EXIT
registry_pod=current-pod; registry_target_port=8082
start_registry_forward
test "$registry_port" = 45209
`, "forward-race-test", dir, helper)
	output, err := command.CombinedOutput()
	if err != nil || ctx.Err() != nil {
		t.Fatalf("fresh registry listener was not selected: %v: %s", err, output)
	}
}
