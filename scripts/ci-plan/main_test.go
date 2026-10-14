package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestBrowserPlan(t *testing.T) {
	cases := []struct {
		name, path, mode string
		want             string
	}{
		{"existing browser test", "internal/browser/theme_browser_test.go", "modify", "browser"},
		{"production dependency", "internal/registry/ui.go", "modify", "full"},
		{"embedded asset", "web/kit.js", "modify", "full"},
		{"workflow", ".github/workflows/ci.yaml", "modify", "full"},
		{"module", "go.mod", "modify", "full"},
		{"classifier widened", "scripts/ci-plan/main.go", "modify", "full"},
		{"new browser file", "internal/browser/new_browser_test.go", "add", "full"},
		{"deleted browser file", "internal/browser/theme_browser_test.go", "delete", "full"},
		{"unknown", "unknown.txt", "add", "full"},
		{"browser rename", "internal/browser/theme_browser_test.go", "rename", "full"},
		{"symlink", "internal/browser/theme_browser_test.go", "symlink", "full"},
		{"new first party import", "internal/browser/theme_browser_test.go", "import", "full"},
		{"removed browser build tag", "internal/browser/theme_browser_test.go", "untag", "full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, base := fixture(t)
			p := filepath.Join(repo, tc.path)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			switch tc.mode {
			case "delete":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if err := os.Rename(
					p,
					filepath.Join(repo, "internal/browser/renamed_browser_test.go"),
				); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../go.mod", p); err != nil {
					t.Fatal(err)
				}
			case "import":
				write(
					t,
					p,
					"//go:build browser\npackage browser_test\nimport _ \"github.com/devantler-tech/data-product-controller/internal/httpsource\"\n",
				)
			case "untag":
				write(t, p, "package browser_test\n")
			default:
				b := readFixture(t, repo, tc.path)
				write(t, p, string(b)+"\n// changed\n")
			}
			gitTest(t, repo, "add", "--", tc.path)
			if tc.mode == "rename" {
				gitTest(t, repo, "add", "--", "internal/browser/renamed_browser_test.go")
			}
			gitTest(t, repo, "commit", "-qm", "candidate")
			head := gitTest(t, repo, "rev-parse", "HEAD")
			got, err := classify(repo, base, head)
			if got != tc.want {
				t.Fatalf("profile=%s err=%v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestPlanRejectsIncompleteInventoryAndUnknownBase(t *testing.T) {
	repo, base := fixture(t)
	write(
		t,
		filepath.Join(repo, "internal/browser/omitted_browser_test.go"),
		"//go:build browser\npackage browser_test\n",
	)
	gitTest(t, repo, "add", "--", "internal/browser/omitted_browser_test.go")
	gitTest(t, repo, "commit", "-qm", "extra inventory")
	incomplete := gitTest(t, repo, "rev-parse", "HEAD")
	write(
		t,
		filepath.Join(repo, browserFiles[0]),
		"//go:build browser\npackage browser_test\n// modified\n",
	)
	gitTest(t, repo, "add", "--", browserFiles[0])
	gitTest(t, repo, "commit", "-qm", "browser edit")
	head := gitTest(t, repo, "rev-parse", "HEAD")
	for _, b := range []string{base, incomplete, strings.Repeat("0", 40), "HEAD", ""} {
		got, err := classify(repo, b, head)
		if got != "full" {
			t.Fatalf("base %q shortened to %q (err %v)", b, got, err)
		}
	}
	got, err := classify(repo, base, base)
	if got != "full" || err != nil {
		t.Fatalf("empty diff: %s %v", got, err)
	}
}

func TestRequiredPlanResults(t *testing.T) {
	for _, profile := range []string{"browser", "full"} {
		needs := validNeeds(profile)
		if err := checkResults(profile, needs); err != nil {
			t.Fatal(err)
		}
		for _, id := range jobIDs {
			for _, state := range []string{"failure", "cancelled", "", "unknown"} {
				bad := validNeeds(profile)
				j := bad[id]
				j.Result = state
				bad[id] = j
				if err := checkResults(profile, bad); err == nil {
					t.Fatalf("accepted %s %s %s", profile, id, state)
				}
			}
			bad := validNeeds(profile)
			delete(bad, id)
			if err := checkResults(profile, bad); err == nil {
				t.Fatalf("accepted missing %s", id)
			}
		}
		bad := validNeeds(profile)
		j := bad["browser"]
		j.Result = "skipped"
		bad["browser"] = j
		if err := checkResults(profile, bad); err == nil {
			t.Fatal("accepted selected browser skip")
		}
		bad = validNeeds(profile)
		j = bad["source-integration"]
		if profile == "full" {
			j.Result = "skipped"
		} else {
			j.Result = "success"
		}
		bad["source-integration"] = j
		if err := checkResults(profile, bad); err == nil {
			t.Fatal("accepted inconsistent source selection")
		}
		bad = validNeeds(profile)
		bad["omitted-new-job"] = jobResult{Result: "success"}
		if err := checkResults(profile, bad); err == nil {
			t.Fatal("accepted unaggregated job")
		}
		bad = validNeeds(profile)
		j = bad["plan"]
		j.Outputs["profile"] = "unknown"
		bad["plan"] = j
		if err := checkResults(profile, bad); err == nil {
			t.Fatal("accepted unknown classifier output")
		}
	}
	if err := checkResults("unknown", validNeeds("full")); err == nil {
		t.Fatal("accepted unknown profile")
	}
}

func validNeeds(profile string) map[string]jobResult {
	m := map[string]jobResult{}
	for _, id := range jobIDs {
		state := "success"
		if profile == "browser" && id != "plan" && id != "core" && id != "browser" &&
			id != "release-contract" {
			state = "skipped"
		}
		m[id] = jobResult{Result: state}
	}
	m["plan"] = jobResult{Result: "success", Outputs: map[string]string{"profile": profile}}
	return m
}

func TestWorkflowPreservesPlanAndEverySelectedResult(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflow(data); err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	jobs, ok := w["jobs"].(map[string]any)
	if !ok {
		t.Fatal("missing workflow jobs")
	}
	required, ok := jobs["required-checks"].(map[string]any)
	if !ok {
		t.Fatal("missing required summary")
	}
	old, ok := required["needs"].([]any)
	if !ok {
		t.Fatal("missing summary dependencies")
	}
	required["needs"] = old[1:]
	mutated, err := yaml.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflow(mutated); err == nil {
		t.Fatal("accepted omitted aggregate dependency")
	}
}

func validateWorkflow(data []byte) error {
	var w struct {
		Jobs map[string]struct {
			Needs any
			If    string
			Steps []struct {
				Run  string
				Uses string
			}
		}
	}
	if err := yaml.Unmarshal(data, &w); err != nil {
		return err
	}
	required, ok := w.Jobs["required-checks"]
	if !ok {
		return errPlan("missing summary")
	}
	deps, ok := required.Needs.([]any)
	if !ok || len(deps) != len(jobIDs) {
		return errPlan("incomplete summary")
	}
	seen := map[string]bool{}
	for _, d := range deps {
		v, ok := d.(string)
		if !ok || seen[v] {
			return errPlan("invalid summary dependency")
		}
		seen[v] = true
	}
	for _, id := range jobIDs {
		if !seen[id] {
			return errPlan("omitted selected job")
		}
	}
	if required.If != "always()" {
		return errPlan("summary must inspect failures")
	}
	if len(w.Jobs) != len(jobIDs)+1 {
		return errPlan("job outside summary")
	}
	for _, id := range jobIDs {
		j, ok := w.Jobs[id]
		if !ok {
			return errPlan("missing job")
		}
		if id == "core" || id == "browser" || id == "plan" || id == "release-contract" {
			if j.If != "" {
				return errPlan("required static/browser job conditional")
			}
			continue
		}
		if j.If != "${{ !cancelled() && needs.plan.outputs.profile != 'browser' }}" ||
			j.Needs != "plan" {
			return errPlan("unexpected full-suite admission")
		}
	}
	run := ""
	for _, s := range required.Steps {
		run += s.Run
	}
	if !strings.Contains(run, "ci-trusted/scripts/ci-plan/main.go check") ||
		!strings.Contains(run, "NEEDS_JSON") {
		return errPlan("missing strict summary")
	}
	return nil
}

func fixture(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	gitTest(t, repo, "init", "-q")
	gitTest(t, repo, "config", "user.name", "Fixture")
	gitTest(t, repo, "config", "user.email", "fixture@example.invalid")
	gitTest(t, repo, "config", "commit.gpgsign", "false")
	for _, p := range browserFiles {
		write(t, filepath.Join(repo, p), "//go:build browser\npackage browser_test\n")
	}
	write(
		t,
		filepath.Join(repo, "go.mod"),
		"module github.com/devantler-tech/data-product-controller\n",
	)
	gitTest(t, repo, "add", "--", "internal/browser", "go.mod")
	gitTest(t, repo, "commit", "-qm", "trusted base")
	return repo, gitTest(t, repo, "rev-parse", "HEAD")
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, repo, path string) []byte {
	t.Helper()
	root, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return data
}

func gitTest(t *testing.T, repo string, args ...string) string {
	t.Helper()
	var cmd *exec.Cmd
	switch args[0] {
	case "init":
		cmd = exec.CommandContext(t.Context(), "git", "init", "-q")
	case "config":
		switch args[1] {
		case "user.name":
			cmd = exec.CommandContext(t.Context(), "git", "config", "user.name", "Fixture")
		case "user.email":
			cmd = exec.CommandContext(
				t.Context(),
				"git",
				"config",
				"user.email",
				"fixture@example.invalid",
			)
		case "commit.gpgsign":
			cmd = exec.CommandContext(t.Context(), "git", "config", "commit.gpgsign", "false")
		default:
			t.Fatal("unknown fixture configuration")
		}
	case "add":
		cmd = exec.CommandContext(
			t.Context(),
			"git",
			"add",
			"--pathspec-from-file=-",
			"--pathspec-file-nul",
		)
		cmd.Stdin = strings.NewReader(strings.Join(args[2:], "\x00") + "\x00")
	case "commit":
		cmd = exec.CommandContext(t.Context(), "git", "commit", "-qm", "fixture")
	case "rev-parse":
		cmd = exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	default:
		t.Fatal("unknown fixture Git operation")
	}
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestNeedsJSON(t *testing.T) {
	data, err := json.Marshal(validNeeds("browser"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]jobResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := checkResults("browser", got); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserFirstPartyDependencyClosure(t *testing.T) {
	cmd := exec.CommandContext(
		t.Context(),
		"go",
		"list",
		"-tags=browser",
		"-deps",
		"-test",
		"-json",
		"./internal/browser",
	)
	cmd.Dir = "../.."
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	got := map[string]bool{}
	for {
		var pkg struct {
			ImportPath string
			Module     *struct{ Path string }
		}
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if pkg.Module == nil ||
			pkg.Module.Path != "github.com/devantler-tech/data-product-controller" ||
			strings.Contains(pkg.ImportPath, " [") {
			continue
		}
		path := strings.TrimPrefix(pkg.ImportPath, pkg.Module.Path+"/")
		if path == "internal/browser" || path == "internal/browser.test" {
			continue
		}
		got[path] = true
	}
	if len(got) != len(firstPartyPackages) {
		t.Fatalf("first-party closure changed: %v", got)
	}
	for p := range firstPartyPackages {
		if !got[p] {
			t.Fatalf("missing first-party dependency %s", p)
		}
	}
}

func TestWorkflowClassifierFailureRetainsFullSuite(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct{ Steps []struct{ ID, Run string } }
	}
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	run := ""
	for _, step := range w.Jobs["plan"].Steps {
		if step.ID == "select" {
			run = step.Run
		}
	}
	if run == "" {
		t.Fatal("missing classifier invocation")
	}
	for _, tc := range []struct {
		name, output string
		code         int
		want         string
	}{
		{"valid browser", "browser", 0, "browser"},
		{"valid full", "full", 0, "full"},
		{"classifier failure", "browser", 1, "full"},
		{"empty output", "", 0, "full"},
		{"unknown output", "anything", 0, "full"},
		{"multiple outputs", "browser\nfull", 0, "full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "scripts/ci-plan/main.go"), "trusted fixture")
			output := filepath.Join(dir, "output")
			stub := fmt.Sprintf("go() { printf '%%s\\n' '%s'; return %d; }\n", tc.output, tc.code)
			cmd := exec.CommandContext(t.Context(), "bash")
			cmd.Stdin = strings.NewReader(stub + run)
			cmd.Dir = dir
			cmd.Env = append(
				os.Environ(),
				"EVENT_NAME=pull_request",
				"BASE_SHA="+strings.Repeat("a", 40),
				"TESTED_SHA="+strings.Repeat("b", 40),
				"GITHUB_WORKSPACE="+dir,
				"GITHUB_OUTPUT="+output,
			)
			log, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("selection failed: %s %v", log, err)
			}
			result := readFixture(t, dir, "output")
			if string(result) != "profile="+tc.want+"\n" {
				t.Fatalf("%q %s", result, log)
			}
		})
	}
}

func TestWorkflowSourceMatrixIsComplete(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			Strategy struct{ Matrix struct{ Suite []string } }
		}
	}
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	got := w.Jobs["source-integration"].Strategy.Matrix.Suite
	sort.Strings(got)
	if strings.Join(got, ",") != "document,graph,lifecycle,sql" {
		t.Fatalf("source acceptance matrix changed: %v", got)
	}
}
