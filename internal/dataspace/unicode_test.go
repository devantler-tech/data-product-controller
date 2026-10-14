package dataspace_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devantler-tech/data-product-controller/internal/dataspace"
)

func TestDSPUnicodeScalarsRemainLossless(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		wire  string
		valid bool
		text  string
	}{
		{`"Sensor � retained"`, true, "Sensor � retained"},
		{`"Sensor \ufffd retained"`, true, "Sensor � retained"},
		{`"Station \ud83c\udf0a"`, true, "Station 🌊"},
		{`"Station København"`, true, "Station København"},
		{`"Literal \\ud800"`, true, `Literal \ud800`},
		{`"Station \ud800"`, false, ""},
		{`"Station \udc00"`, false, ""},
		{`"Station \ud800\ud800"`, false, ""},
		{`"Station \udc00\ud800"`, false, ""},
	} {
		t.Run(tc.wire, func(t *testing.T) {
			t.Parallel()
			original := string(example(t, "catalog"))
			source := strings.Replace(original, `"Public temperature observations."`, tc.wire, 1)
			if source == original {
				t.Fatal("Unicode fixture was not substituted")
			}
			result, err := dataspace.Export(
				strings.NewReader(source),
				bytes.NewReader(example(t, "bindings")),
			)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				var decoded any
				if err := json.Unmarshal(result, &decoded); err != nil {
					t.Fatal(err)
				}
				canonical, err := json.Marshal(decoded)
				if err != nil {
					t.Fatal(err)
				}
				want, err := json.Marshal(tc.text)
				if err != nil || !bytes.Contains(canonical, want) {
					t.Fatal("accepted Unicode text changed during export")
				}
			} else if err == nil || len(result) != 0 {
				t.Fatal("malformed Unicode admitted or partially exported")
			}
		})
	}
}
