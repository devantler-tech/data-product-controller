// The publisher-review command hosts bounded offline report inspection.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/devantler-tech/data-product-controller/internal/portablehost"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	"github.com/devantler-tech/data-product-controller/web"
)

// main reports configuration failures before exposing a listener.
func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("Publisher review", "error", err)
		os.Exit(1)
	}
}

// run binds only publisher preflight; presentation settings have no effect on this application.
func run(args []string, getenv func(string) string) error {
	options, err := portablehost.ParseOptions("publisher-review", args)
	if err != nil {
		return err
	}
	setting := getenv("PUBLISHER_PREFLIGHT_ENABLED")
	if setting != "" && setting != "true" && setting != "false" {
		return errors.New("PUBLISHER_PREFLIGHT_ENABLED must be true or false")
	}
	client, err := featureflag.NewClient("publisher-review",
		featureflag.NewProvider(map[string]bool{"publisher-preflight": setting == "true"}))
	if err != nil {
		return fmt.Errorf("configure publisher flag: %w", err)
	}
	return portablehost.Run(options, web.PublisherHandler(func() bool {
		return featureflag.Enabled(context.Background(), client, "publisher-preflight")
	}))
}
