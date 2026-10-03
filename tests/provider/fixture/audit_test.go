package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

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
