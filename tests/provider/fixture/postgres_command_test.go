package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPostgresProbeSelectsExplicitModel verifies the executable command without an ambient model or shell utility.
func TestPostgresProbeSelectsExplicitModel(t *testing.T) {
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	t.Setenv("POSTGRES_MODEL", "unapproved")
	for _, model := range []string{"sql", "document", "graph"} {
		os.Args = []string{"fixture", "postgres-probe", model, "unapproved"}
		if err := run(); err == nil || err.Error() != "unsupported PostgreSQL probe mode" {
			t.Fatalf("%s explicit model did not reach bounded probe validation: %v", model, err)
		}
	}
}

// TestPostgresCommandsRequireExplicitModel prevents a missing argument from silently selecting an ambient model.
func TestPostgresCommandsRequireExplicitModel(t *testing.T) {
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	t.Setenv("POSTGRES_MODEL", "sql")
	for _, mode := range []string{"postgres-seed", "postgres-probe"} {
		os.Args = []string{"fixture", mode}
		if err := run(); err == nil || err.Error() != "PostgreSQL model argument required" {
			t.Fatalf("%s missing model accepted or entered the data plane: %v", mode, err)
		}
	}
	for _, model := range []string{"", "postgres", "POSTGRES_MODEL=graph", "graph;exit"} {
		os.Args = []string{"fixture", "postgres-seed", model}
		if err := run(); err == nil || err.Error() != "unsupported PostgreSQL model" {
			t.Fatalf("unsupported explicit model %q entered the data plane: %v", model, err)
		}
	}
}

// TestPostgresConsumerExecutesFixtureDirectly runs the actual harness helper against the distroless command contract.
func TestPostgresConsumerExecutesFixtureDirectly(t *testing.T) {
	script, err := os.ReadFile("../postgres.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, helper, found := strings.Cut(string(script), "\nquery_model() {\n")
	if !found {
		t.Fatal("consumer helper missing")
	}
	helper, _, found = strings.Cut(helper, "\n}\n")
	if !found {
		t.Fatal("consumer helper incomplete")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
kube() {
	[[ $1 == exec && $2 == postgres-consumer && $3 == -- && $4 == /fixture &&
		$5 == postgres-probe && $6 == "$expected_model" && ${7:-} == "$expected_mode" ]]
}
`+"\nquery_model() {\n"+helper+"\n}\n"+`
for expected_model in sql document graph; do
	for expected_mode in '' contract denied outage; do
		if [[ -n $expected_mode ]]; then
			query_model "$expected_model" "$expected_mode"
		else
			query_model "$expected_model"
		fi
	done
done
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("distroless consumer command: %v %s", err, output)
	}
}
