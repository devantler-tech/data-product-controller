package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/config"
	"github.com/devantler-tech/data-product-controller/internal/contractprobe"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

// main exposes only private management endpoints and reports process failures without contract contents.
func main() {
	if err := run(); err != nil {
		slog.Error("run contract probe", "error", err)
		os.Exit(1)
	}
}

// run configures the default-off release flag and cancels active probes during shutdown.
func run() error {
	enabled, err := config.ContractReadinessEnabled(os.Getenv("CONTRACT_READINESS_ENABLED"))
	if err != nil {
		return errors.New("CONTRACT_READINESS_ENABLED must be a boolean")
	}
	flags, err := featureflag.NewClient(
		"contract-probe",
		featureflag.NewProvider(map[string]bool{"contract-readiness": enabled}),
	)
	if err != nil {
		return errors.New("initialize contract release flag")
	}
	service := contractprobe.NewService(
		os.Getenv("CONTRACT_PROBE_URL"),
		func(ctx context.Context) bool { return featureflag.Enabled(ctx, flags, "contract-readiness") },
	)
	defer service.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{
		Addr:              ":8081",
		Handler:           service,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	err = server.Shutdown(shutdown)
	_ = server.Close()
	return err
}
