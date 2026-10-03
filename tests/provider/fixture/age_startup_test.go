package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestAGERestartReportsServerFailure executes both actual restart paths with a failed pg_ctl.
func TestAGERestartReportsServerFailure(t *testing.T) {
	t.Parallel()
	script, err := os.ReadFile("../age-image-bootstrap.sh")
	if err != nil {
		t.Fatal(err)
	}
	restarts := regexp.MustCompile(`(?ms)^pg_ctl -D "\$PGDATA" -m fast -w stop >/dev/null\n(.*?)^\[\[ \$\(admin`).
		FindAllSubmatch(script, -1)
	if len(restarts) != 2 {
		t.Fatalf("found %d restart paths, want both preload transitions", len(restarts))
	}
	helper := regexp.MustCompile(`(?ms)^start_server\(\) \{\n.*?^\}`).Find(script)
	for index, restart := range restarts {
		name := []string{"without preload", "restore preload"}[index]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logPath := filepath.Join(t.TempDir(), "postgres.log")
			if err := os.WriteFile(
				logPath,
				[]byte("synthetic PostgreSQL restart failure\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			program := `set -euo pipefail
PGDATA=unused
postgres_log=$DPC_TEST_POSTGRES_LOG
pg_ctl() { return 1; }
` + string(helper) + "\n" + string(restart[1])
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", "-c", program)
			command.Env = append(os.Environ(), "DPC_TEST_POSTGRES_LOG="+logPath)
			output, runErr := command.CombinedOutput()
			if runErr == nil {
				t.Fatal("failed PostgreSQL restart was accepted")
			}
			if !strings.Contains(string(output), "synthetic PostgreSQL restart failure") {
				t.Fatalf("restart discarded its server diagnostics: %s", output)
			}
		})
	}
}
