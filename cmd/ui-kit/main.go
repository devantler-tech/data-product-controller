// The ui-kit command hosts a portable, opt-in compatibility page without Kubernetes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/devantler-tech/data-product-controller/internal/config"
	"github.com/devantler-tech/data-product-controller/internal/portablehost"
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

// run binds only the operator-selected listener; it never fetches or proxies a product.
func run(args []string, getenv func(string) string) error {
	options, err := portablehost.ParseOptions("ui-kit", args)
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
	discoveryEnabled, err := config.RegistryDiscoveryEnabled(getenv("REGISTRY_DISCOVERY_ENABLED"))
	if err != nil {
		return err
	}
	publisherSetting := getenv("PUBLISHER_PREFLIGHT_ENABLED")
	if publisherSetting != "" && publisherSetting != "true" && publisherSetting != "false" {
		return errors.New("PUBLISHER_PREFLIGHT_ENABLED must be true or false")
	}
	client, err := featureflag.NewClient(
		"ui-kit",
		featureflag.NewProvider(
			map[string]bool{
				"ui-contract":         enabled,
				"ui-appearance":       appearanceEnabled,
				"registry-discovery":  discoveryEnabled,
				"publisher-preflight": publisherSetting == "true",
			},
		),
	)
	if err != nil {
		return fmt.Errorf("configure UI flag: %w", err)
	}
	return portablehost.Run(options, web.KitHandlerWithOptions(web.KitOptions{
		ContractEnabled:   func() bool { return featureflag.Enabled(context.Background(), client, "ui-contract") },
		AppearanceEnabled: func() bool { return featureflag.Enabled(context.Background(), client, "ui-appearance") },
		DiscoveryEnabled:  func() bool { return featureflag.Enabled(context.Background(), client, "registry-discovery") },
		PublisherEnabled:  func() bool { return featureflag.Enabled(context.Background(), client, "publisher-preflight") },
	}))
}
