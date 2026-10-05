//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReplacedInputRejectsWithoutBlocking exercises the real FIFO open in an isolated process.
func TestReplacedInputRejectsWithoutBlocking(t *testing.T) {
	if os.Getenv("DPC_FIFO_OPEN_CHILD") == "1" {
		replacedInputChild(t)
		return
	}
	for _, links := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "regular-to-fifo", true: "retargeted-symlink"}[links],
			func(t *testing.T) {
				selected, replacement := inputReplacementFixture(t, links)
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				// #nosec G204 -- Re-enter only the current test executable with authored fixture options.
				command := exec.CommandContext(
					ctx,
					executable,
					"-test.v",
					"-test.run=^TestReplacedInputRejectsWithoutBlocking$",
				)
				command.Env = append(os.Environ(), "DPC_FIFO_OPEN_CHILD=1",
					"DPC_FIFO_OPEN_SELECTED="+selected, "DPC_FIFO_OPEN_REPLACEMENT="+replacement)
				output, err := command.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf(
						"opening a replaced selection blocked: %v; child output: %s",
						ctx.Err(),
						output,
					)
				}
				if err != nil {
					t.Fatalf(
						"replaced selection was not safely rejected: %v; child output: %s",
						err,
						output,
					)
				}
			},
		)
	}
}

// inputReplacementFixture keeps cleanup in the parent even when the regression child is killed.
func inputReplacementFixture(t *testing.T, links bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	selected := filepath.Join(dir, "selected.json")
	replacement := filepath.Join(dir, "replacement.fifo")
	if err := os.WriteFile(selected, []byte("public metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if links {
		selectedLink := filepath.Join(dir, "selected-link.json")
		replacementLink := filepath.Join(dir, "replacement-link.json")
		if err := os.Symlink(selected, selectedLink); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(replacement, replacementLink); err != nil {
			t.Fatal(err)
		}
		return selectedLink, replacementLink
	}
	return selected, replacement
}

// replacedInputChild replaces a regular selection immediately before its real open.
func replacedInputChild(t *testing.T) {
	t.Helper()
	selected := os.Getenv("DPC_FIFO_OPEN_SELECTED")
	replacement := os.Getenv("DPC_FIFO_OPEN_REPLACEMENT")
	var opened *os.File
	file, err := openInputWith(
		selected,
		func(path string, flag int, mode os.FileMode) (*os.File, error) {
			// #nosec G703 -- Both paths are created and passed by the parent inside its disposable test fixture.
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			t.Log("replaced selected regular file with FIFO before the real open")
			var openErr error
			// #nosec G304 G703 -- Open only the parent-owned fixture selected for this real syscall regression.
			opened, openErr = os.OpenFile(path, flag, mode)
			return opened, openErr
		},
	)
	if err == nil || file != nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("FIFO selection was admitted: file=%v, err=%v", file, err)
	}
	if opened == nil {
		t.Fatal("regression did not exercise the opened FIFO descriptor")
	}
	if _, err := opened.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("rejected descriptor remained open: %v", err)
	}
}

// TestInputRegularFilesAndSymlinks preserves supported selections and rejects directories.
func TestInputRegularFilesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "catalog.json")
	link := filepath.Join(dir, "catalog-link.json")
	content := "public catalog metadata"
	if err := os.WriteFile(regular, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{regular, link} {
		file, err := openInput(path)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || string(got) != content {
			t.Fatalf(
				"supported selection changed: read=%v close=%v content=%q",
				readErr,
				closeErr,
				got,
			)
		}
	}
	for _, path := range []string{dir, filepath.Join(dir, "missing")} {
		file, err := openInput(path)
		if err == nil || file != nil || strings.Contains(err.Error(), path) {
			t.Fatalf("invalid selection admitted or disclosed: file=%v err=%v", file, err)
		}
	}
}
