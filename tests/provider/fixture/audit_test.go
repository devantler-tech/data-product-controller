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
					"stage": "ResponseComplete", "verb": verb,
					"user":           map[string]any{"username": "system:serviceaccount:products:dpc"},
					"responseStatus": map[string]any{"code": 403},
				})
				if err != nil {
					t.Fatal(err)
				}
				input.Write(data)
				input.WriteByte('\n')
			}
			cmd := exec.Command("jq", "-se", "-f", "../audit-read-only.jq")
			cmd.Stdin = strings.NewReader(input.String())
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.want {
				t.Fatalf("read-only=%v, want %v: %s", err == nil, tc.want, output)
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
				mounts = append(mounts, map[string]any{"name": "audit", "mountPath": "/audit", "readOnly": false})
			}
			hostPath := "/audit"
			if !tc.host {
				hostPath = "/unrelated"
			}
			value := map[string]any{"items": []any{map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "kube-apiserver", "command": command, "volumeMounts": mounts}},
				"volumes":    []any{map[string]any{"name": "audit", "hostPath": map[string]any{"path": hostPath, "type": "Directory"}}},
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
