package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/httpsource"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

func main() {
	if err := run(); err != nil {
		slog.Error("run HTTP source connector", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("listen-address", ":8080", "Address for the read-only export API.")
	managementAddress := flag.String(
		"management-address",
		":8081",
		"Private address for probes and metrics.",
	)
	configPath := flag.String(
		"source-config",
		"/etc/http-source/config.json",
		"Projected source configuration file.",
	)
	flag.Parse()
	enabled := false
	if raw := os.Getenv("HTTP_SOURCE_ENABLED"); raw != "" {
		var err error
		enabled, err = strconv.ParseBool(raw)
		if err != nil {
			return errors.New("HTTP_SOURCE_ENABLED must be a boolean")
		}
	}
	flags, err := featureflag.NewClient(
		"http-source",
		featureflag.NewProvider(map[string]bool{"http-source": enabled}),
	)
	if err != nil {
		return fmt.Errorf("initialize release flag: %w", err)
	}
	service := httpsource.NewService(
		*configPath,
		func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "http-source") },
	)
	defer service.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	public, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *address)
	if err != nil {
		return fmt.Errorf("listen for queries: %w", err)
	}
	defer func() { _ = public.Close() }()
	management, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *managementAddress)
	if err != nil {
		return fmt.Errorf("listen for management: %w", err)
	}
	defer func() { _ = management.Close() }()
	return serve(ctx, service, public, management)
}

// serve closes both listeners on shutdown or a listener failure, cancelling active source reads.
func serve(
	ctx context.Context,
	service *httpsource.Service,
	public, management net.Listener,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	newServer := func(handler http.Handler) *http.Server {
		return &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
			MaxHeaderBytes:    8 << 10,
			BaseContext:       func(net.Listener) context.Context { return ctx },
		}
	}
	api := newServer(service.PublicHandler())
	admin := newServer(service.ManagementHandler())
	results := make(chan error, 2)
	go func() { results <- api.Serve(public) }()
	go func() { results <- admin.Serve(management) }()
	var result error
	select {
	case <-ctx.Done():
	case result = <-results:
		if errors.Is(result, http.ErrServerClosed) {
			result = nil
		}
	}
	cancel()
	shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer done()
	apiError := api.Shutdown(shutdown)
	adminError := admin.Shutdown(shutdown)
	// Force-close any connections that outlive the graceful shutdown deadline.
	_ = api.Close()
	_ = admin.Close()
	return errors.Join(result, apiError, adminError)
}
