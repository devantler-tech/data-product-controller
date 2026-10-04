package registry

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLineageReaderRejectsFIFO runs the actual independent reader without a pipe writer.
func TestLineageReaderRejectsFIFO(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	source, err := os.ReadFile("../../docs/examples/lineage-client/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("main.go", source, 0o600); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", "reader", "main.go")
	build.Dir = directory
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build reader: %v %s", err, output)
	}
	fifo := filepath.Join(directory, "trace.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, "./reader", "trace.json")
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if bounded.Err() != nil {
		t.Fatal("lineage reader blocked on a named pipe without a writer")
	}
	if err == nil || !strings.Contains(
		string(output), "Could not read a supported dependency trace.",
	) {
		t.Fatalf("non-regular trace was not rejected: %v %s", err, output)
	}
}
