// Package httpserver owns the bounded lifecycle of independent HTTP commands.
package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Run serves until cancellation or listener failure.
func Run(
	ctx context.Context,
	server *http.Server,
	start func() error,
	timeout time.Duration,
) error {
	results := make(chan error, 1)
	go func() { results <- start() }()
	var serveError error
	received := false
	select {
	case <-ctx.Done():
	case serveError = <-results:
		received = true
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	shutdownError := server.Shutdown(shutdown)
	// A deadline closes connections that could not finish graceful draining.
	_ = server.Close()
	if !received {
		select {
		case serveError = <-results:
		case <-shutdown.Done():
			serveError = shutdown.Err()
		}
	}
	if errors.Is(serveError, http.ErrServerClosed) {
		serveError = nil
	}
	return errors.Join(serveError, shutdownError)
}
