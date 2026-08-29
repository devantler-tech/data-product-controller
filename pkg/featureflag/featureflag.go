// Package featureflag provides the controller's portable OpenFeature boundary.
package featureflag

import (
	"context"
	"fmt"

	"github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/memprovider"
)

// NewProvider builds an in-process OpenFeature provider for deployment flags.
func NewProvider(flags map[string]bool) memprovider.InMemoryProvider {
	memFlags := make(map[string]memprovider.InMemoryFlag, len(flags))

	for key, on := range flags {
		variant := "off"
		if on {
			variant = "on"
		}

		memFlags[key] = memprovider.InMemoryFlag{
			State:          memprovider.Enabled,
			DefaultVariant: variant,
			Variants:       map[string]any{"on": true, "off": false},
		}
	}

	return memprovider.NewInMemoryProvider(memFlags)
}

// NewClient registers provider under domain and returns a client bound to it.
func NewClient(domain string, provider openfeature.FeatureProvider) (*openfeature.Client, error) {
	err := openfeature.SetNamedProviderAndWait(domain, provider)
	if err != nil {
		return nil, fmt.Errorf("register feature-flag provider for %q: %w", domain, err)
	}

	return openfeature.NewClient(domain), nil
}

// Enabled reports whether flag is on, defaulting to off on missing flags or errors.
func Enabled(ctx context.Context, client *openfeature.Client, flag string) bool {
	return client.Boolean(ctx, flag, false, openfeature.EvaluationContext{})
}
