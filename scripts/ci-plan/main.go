// ci-plan admits a bounded browser-test-only profile from trusted base code.
// All uncertain paths retain the full acceptance suite.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var browserFiles = []string{
	"internal/browser/asset_upgrade_browser_test.go",
	"internal/browser/authenticated_workspace_browser_test.go",
	"internal/browser/catalog_admission_browser_test.go",
	"internal/browser/composition_browser_test.go",
	"internal/browser/descriptor_admission_browser_test.go",
	"internal/browser/descriptor_browser_test.go",
	"internal/browser/discovery_browser_test.go",
	"internal/browser/history_integrity_browser_test.go",
	"internal/browser/host_integrity_browser_test.go",
	"internal/browser/kit_workspace_browser_test.go",
	"internal/browser/launch_browser_profile_test.go",
	"internal/browser/launch_browser_test.go",
	"internal/browser/lineage_browser_test.go",
	"internal/browser/public_url_browser_test.go",
	"internal/browser/publisher_browser_test.go",
	"internal/browser/raw_metadata_browser_test.go",
	"internal/browser/raw_response_browser_test.go",
	"internal/browser/registry_bounds_browser_test.go",
	"internal/browser/registry_browser_test.go",
	"internal/browser/theme_browser_test.go",
	"internal/browser/trace_integrity_browser_test.go",
	"internal/browser/ui_contract_browser_test.go",
	"internal/browser/workspace_browser_test.go",
}

// This is the complete first-party production closure of the tagged browser
// package, checked with go list -tags=browser -deps -test ./internal/browser.
// Changes anywhere outside the exact test inventory retain full acceptance,
// including these packages, their embedded assets and composition fixtures.
var firstPartyPackages = map[string]bool{
	"api/v1alpha1": true, "config/crd": true, "internal/catalog": true,
	"internal/config": true, "internal/connector/v1": true,
	"internal/controller": true, "internal/demoproduct": true,
	"internal/preflight": true, "internal/provider/v1": true,
	"internal/provisioner/v1": true, "internal/registry": true,
	"internal/uibundle": true, "web": true,
}

var jobIDs = []string{
	"plan",
	"core",
	"browser",
	"release-contract",
	"source-integration",
	"percona-acceptance",
	"arango-acceptance",
	"age-image",
	"postgres-acceptance",
}

type jobResult struct {
	Result  string            `json:"result"`
	Outputs map[string]string `json:"outputs"`
}

func errPlan(message string) error { return errors.New(message) }

// Git arguments are fixed. Exact object IDs and paths are data on stdin;
// replacement refs cannot redirect the immutable objects being classified.
func gitObject(repo, object string) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	check := exec.CommandContext(ctx, "git", "--no-replace-objects", "cat-file", "--batch-check")
	check.Dir = repo
	check.Stdin = strings.NewReader(object + "\n")
	meta, err := check.Output()
	if err != nil {
		return "", nil, err
	}
	fields := strings.Fields(string(meta))
	if len(fields) != 3 {
		return "", nil, errPlan("missing Git object")
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil || size < 0 || size > 1024*1024 {
		return "", nil, errPlan("Git object exceeds review bound")
	}
	read := exec.CommandContext(ctx, "git", "--no-replace-objects", "cat-file", "--batch")
	read.Dir = repo
	read.Stdin = strings.NewReader(object + "\n")
	data, err := read.Output()
	if err != nil {
		return "", nil, err
	}
	split := bytes.IndexByte(data, '\n')
	if split < 0 || string(data[:split]) != strings.TrimSpace(string(meta)) ||
		len(data) != split+1+size+1 {
		return "", nil, errPlan("Git object changed or was incomplete")
	}
	return fields[1], data[split+1 : split+1+size], nil
}

func inventory(repo, sha string) (map[string]bool, error) {
	kind, data, err := gitObject(repo, sha+":internal/browser")
	if err != nil {
		return nil, err
	}
	if kind != "tree" {
		return nil, errPlan("browser inventory is not a Git tree")
	}
	got := map[string]bool{}
	for len(data) > 0 {
		space := bytes.IndexByte(data, ' ')
		end := bytes.IndexByte(data, 0)
		if space < 0 || end <= space || len(data) < end+21 || string(data[:space]) != "100644" {
			return nil, errPlan("browser inventory has a non-regular file")
		}
		path := "internal/browser/" + string(data[space+1:end])
		if got[path] {
			return nil, errPlan("duplicate browser file")
		}
		got[path] = true
		data = data[end+21:]
	}
	if len(got) != len(browserFiles) {
		return nil, errPlan("browser inventory changed")
	}
	for _, path := range browserFiles {
		if !got[path] {
			return nil, errPlan("browser inventory omitted a file")
		}
	}
	return got, nil
}

func admittedTest(repo, sha, path string) error {
	kind, data, err := gitObject(repo, sha+":"+path)
	if err != nil {
		return err
	}
	if kind != "blob" || len(data) > 1024*1024 {
		return errPlan("browser test exceeds review bound")
	}
	if !strings.HasPrefix(string(data), "//go:build browser\n") {
		return errPlan("browser build constraint changed")
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ParseComments)
	if err != nil {
		return err
	}
	if file.Name.Name != "browser_test" {
		return errPlan("browser package changed")
	}
	for _, c := range file.Comments {
		for _, line := range c.List {
			if strings.HasPrefix(line.Text, "//go:embed") ||
				strings.HasPrefix(line.Text, "// +build") {
				return errPlan("additional build or asset directive")
			}
		}
	}
	for _, imp := range file.Imports {
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return err
		}
		const prefix = "github.com/devantler-tech/data-product-controller/"
		if strings.HasPrefix(value, prefix) &&
			!firstPartyPackages[strings.TrimPrefix(value, prefix)] {
			return errPlan("unreviewed first-party dependency")
		}
		if imp.Name != nil && imp.Name.Name == "." {
			return errPlan("dot import")
		}
	}
	// Reject cgo: it can introduce dependencies outside the Go import graph.
	for _, imp := range file.Imports {
		if imp.Path.Value == "\"C\"" {
			return errPlan("cgo import")
		}
	}
	return nil
}

func classify(repo, base, head string) (string, error) {
	// Inputs are exact event commits, never branch names or shell expressions.
	sha := regexp.MustCompile("^[0-9a-f]{40}$")
	if !sha.MatchString(base) || !sha.MatchString(head) {
		return "full", errPlan("exact commit IDs required")
	}
	allowed, err := inventory(repo, base)
	if err != nil {
		return "full", err
	}
	if _, err := inventory(repo, head); err != nil {
		return "full", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		"git",
		"--no-replace-objects",
		"diff-tree",
		"--stdin",
		"--no-commit-id",
		"--no-renames",
		"--name-status",
		"-r",
		"-z",
	)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(base + " " + head + "\n")
	data, err := cmd.Output()
	if err != nil {
		return "full", err
	}
	if len(data) == 0 {
		return "full", nil
	}
	changes := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	if len(changes)%2 != 0 {
		return "full", errPlan("malformed changed paths")
	}
	for i := 0; i < len(changes); i += 2 {
		if changes[i] != "M" || !allowed[changes[i+1]] {
			return "full", nil
		}
	}
	// Recheck the complete inventory, not just the files a path filter selected.
	for _, path := range browserFiles {
		if err := admittedTest(repo, base, path); err != nil {
			return "full", err
		}
		if err := admittedTest(repo, head, path); err != nil {
			return "full", err
		}
	}
	return "browser", nil
}

func checkResults(profile string, needs map[string]jobResult) error {
	if profile != "full" && profile != "browser" {
		return errPlan("unknown selected profile")
	}
	if len(needs) != len(jobIDs) {
		return errPlan("missing or extra job result")
	}
	for _, id := range jobIDs {
		job, ok := needs[id]
		if !ok {
			return fmt.Errorf("missing job %s", id)
		}
		want := "success"
		if profile == "browser" && id != "plan" && id != "core" && id != "browser" &&
			id != "release-contract" {
			want = "skipped"
		}
		if job.Result != want {
			return fmt.Errorf("%s requires %s, got %q", id, want, job.Result)
		}
	}
	if needs["plan"].Outputs["profile"] != profile {
		return errPlan("profile does not match successful classifier")
	}
	return nil
}

func main() {
	if len(os.Args) == 5 && os.Args[1] == "classify" {
		profile, err := classify(os.Args[2], os.Args[3], os.Args[4])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Full suite retained:", err)
		}
		fmt.Println(profile)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "check" {
		var needs map[string]jobResult
		if err := json.Unmarshal([]byte(os.Getenv("NEEDS_JSON")), &needs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := checkResults(os.Args[2], needs); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Every selected CI job passed; unselected jobs match the verified profile.")
		return
	}
	fmt.Fprintln(
		os.Stderr,
		"usage: ci-plan classify <repository> <base-sha> <tested-sha> | check <profile>",
	)
	os.Exit(2)
}
