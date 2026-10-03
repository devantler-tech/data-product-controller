// The ui-kit command hosts a portable, opt-in compatibility page without Kubernetes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/config"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	"github.com/devantler-tech/data-product-controller/web"
)

// main starts the independently deployed host and reports configuration failures before listening.
func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("UI compatibility kit", "error", err)
		os.Exit(1)
	}
}

type hostOptions struct {
	address, cert, key string
	httpBehindGateway  bool
}

// parseOptions keeps plaintext transport an explicit deployment choice.
func parseOptions(args []string) (hostOptions, error) {
	var options hostOptions
	flags := flag.NewFlagSet("ui-kit", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(
		&options.address,
		"listen-address",
		"127.0.0.1:8443",
		"Exact IP or localhost listener for the compatibility kit.",
	)
	flags.StringVar(&options.cert, "tls-cert", "", "TLS certificate file.")
	flags.StringVar(&options.key, "tls-key", "", "TLS private key file.")
	flags.BoolVar(
		&options.httpBehindGateway,
		"http-behind-gateway",
		false,
		"Serve internal HTTP behind an independently managed HTTPS gateway.",
	)
	if err := flags.Parse(args); err != nil {
		return options, fmt.Errorf("parse UI host options: %w", err)
	}
	if flags.NArg() != 0 {
		return options, errors.New("invalid UI host options")
	}
	explicitAddress := false
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "listen-address" {
			explicitAddress = true
		}
	})
	if options.httpBehindGateway {
		if !explicitAddress {
			return options, errors.New("HTTP behind a gateway requires explicit --listen-address")
		}
		if options.cert != "" || options.key != "" {
			return options, errors.New("HTTP behind a gateway cannot use TLS material")
		}
	} else if options.cert == "" || options.key == "" {
		return options, errors.New("provide --tls-cert and --tls-key for the HTTPS host")
	}
	host, portText, err := net.SplitHostPort(options.address)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 ||
		(host != "localhost" && net.ParseIP(host) == nil) {
		return options, errors.New(
			"listener requires an explicit IP or localhost and a port from 1 to 65535",
		)
	}
	return options, nil
}

// run binds only the operator-selected listener; it never fetches or proxies a product.
func run(args []string, getenv func(string) string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	enabled, err := config.UIContractEnabled(getenv("UI_CONTRACT_ENABLED"))
	if err != nil {
		return err
	}
	appearanceEnabled, err := config.UIAppearanceEnabled(getenv("UI_APPEARANCE_ENABLED"))
	if err != nil {
		return err
	}
	client, err := featureflag.NewClient(
		"ui-kit",
		featureflag.NewProvider(
			map[string]bool{"ui-contract": enabled, "ui-appearance": appearanceEnabled},
		),
	)
	if err != nil {
		return fmt.Errorf("configure UI flag: %w", err)
	}
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", health)
	handler.Handle("/", web.KitHandlerWithAppearance(
		func() bool { return featureflag.Enabled(context.Background(), client, "ui-contract") },
		func() bool { return featureflag.Enabled(context.Background(), client, "ui-appearance") },
	))
	server := &http.Server{
		Addr:              options.address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	if options.httpBehindGateway {
		err = server.ListenAndServe()
	} else {
		err = server.ListenAndServeTLS(options.cert, options.key)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve kit: %w", err)
	}
	return nil
}

// health reports process availability without consulting flags or accepting input.
func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		http.Error(w, "health accepts no input", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", "3")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "ok\n")
	}
}
