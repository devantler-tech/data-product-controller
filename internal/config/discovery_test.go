package config

import (
	"strings"
	"testing"
)

// TestRegistryDiscoveryEnabled keeps omitted settings off and malformed settings actionable.
func TestRegistryDiscoveryEnabled(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value            string
		enabled, invalid bool
	}{
		{"", false, false}, {"false", false, false}, {"true", true, false}, {"sometimes", false, true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			enabled, err := RegistryDiscoveryEnabled(test.value)
			if enabled != test.enabled || (err != nil) != test.invalid {
				t.Fatalf("setting=%q enabled=%t err=%v", test.value, enabled, err)
			}
			if err != nil && !strings.Contains(err.Error(), "REGISTRY_DISCOVERY_ENABLED") {
				t.Fatalf("error does not name the setting: %v", err)
			}
		})
	}
}
