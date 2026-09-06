package config

import (
	"strings"
	"testing"
)

// TestConnectorReadinessEnabled verifies default-off configuration and rejects malformed release settings.
func TestConnectorReadinessEnabled(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value         string
		want, invalid bool
	}{{value: ""}, {value: "false"}, {value: "true", want: true}, {value: "sometimes", invalid: true}} {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			got, err := ConnectorReadinessEnabled(test.value)
			if got != test.want || (err != nil) != test.invalid {
				t.Fatalf("value %q: enabled=%t, error=%v", test.value, got, err)
			}
			if err != nil && !strings.Contains(err.Error(), "CONNECTOR_READINESS_ENABLED") {
				t.Fatalf("invalid setting not named: %v", err)
			}
		})
	}
}

// TestProvisionedSourcesEnabled verifies default-off parsing and actionable errors for invalid settings.
func TestProvisionedSourcesEnabled(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value           string
		want, wantError bool
	}{
		{value: ""}, {value: "false"}, {value: "true", want: true}, {value: "sometimes", wantError: true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			got, err := ProvisionedSourcesEnabled(test.value)
			if got != test.want || (err != nil) != test.wantError {
				t.Fatalf("enabled = %t, error = %v", got, err)
			}
			if err != nil && !strings.Contains(err.Error(), "PROVISIONED_SOURCES_ENABLED") {
				t.Fatalf("error must name the invalid setting: %v", err)
			}
		})
	}
}

func TestRegistryUIEnabledDefaultsOffAndRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		want      bool
		wantError bool
	}{
		{name: "unset", value: "", want: false},
		{name: "enabled", value: "true", want: true},
		{name: "disabled", value: "false", want: false},
		{name: "invalid", value: "sometimes", want: false, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := RegistryUIEnabled(test.value)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %t", err, test.wantError)
			}
			if got != test.want {
				t.Fatalf("enabled = %t, want %t", got, test.want)
			}
		})
	}
}
