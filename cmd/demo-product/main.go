package main

import (
	"context"
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
	"github.com/devantler-tech/data-product-controller/internal/httpserver"
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
	appearanceEnabled, err := config.UIAppearanceEnabled(os.Getenv("UI_APPEARANCE_ENABLED"))
	if err != nil {
		return err
	}
	flagClient, err := featureflag.NewClient(
		"demo-product",
		featureflag.NewProvider(
			map[string]bool{"ui-contract": enabled, "ui-appearance": appearanceEnabled},
		),
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

	handler, err := demoproduct.NewHandlerWithOptions(demoproduct.HandlerOptions{
		PublicBaseURL: os.Getenv("PUBLIC_BASE_URL"), HostOrigins: origins,
		AppearanceEnabled: featureflag.Enabled(context.Background(), flagClient, "ui-contract") &&
			featureflag.Enabled(context.Background(), flagClient, "ui-appearance"),
	})
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

	slog.Info("starting demo data product", "address", *address)
	return httpserver.Run(stopContext, server, server.ListenAndServe, 10*time.Second)
}
