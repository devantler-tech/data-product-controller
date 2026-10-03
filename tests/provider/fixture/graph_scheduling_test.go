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

func TestGraphFixtureRestoresConcreteScheduling(t *testing.T) {
	body := graphLifecycleBody(t, "restore_scheduling")
	for _, valid := range []bool{true, false} {
		t.Run(
			map[bool]string{true: "observed hostname", false: "missing hostname"}[valid],
			func(t *testing.T) {
				capture := t.TempDir() + "/patch"
				node := `{"metadata":{"labels":{}}}`
				if valid {
					node = `{"metadata":{"labels":{"kubernetes.io/hostname":"observed-node"}}}`
				}
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
control_node=observed-node
kubectl() { printf '%s\n' "$DPC_TEST_NODE"; }
kube() { printf '%s\n%s' "$4" "$6" > "$DPC_TEST_CAPTURE"; }
restore_scheduling() {
`+body+"\n}\nrestore_scheduling")
				command.Env = append(
					os.Environ(),
					"DPC_TEST_NODE="+node,
					"DPC_TEST_CAPTURE="+capture,
				)
				output, executionErr := command.CombinedOutput()
				if (executionErr == nil) != valid {
					t.Fatalf("recovery: %v %s", executionErr, output)
				}
				data, readErr := os.ReadFile(capture)
				if !valid {
					if !os.IsNotExist(readErr) {
						t.Fatal("unknown node identity caused a patch")
					}
					return
				}
				if readErr != nil {
					t.Fatal(readErr)
				}
				kind, patch, _ := strings.Cut(string(data), "\n")
				var operations []struct {
					Op, Path string
					Value    map[string]string
				}
				if kind != "--type=json" || json.Unmarshal([]byte(patch), &operations) != nil ||
					len(operations) != 1 || operations[0].Op != "replace" ||
					operations[0].Path != "/spec/single/nodeSelector" {
					t.Fatal("recovery must replace the entire selector")
				}
				selector := operations[0].Value
				if len(selector) != 1 || selector["kubernetes.io/hostname"] != "observed-node" {
					t.Fatal("recovery retained the outage selector or selected an unobserved node")
				}
			},
		)
	}
}
