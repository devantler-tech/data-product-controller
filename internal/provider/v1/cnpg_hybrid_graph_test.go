package v1

import (
	"strings"
	"testing"
)

// TestHybridGraphPublication requires current read-only AGE intent and a bounded graph identifier.
func TestHybridGraphPublication(t *testing.T) {
	t.Parallel()
	const prefix = "data.devantler.tech/cnpg-hybrid-"
	for _, tc := range []struct {
		name, field, value string
		generation         int64
		want               bool
	}{
		{name: "current AGE graph", generation: 3, want: true},
		{name: "different extension version", field: "capability", value: "age/1.6.0", generation: 3},
		{name: "JSONB publication", field: "capability", value: "jsonb/v1", generation: 3},
		{name: "missing graph", field: "graph", generation: 3},
		{name: "one-character graph", field: "graph", value: "a", generation: 3, want: true},
		{name: "maximum graph length", field: "graph", value: strings.Repeat("a", 63), generation: 3, want: true},
		{name: "mixed-case graph", field: "graph", value: "Lineage", generation: 3},
		{name: "leading digit", field: "graph", value: "1lineage", generation: 3},
		{name: "qualified graph", field: "graph", value: "public.lineage", generation: 3},
		{name: "path in graph name", field: "graph", value: "public/lineage", generation: 3},
		{name: "missing capability", field: "capability", generation: 3},
		{name: "unbounded graph", field: "graph", value: strings.Repeat("a", 64), generation: 3},
		{name: "query in graph name", field: "graph", value: "lineage'); DROP", generation: 3},
		{name: "read-write declaration", field: "access", value: "read-write", generation: 3},
		{name: "superuser", field: "user", value: "postgres", generation: 3},
		{name: "reserved application role", field: "user", value: "cnpg_reader", generation: 3},
		{name: "stale generation", generation: 4},
		{name: "zero generation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			annotations := map[string]string{
				prefix + "publication":       "v1",
				prefix + "access":            "read-only",
				prefix + "source-generation": "3",
				prefix + "database":          "catalog",
				prefix + "user":              "graph_reader",
				prefix + "capability":        "age/1.7.0",
				prefix + "graph":             "lineage",
			}
			if tc.field != "" {
				annotations[prefix+tc.field] = tc.value
			}
			if got := hybridPublication(annotations, tc.generation, "graph"); got != tc.want {
				t.Fatalf("graph publication accepted=%t, want %t", got, tc.want)
			}
		})
	}
}
