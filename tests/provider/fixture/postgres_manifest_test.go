package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPostgresHarnessRendersActualModelDescriptors executes the harness's real yq expression and verifies every published model and query route.
func TestPostgresHarnessRendersActualModelDescriptors(t *testing.T) {
	script, err := os.ReadFile("../postgres.sh")
	if err != nil {
		t.Fatal(err)
	}
	const start = "\nfor model in sql document graph; do\n\texport DPC_POSTGRES_MODEL=$model\n"
	_, rendering, found := strings.Cut(string(script), start)
	if !found {
		t.Fatal("publication rendering missing")
	}
	rendering, _, found = strings.Cut(rendering, "\nwait_for 'current controller publishes")
	if !found {
		t.Fatal("publication rendering incomplete")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
kube() {
	[[ "$*" == 'apply -f -' ]]
	yq -o=json '.' - >"$DPC_PRODUCT_DIR/$DPC_POSTGRES_MODEL.json"
}
`+start+rendering)
	command.Env = append(os.Environ(), "repo_root="+root, "DPC_PRODUCT_DIR="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual model rendering: %v %s", err, output)
	}
	for model, path := range map[string]string{"sql": "/api/rows", "document": "/api/documents", "graph": "/api/lineage"} {
		body, err := os.ReadFile(filepath.Join(directory, model+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var product struct {
			Metadata struct{ Name string }
			Spec     struct {
				ID     string
				Source struct {
					Adapter             string
					Engine              struct{ APIVersion, Type, Provider string }
					ResourceRef         struct{ APIVersion, Kind, Name string }
					ConnectionSecretRef struct{ Name string }
				}
				Outputs []struct{ URL, ContractURL string }
			}
		}
		if err := json.Unmarshal(body, &product); err != nil {
			t.Fatal(err)
		}
		adapter, provider, publication := "cnpg-hybrid/v1", "cnpg-hybrid", "warehouse-"+model+"-reader"
		if model == "sql" {
			adapter, provider, publication = "cnpg/v1", "native", "warehouse-app"
		}
		base := "https://postgres-query-" + model + ".products.svc.cluster.local:8443"
		source := product.Spec.Source
		if product.Metadata.Name != "postgres-"+model+"-product" ||
			product.Spec.ID != "urn:example:postgres-"+model ||
			source.Adapter != adapter ||
			source.Engine.Provider != provider ||
			source.Engine.Type != model ||
			source.Engine.APIVersion != "engine-provider/v1" ||
			source.ConnectionSecretRef.Name != publication ||
			source.ResourceRef.APIVersion != "postgresql.cnpg.io/v1" ||
			source.ResourceRef.Kind != "Cluster" ||
			source.ResourceRef.Name != "warehouse" ||
			len(product.Spec.Outputs) != 1 ||
			product.Spec.Outputs[0].URL != base+path ||
			product.Spec.Outputs[0].ContractURL != base+"/openapi.json" {
			t.Fatalf(
				"%s descriptor does not bind the intended source, reader and HTTPS contract: %s",
				model,
				body,
			)
		}
	}
}
