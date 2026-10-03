package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestAuditReadOnlyRequiresObservedGets rejects attempts to mutate or broaden source access.
func TestAuditReadOnlyRequiresObservedGets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		verbs []string
		want  bool
	}{
		{"observed exact reads", []string{"get", "get"}, true},
		{"no evidence", nil, false},
		{"create", []string{"get", "create"}, false},
		{"update", []string{"get", "update"}, false},
		{"patch", []string{"get", "patch"}, false},
		{"delete", []string{"get", "delete"}, false},
		{"delete collection", []string{"get", "deletecollection"}, false},
		{"list", []string{"get", "list"}, false},
		{"watch", []string{"get", "watch"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input strings.Builder
			for _, verb := range tc.verbs {
				data, err := json.Marshal(map[string]any{
					"stage": "ResponseComplete",
					"verb":  verb,
					"user": map[string]any{
						"username": "system:serviceaccount:products:dpc",
					},
					"responseStatus": map[string]any{"code": 403},
					"objectRef": map[string]any{
						"apiGroup":  "database.arangodb.com",
						"namespace": "products",
						"resource":  "arangodeployments",
						"name":      "lineage",
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				input.Write(data)
				input.WriteByte('\n')
			}
			cmd := exec.Command(
				"jq",
				"-se",
				"--argjson",
				"expected",
				graphAuditObjects,
				"-f",
				"../audit-read-only.jq",
			)
			cmd.Stdin = strings.NewReader(input.String())
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.want {
				t.Fatalf("read-only=%v, want %v: %s", err == nil, tc.want, output)
			}
		})
	}
}

const graphAuditObjects = `[{"group":"database.arangodb.com","resource":"arangodeployments","name":"lineage"},{"group":"","resource":"secrets","name":"lineage-reader"}]`

// TestAuditRejectsUndeclaredGets checks the actual filter against broader metadata reads, including denied requests.
func TestAuditRejectsUndeclaredGets(t *testing.T) {
	for _, tc := range []struct {
		name, group, resource, namespace, objectName string
		want                                         bool
	}{
		{"declared source", "database.arangodb.com", "arangodeployments", "products", "lineage", true},
		{"declared publication", "", "secrets", "products", "lineage-reader", true},
		{"other source", "database.arangodb.com", "arangodeployments", "products", "unrelated", false},
		{"other Secret", "", "secrets", "products", "unrelated", false},
		{"other namespace", "", "secrets", "other", "lineage-reader", false},
		{"other engine", "psmdb.percona.com", "perconaservermongodbs", "products", "lineage", false},
		{"unnamed object", "", "secrets", "products", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := map[string]any{
				"stage":          "ResponseComplete",
				"verb":           "get",
				"user":           map[string]any{"username": "system:serviceaccount:products:dpc"},
				"responseStatus": map[string]any{"code": 403},
				"objectRef": map[string]any{
					"apiGroup":  tc.group,
					"resource":  tc.resource,
					"namespace": tc.namespace,
					"name":      tc.objectName,
				},
			}
			input, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(
				"jq",
				"-se",
				"--argjson",
				"expected",
				graphAuditObjects,
				"-f",
				"../audit-read-only.jq",
			)
			cmd.Stdin = strings.NewReader(string(input))
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.want {
				t.Fatalf("declared GET accepted=%v want %v: %s", err == nil, tc.want, output)
			}
		})
	}
}

// TestAuditServerRejectsInactiveConfiguration prevents zero reads from proving a disabled observer.
func TestAuditServerRejectsInactiveConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		policy, log, mount, host bool
		want                     bool
	}{
		{"active audit server", true, true, true, true, true},
		{"patch ignored", false, false, true, true, false},
		{"policy flag missing", false, true, true, true, false},
		{"log flag missing", true, false, true, true, false},
		{"volume not mounted", true, true, false, true, false},
		{"wrong host directory", true, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := []string{"kube-apiserver"}
			if tc.policy {
				command = append(command, "--audit-policy-file=/audit/policy.yaml")
			}
			if tc.log {
				command = append(command, "--audit-log-path=/audit/log.json")
			}
			mounts := []any{}
			if tc.mount {
				mounts = append(
					mounts,
					map[string]any{"name": "audit", "mountPath": "/audit", "readOnly": false},
				)
			}
			hostPath := "/audit"
			if !tc.host {
				hostPath = "/unrelated"
			}
			value := map[string]any{"items": []any{map[string]any{"spec": map[string]any{
				"containers": []any{
					map[string]any{
						"name":         "kube-apiserver",
						"command":      command,
						"volumeMounts": mounts,
					},
				},
				"volumes": []any{
					map[string]any{
						"name":     "audit",
						"hostPath": map[string]any{"path": hostPath, "type": "Directory"},
					},
				},
			}}}}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("jq", "-e", "-f", "../audit-server.jq")
			cmd.Stdin = strings.NewReader(string(data))
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.want {
				t.Fatalf("audit active=%v, want %v: %s", err == nil, tc.want, output)
			}
		})
	}
}
