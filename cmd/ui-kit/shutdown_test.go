//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestUIKitTerminationBoundsTLSStartup exercises the real signal and pre-Serve file-loading path.
func TestUIKitTerminationBoundsTLSStartup(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "certificate.pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	address := availableAddress(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := hostCommand(
		t,
		ctx,
		[]string{"--listen-address", address, "--tls-cert", fifo, "--tls-key", fifo},
		"false",
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { cancel() })
	deadline := time.Now().Add(3 * time.Second)
	bound := false
	for time.Now().Before(deadline) {
		connection, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(
			ctx,
			"tcp",
			address,
		)
		if err == nil {
			_ = connection.Close()
			bound = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bound {
		t.Fatal("TLS startup did not own its listener")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || ctx.Err() != nil ||
			!strings.Contains(output.String(), "context deadline exceeded") {
			t.Fatalf("startup termination lost its bound: %v %s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("SIGTERM did not bound TLS startup")
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", address)
	if err != nil {
		t.Fatalf("terminated TLS startup retained its listener: %v", err)
	}
	_ = listener.Close()
}

// TestUIKitSignalShutsDownBothTransports retains successful termination after listening.
func TestUIKitSignalShutsDownBothTransports(t *testing.T) {
	for _, transport := range []string{"gateway", "tls"} {
		t.Run(transport, func(t *testing.T) {
			address := availableAddress(t)
			args := []string{"--listen-address", address}
			scheme := "http"
			client := &http.Client{Timeout: time.Second}
			if transport == "tls" {
				cert, key, trusted := hostCertificate(t)
				args = append(args, "--tls-cert", cert, "--tls-key", key)
				client.Transport = trusted
				scheme = "https"
			} else {
				args = append(args, "--http-behind-gateway")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := hostCommand(t, ctx, args, "false")
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill() }()
			waitForHost(t, client, scheme+"://"+address+"/healthz")
			if err := command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err != nil || ctx.Err() != nil {
				t.Fatalf("signal shutdown failed: %v %s", err, output.String())
			}
		})
	}
}
