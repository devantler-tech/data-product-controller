package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The independent sample consumes real complete and incomplete traces without controller imports.
func TestLineageIndependentConsumer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		products int
		complete string
	}{
		{"complete", 2, "complete"},
		{"incomplete", 1, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := traceProduct("root", "producer")
			reader, _ := traceReader(t, root)
			if tc.products == 2 {
				reader, _ = traceReader(t, root, traceProduct("producer"))
			}
			_, body := readTrace(t, traceHandler(reader))
			directory := t.TempDir()
			path := filepath.Join(directory, "trace.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile("../../docs/examples/lineage-client/main.go")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "main.go"), source, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "go", "run", "main.go", "trace.json")
			cmd.Dir = directory
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("independent consumer failed: %v %s", err, output)
			}
			for _, want := range []string{"products/root", tc.complete, "producer", "input-0"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("consumer missing %s: %s", want, output)
				}
			}
		})
	}
}
