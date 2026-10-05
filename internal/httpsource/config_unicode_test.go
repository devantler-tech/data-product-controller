package httpsource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditConfigurationEscapedUnicodeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"unpaired high", `\ud800`, false},
		{"unpaired low", `\udc00`, false},
		{"unpaired high before ASCII", `\ud800a`, false},
		{"valid pair", `\ud83d\ude80`, true},
		{"literal replacement", `�`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(
				path,
				[]byte(
					`{"endpointURL":"https://source.example.test/`+tc.value+`","bearerToken":"read-only-token"}`,
				),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			cfg, err := readConfig(path)
			if (err == nil) != tc.valid {
				t.Fatalf(
					"accepted=%v want=%v; endpoint=%q error=%v",
					err == nil,
					tc.valid,
					cfg.endpointURL,
					err,
				)
			}
		})
	}
}
