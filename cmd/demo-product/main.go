package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/config"
	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

func main() {
	if err := run(); err != nil {
		slog.Error("run demo data product", "error", err)
		os.Exit(1)
	}
}

// run validates publication settings before starting the sample's independent API and UI.
func run() error {
	address := flag.String("listen-address", ":8080", "Address for the example data product.")
	flag.Parse()
	enabled, err := config.UIContractEnabled(os.Getenv("UI_CONTRACT_ENABLED"))
	if err != nil {
		return err
	}
	flagClient, err := featureflag.NewClient(
		"demo-product",
		featureflag.NewProvider(map[string]bool{"ui-contract": enabled}),
	)
	if err != nil {
		return fmt.Errorf("configure UI flag: %w", err)
	}
	var origins []string
	if featureflag.Enabled(context.Background(), flagClient, "ui-contract") {
		origins, err = config.UIHostOrigins(os.Getenv("UI_HOST_ORIGINS"))
		if err != nil {
			return err
		}
	}

	handler, err := demoproduct.NewHandlerWithPublicURL(os.Getenv("PUBLIC_BASE_URL"), origins...)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-stopContext.Done()

		shutdownContext, cancel := context.WithTimeout(
			context.WithoutCancel(stopContext),
			10*time.Second,
		)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("shut down demo product", "error", err)
		}
	}()

	slog.Info("starting demo data product", "address", *address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}
