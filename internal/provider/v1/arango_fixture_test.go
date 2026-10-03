package v1

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	arango "github.com/arangodb/kube-arangodb/pkg/apis/deployment/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/yaml"
)

// The upstream operator uses these labels for both Pod inspection and Service routing.
// An override can leave a running server invisible to reconciliation and its clients.
func TestArangoFixturePreservesOperatorSelectors(t *testing.T) {
	data, err := os.ReadFile("../../../tests/provider/arango.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Metadata struct{ Name string }
		Spec     struct {
			Single struct{ Labels map[string]string }
		}
	}
	if err := yaml.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	reserved := labels.Set{
		"app":               "arangodb",
		"arango_deployment": fixture.Metadata.Name,
		"role":              "single",
	}
	podLabels := maps.Clone(reserved)
	maps.Copy(podLabels, fixture.Spec.Single.Labels)
	if !labels.SelectorFromSet(reserved).Matches(podLabels) {
		t.Fatal("fixture overrides labels required by the operator and database Service")
	}
}

// The accepted outage selector survives null defaulting. Exercise the actual Bash recovery patch
// and the pinned operator's default restoration to prove it selects the observed schedulable node.
func TestArangoFixtureRestoresConcreteScheduling(t *testing.T) {
	script, err := os.ReadFile("../../../tests/provider/arango.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(script), "\nrestore_scheduling() {\n")
	if !found {
		t.Fatal("scheduling recovery missing")
	}
	body, _, found = strings.Cut(body, "\n}\n")
	if !found {
		t.Fatal("scheduling recovery incomplete")
	}
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "observed hostname", false: "missing hostname"}[valid], func(t *testing.T) {
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
			command.Env = append(os.Environ(), "DPC_TEST_NODE="+node, "DPC_TEST_CAPTURE="+capture)
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
			accepted := arango.ServerGroupSpec{
				NodeSelector: map[string]string{"data.devantler.tech/acceptance-node": "unavailable"},
			}
			cleared := arango.ServerGroupSpec{}
			cleared.SetDefaultsFrom(accepted)
			if labels.SelectorFromSet(cleared.NodeSelector).Matches(labels.Set{"kubernetes.io/hostname": "observed-node"}) {
				t.Fatal("regression fixture does not reproduce the retained outage selector")
			}
			recovered := arango.ServerGroupSpec{NodeSelector: operations[0].Value}
			recovered.SetDefaultsFrom(accepted)
			if !labels.SelectorFromSet(recovered.NodeSelector).Matches(labels.Set{"kubernetes.io/hostname": "observed-node"}) ||
				len(recovered.NodeSelector) != 1 {
				t.Fatal("operator defaulting retained the outage selector")
			}
		})
	}
}

// The upstream certificate defaults omit the cluster domain unless explicitly configured.
// Exercise its actual alternate-name parser and Go's hostname verification for the fixed origin.
func TestArangoFixtureCertificateCoversClientOrigin(t *testing.T) {
	data, err := os.ReadFile("../../../tests/provider/arango.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var fixture arango.ArangoDeployment
	if err := yaml.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	names, _, _, err := fixture.Spec.TLS.GetParsedAltNames()
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{DNSNames: names}
	if err := certificate.VerifyHostname("lineage.products.svc.cluster.local"); err != nil {
		t.Fatalf("operator certificate cannot authenticate the fixed client origin: %v", err)
	}
}
