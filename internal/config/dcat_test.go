package config

import (
	"strings"
	"testing"
)

// TestDCATCatalogEnabled prevents accidental activation and rejects malformed release settings.
func TestDCATCatalogEnabled(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, value   string
		want, invalid bool
	}{
		{name: "unset"},
		{name: "disabled", value: "false"},
		{name: "enabled", value: "true", want: true},
		{name: "invalid", value: "sometimes", invalid: true},
		{name: "whitespace", value: " true ", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := DCATCatalogEnabled(test.value)
			if got != test.want || (err != nil) != test.invalid {
				t.Fatalf(
					"enabled = %t, error = %v; want %t, invalid = %t",
					got,
					err,
					test.want,
					test.invalid,
				)
			}
			if err != nil && !strings.Contains(err.Error(), "DCAT_CATALOG_ENABLED") {
				t.Fatalf("invalid setting not named: %v", err)
			}
		})
	}
}
