package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestAuditRejectsSubresourceReads(t *testing.T) {
	for _, subresource := range []string{"", "status", "scale", "proxy"} {
		for _, stage := range []string{"ResponseComplete", "RequestReceived"} {
			t.Run(subresource+stage, func(t *testing.T) {
				legitimate := map[string]any{
					"stage": "ResponseComplete",
					"verb":  "get",
					"user":  map[string]any{"username": "system:serviceaccount:products:dpc"},
					"objectRef": map[string]any{
						"apiGroup":  "database.arangodb.com",
						"namespace": "products",
						"resource":  "arangodeployments",
						"name":      "lineage",
					},
				}
				first, err := json.Marshal(legitimate)
				if err != nil {
					t.Fatal(err)
				}
				legitimate["stage"] = stage
				legitimate["responseStatus"] = map[string]any{"code": 403}
				legitimate["objectRef"].(map[string]any)["subresource"] = subresource
				second, err := json.Marshal(legitimate)
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
				cmd.Stdin = strings.NewReader(string(first) + "\n" + string(second))
				output, err := cmd.CombinedOutput()
				if (err == nil) != (subresource == "") {
					t.Fatalf(
						"subresource %q %s accepted=%v: %s",
						subresource,
						stage,
						err == nil,
						output,
					)
				}
			})
		}
	}
}
