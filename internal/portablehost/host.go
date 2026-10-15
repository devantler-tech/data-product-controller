// Package portablehost shares bounded offline-host transport without binding application flags.
package portablehost

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/devantler-tech/data-product-controller/internal/httpserver"
)

// Options describes the operator-selected transport and listener.
type Options struct {
	Address, Cert, Key string
	HTTPBehindGateway  bool
}

// ParseOptions keeps plaintext transport an explicit deployment choice.
func ParseOptions(name string, args []string) (Options, error) {
	var options Options
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(
		&options.Address,
		"listen-address",
		"127.0.0.1:8443",
		"Exact IP or localhost listener for the portable host.",
	)
	flags.StringVar(&options.Cert, "tls-cert", "", "TLS certificate file.")
	flags.StringVar(&options.Key, "tls-key", "", "TLS private key file.")
	flags.BoolVar(
		&options.HTTPBehindGateway,
		"http-behind-gateway",
		false,
		"Serve internal HTTP behind an independently managed HTTPS gateway.",
	)
	if err := flags.Parse(args); err != nil {
		return options, fmt.Errorf("parse portable host options: %w", err)
	}
	if flags.NArg() != 0 {
		return options, errors.New("invalid portable host options")
	}
	explicitAddress := false
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "listen-address" {
			explicitAddress = true
		}
	})
	if options.HTTPBehindGateway {
		if !explicitAddress {
			return options, errors.New("HTTP behind a gateway requires explicit --listen-address")
		}
		if options.Cert != "" || options.Key != "" {
			return options, errors.New("HTTP behind a gateway cannot use TLS material")
		}
	} else if options.Cert == "" || options.Key == "" {
		return options, errors.New("provide --tls-cert and --tls-key for the HTTPS host")
	}
	host, portText, err := net.SplitHostPort(options.Address)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 ||
		(host != "localhost" && net.ParseIP(host) == nil) {
		return options, errors.New(
			"listener requires an explicit IP or localhost and a port from 1 to 65535",
		)
	}
	return options, nil
}

// Run serves an application and bounded health endpoint until shutdown.
func Run(options Options, application http.Handler) error {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", health)
	handler.Handle("/", application)
	server := &http.Server{
		Addr:              options.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Own the listener even when TLS loading has not yet registered it with Serve.
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Address)
	if err != nil {
		return fmt.Errorf("listen for portable host: %w", err)
	}
	defer func() { _ = listener.Close() }()
	return httpserver.Run(ctx, server, func() error {
		if options.HTTPBehindGateway {
			return server.Serve(listener)
		}
		return server.ServeTLS(listener, options.Cert, options.Key)
	}, 10*time.Second)
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
