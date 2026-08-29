package config

import "testing"

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
