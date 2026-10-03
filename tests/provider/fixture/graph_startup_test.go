package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestGraphStartupUsesOperatorConditions rejects incomplete bootstrap without requiring invented status fields.
func TestGraphStartupUsesOperatorConditions(t *testing.T) {
	script, err := os.ReadFile("../arango.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, function, found := strings.Cut(string(script), "\ndatabase_ready() {\n")
	if !found {
		t.Fatal("database readiness function missing")
	}
	body, _, found := strings.Cut(function, "\n}\n")
	if !found {
		t.Fatal("database readiness function incomplete")
	}
	fixture, err := os.ReadFile("../../source/fixtures/arango-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, condition string
		ready           bool
	}{
		{"operator ready", "", true},
		{"bootstrap not completed", "BootstrapCompleted", false},
		{"bootstrap unsuccessful", "BootstrapSucceded", false},
		{"deployment not ready", "Ready", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var object map[string]any
			if err := json.Unmarshal(fixture, &object); err != nil {
				t.Fatal(err)
			}
			status := object["status"].(map[string]any)
			for _, condition := range status["conditions"].([]any) {
				c := condition.(map[string]any)
				if c["type"] == tc.condition {
					c["status"] = "False"
				}
			}
			encoded, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(
				ctx,
				"bash",
				"-c",
				"set -euo pipefail; kube() { cat; }; database_ready() {\n"+body+"\n}; database_ready",
			)
			command.Stdin = strings.NewReader(string(encoded))
			output, err := command.CombinedOutput()
			if ctx.Err() != nil || (err == nil) != tc.ready {
				t.Fatalf("readiness=%v want %v: %v %s", err == nil, tc.ready, err, output)
			}
		})
	}
}

// TestGraphBootstrapSecretIncludesUsername protects the operator's username/password Secret contract.
func TestGraphBootstrapSecretIncludesUsername(t *testing.T) {
	script, err := os.ReadFile("../arango.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, loop, found := strings.Cut(string(script), "for identity in reader writer root; do\n")
	if !found {
		t.Fatal("credential creation loop missing")
	}
	loop, _, found = strings.Cut(loop, "\ndone\n")
	if !found {
		t.Fatal("credential creation loop incomplete")
	}
	directory := t.TempDir()
	capture := directory + "/arguments"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
test_dir=$DPC_TEST_DIRECTORY
kube() { printf '%s\t' "$@" >> "$DPC_TEST_CAPTURE"; printf '\n' >> "$DPC_TEST_CAPTURE"; }
for identity in reader writer root; do
`+loop+"\ndone")
	command.Env = append(os.Environ(), "DPC_TEST_DIRECTORY="+directory, "DPC_TEST_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("credential creation failed: %v %s", err, output)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, "\tlineage-root-password\t") {
			if !strings.Contains(line, "\t--type=kubernetes.io/basic-auth\t") ||
				!strings.Contains(line, "\t--from-literal=username=root\t") ||
				!strings.Contains(line, "\t--from-file=password=") {
				t.Fatal("bootstrap Secret lacks the operator's username/password format")
			}
			return
		}
	}
	t.Fatal("root Secret not created")
}
