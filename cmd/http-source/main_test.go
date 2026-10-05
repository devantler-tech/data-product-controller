package main

import (
	"flag"
	"io"
	"net"
	"os"
	"strings"
	"testing"
)

// TestRejectsTrailingArgumentsBeforeConfiguration rejects options that Go would otherwise ignore.
func TestRejectsTrailingArgumentsBeforeConfiguration(t *testing.T) {
	for _, args := range [][]string{{"export", "--listen-address", "127.0.0.1:18080"}, {"--", "--source-config", "/unreadable/config"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			priorFlags, priorArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine = priorFlags; os.Args = priorArgs })
			flag.CommandLine = flag.NewFlagSet("http-source", flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)
			os.Args = append([]string{"http-source"}, args...)
			t.Setenv("HTTP_SOURCE_ENABLED", "invalid")
			if err := run(); err == nil || !strings.Contains(err.Error(), "arguments") {
				t.Fatalf("ignored trailing arguments before reading configuration: %v", err)
			}
		})
	}
}

// TestNamedArgumentsReachTheSelectedListener preserves supported operator options.
func TestNamedArgumentsReachTheSelectedListener(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	priorFlags, priorArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine = priorFlags; os.Args = priorArgs })
	flag.CommandLine = flag.NewFlagSet("http-source", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = []string{
		"http-source",
		"--listen-address",
		listener.Addr().String(),
		"--source-config",
		"/unreadable/config",
	}
	t.Setenv("HTTP_SOURCE_ENABLED", "false")
	if err := run(); err == nil || !strings.Contains(err.Error(), "listen for queries") {
		t.Fatalf("named arguments failed before the selected listener: %v", err)
	}
}
