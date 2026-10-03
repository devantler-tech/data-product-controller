package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRegistryTargetRejectsRetiringRevision prevents port-forward from attaching to an old rollout pod.
func TestRegistryTargetRejectsRetiringRevision(t *testing.T) {
	filter, err := filepath.Abs("../registry-target.jq")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, revision, owner, status string
		deleting                      bool
		success                       bool
	}{
		{"current revision", "2", "new-rs", "True", false, true},
		{"old ready pod", "1", "old-rs", "True", false, false},
		{"terminating current pod", "2", "new-rs", "True", true, false},
		{"current pod not ready", "2", "new-rs", "False", false, false},
		{"unrelated owner", "2", "foreign-rs", "True", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, value any) string {
				t.Helper()
				b, e := json.Marshal(value)
				if e != nil {
					t.Fatal(e)
				}
				p := filepath.Join(dir, name)
				if e = os.WriteFile(p, b, 0o600); e != nil {
					t.Fatal(e)
				}
				return p
			}
			deployment := write(
				"deployment.json",
				map[string]any{
					"metadata": map[string]any{
						"uid":         "deployment-uid",
						"annotations": map[string]string{"deployment.kubernetes.io/revision": "2"},
					},
					"spec": map[string]any{
						"template": map[string]any{
							"spec": map[string]any{
								"containers": []any{
									map[string]any{
										"name":  "controller",
										"image": "current-image",
										"ports": []any{
											map[string]any{
												"name":          "registry",
												"containerPort": 8082,
											},
											map[string]any{
												"name":          "metrics",
												"containerPort": 8080,
											},
										},
									},
								},
							},
						},
					},
				},
			)
			var rs []any
			for _, version := range []string{"1", "2"} {
				uid := "new-rs"
				if version == "1" {
					uid = "old-rs"
				}
				rs = append(
					rs,
					map[string]any{
						"metadata": map[string]any{
							"uid": uid,
							"annotations": map[string]string{
								"deployment.kubernetes.io/revision": version,
							},
							"ownerReferences": []any{
								map[string]any{
									"controller": true,
									"kind":       "Deployment",
									"uid":        "deployment-uid",
								},
							},
						},
					},
				)
			}
			replicasets := write("rs.json", map[string]any{"items": rs})
			metadata := map[string]any{
				"name": "current-pod",
				"ownerReferences": []any{
					map[string]any{"controller": true, "kind": "ReplicaSet", "uid": tc.owner},
				},
			}
			if tc.deleting {
				metadata["deletionTimestamp"] = "2026-10-02T00:00:00Z"
			}
			pods := write(
				"pods.json",
				map[string]any{
					"items": []any{
						map[string]any{
							"metadata": metadata,
							"spec": map[string]any{
								"containers": []any{
									map[string]any{"name": "controller", "image": "current-image"},
								},
							},
							"status": map[string]any{
								"conditions": []any{
									map[string]any{"type": "Ready", "status": tc.status},
								},
							},
						},
					},
				},
			)
			output, e := exec.Command("jq", "-n", "--slurpfile", "deployment", deployment, "--slurpfile", "replicasets", replicasets, "--slurpfile", "pods", pods, "-f", filter).
				CombinedOutput()
			if (e == nil) != tc.success {
				t.Fatalf("selection success=%v, want %v: %s", e == nil, tc.success, output)
			}
			if tc.success {
				var target struct {
					Pod  string
					Port int
				}
				if json.Unmarshal(output, &target) != nil || target.Pod != "current-pod" ||
					target.Port != 8082 {
					t.Fatal("wrong live registry target")
				}
			}
		})
	}
}
