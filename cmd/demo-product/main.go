package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/demoproduct"
)

func main() {
	address := flag.String("listen-address", ":8080", "Address for the example data product.")
	flag.Parse()

	server := &http.Server{
		Addr:              *address,
		Handler:           demoproduct.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-stopContext.Done()

		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("shut down demo product", "error", err)
		}
	}()

	slog.Info("starting demo data product", "address", *address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("run demo data product", "error", err)
		os.Exit(1)
	}
}
