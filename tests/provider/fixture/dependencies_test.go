package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestProviderAvoidsOpenPGP enforces the compiled-package evidence supporting
// the single expiring module-level advisory exception. Loading failure is fatal.
func TestProviderAvoidsOpenPGP(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "go", "list", "-deps", "-test", "./...").Output()
	if err != nil || len(output) == 0 {
		t.Fatalf("compiled dependency observation failed: %v", err)
	}
	for pkg := range strings.SplitSeq(string(output), "\n") {
		if pkg == "golang.org/x/crypto/openpgp" || strings.HasPrefix(pkg, "golang.org/x/crypto/openpgp/") {
			t.Fatal("unsafe OpenPGP package invalidates the advisory exception")
		}
	}
}
