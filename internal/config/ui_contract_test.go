package config

import "testing"

// TestUIContractConfiguration exercises the default-off flag and canonical publisher origin policy.
func TestUIContractConfiguration(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "false", "true", "invalid"} {
		got, err := UIContractEnabled(value)
		if got != (value == "true") || (err != nil) != (value == "invalid") {
			t.Fatalf("flag %q: %t, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "https://catalog.example", "https://127.0.0.1:8443,https://kit.example"} {
		if _, err := UIHostOrigins(value); err != nil {
			t.Fatalf("valid origin %q: %v", value, err)
		}
	}
	for _, value := range []string{
		"*", "https://*.example", "https://catalog.example/", "http://catalog.example",
		"https://user:pass@catalog.example", "https://catalog.example?", "https://catalog.example#",
		"https://catalog.example,https://catalog.example", "https://CATALOG.example",
		"https://catalog.example:443", "https://catalog.example:99999", "https://catalog.example:",
		"https://catalog.example:08443", "https://127.1", "https://-invalid.example",
	} {
		if _, err := UIHostOrigins(value); err == nil {
			t.Errorf("accepted invalid or noncanonical origin %q", value)
		}
	}
}
