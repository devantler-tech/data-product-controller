// The ui-kit command hosts a portable, opt-in compatibility page without Kubernetes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/config"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	"github.com/devantler-tech/data-product-controller/web"
)

// main starts the explicit TLS test host and reports configuration failures before listening.
func main() {
	if err := run(); err != nil {
		slog.Error("UI compatibility kit", "error", err)
		os.Exit(1)
	}
}

// run binds only the operator-selected listener; it never fetches or proxies a product.
func run() error {
	address := flag.String(
		"listen-address",
		"127.0.0.1:8443",
		"TLS address for the compatibility kit.",
	)
	cert := flag.String("tls-cert", "", "TLS certificate file.")
	key := flag.String("tls-key", "", "TLS private key file.")
	flag.Parse()
	if *cert == "" || *key == "" {
		return errors.New("provide --tls-cert and --tls-key for the HTTPS host")
	}
	enabled, err := config.UIContractEnabled(os.Getenv("UI_CONTRACT_ENABLED"))
	if err != nil {
		return err
	}
	client, err := featureflag.NewClient(
		"ui-kit",
		featureflag.NewProvider(map[string]bool{"ui-contract": enabled}),
	)
	if err != nil {
		return fmt.Errorf("configure UI flag: %w", err)
	}
	server := &http.Server{
		Addr: *address,
		Handler: web.KitHandler(
			func() bool { return featureflag.Enabled(context.Background(), client, "ui-contract") },
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServeTLS(
		*cert,
		*key,
	); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve kit: %w", err)
	}
	return nil
}
