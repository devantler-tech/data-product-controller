package featureflag_test

import (
	"context"
	"testing"

	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
)

func TestEnabled(t *testing.T) {
	t.Parallel()

	const flag = "registry-ui"

	testCases := []struct {
		name  string
		flags map[string]bool
		want  bool
	}{
		{
			name:  "enabled",
			flags: map[string]bool{flag: true},
			want:  true,
		},
		{
			name:  "disabled",
			flags: map[string]bool{flag: false},
			want:  false,
		},
		{
			name:  "missing defaults off",
			flags: map[string]bool{},
			want:  false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			provider := featureflag.NewProvider(testCase.flags)

			client, err := featureflag.NewClient(testCase.name, provider)
			if err != nil {
				t.Fatalf("NewClient(%q) returned error: %v", testCase.name, err)
			}

			got := featureflag.Enabled(context.Background(), client, flag)
			if got != testCase.want {
				t.Errorf("Enabled() = %t, want %t", got, testCase.want)
			}
		})
	}
}
