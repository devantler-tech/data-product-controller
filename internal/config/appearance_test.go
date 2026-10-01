package config

import "testing"

// TestUIAppearanceFlag keeps operator opt-in explicit and rejects malformed settings.
func TestUIAppearanceFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value            string
		enabled, invalid bool
	}{{"", false, false}, {"false", false, false}, {"true", true, false}, {"sometimes", false, true}} {
		got, err := UIAppearanceEnabled(tc.value)
		if got != tc.enabled || (err != nil) != tc.invalid {
			t.Fatalf("value=%q: enabled=%t error=%v", tc.value, got, err)
		}
	}
}
