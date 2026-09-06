package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("expected serve, control, idle, or probe")
	}
	switch args[0] {
	case "serve":
		if len(args) != 1 {
			return errors.New("serve accepts no arguments")
		}
		return serve(ctx)
	case "control":
		return control(ctx, args[1:])
	case "idle":
		if len(args) != 1 {
			return errors.New("idle accepts no arguments")
		}
		return idle(ctx)
	case "probe":
		return probe(ctx, args[1:])
	default:
		return errors.New("unknown fixture command")
	}
}

func newServer(ctx context.Context, address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       15 * time.Second,
		MaxHeaderBytes:    8 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

func idle(ctx context.Context) error {
	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := newServer(ctx, "127.0.0.1:9000", health)
	finished := make(chan error, 1)
	go func() { finished <- server.ListenAndServe() }()
	var result error
	select {
	case <-ctx.Done():
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			result = errors.New("fixture health listener failed")
		}
	}
	shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	shutdownErr := server.Shutdown(shutdown)
	_ = server.Close()
	if shutdownErr != nil {
		return errors.New("fixture health shutdown failed")
	}
	return result
}

func serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fixture := &source{}
	public := http.NewServeMux()
	public.HandleFunc("/export", fixture.export)
	admin := http.NewServeMux()
	admin.HandleFunc("/control/", fixture.control)
	admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	api := newServer(ctx, ":443", public)
	management := newServer(ctx, "127.0.0.1:9000", admin)
	results := make(chan error, 2)
	go func() { results <- api.ListenAndServeTLS("/tls/tls.crt", "/tls/tls.key") }()
	go func() { results <- management.ListenAndServe() }()
	var result error
	select {
	case <-ctx.Done():
	case err := <-results:
		if !errors.Is(err, http.ErrServerClosed) {
			result = errors.New("fixture listener failed")
		}
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	apiErr := api.Shutdown(shutdown)
	managementErr := management.Shutdown(shutdown)
	_ = api.Close()
	_ = management.Close()
	if apiErr != nil || managementErr != nil {
		return errors.New("fixture shutdown failed")
	}
	return result
}
