package v1

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestHybridAGEPreloadRequiresDeclaredLibrary rejects missing or ambiguously typed preload intent.
func TestHybridAGEPreloadRequiresDeclaredLibrary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		preload any
		want    bool
	}{
		{"AGE declared", []any{"age"}, true},
		{"another extension also declared", []any{"pg_stat_statements", "age"}, true},
		{"missing declaration", nil, false},
		{"empty declaration", []any{}, false},
		{"scalar declaration", "age", false},
		{"mixed types", []any{"age", int64(1)}, false},
		{"uppercase spelling", []any{"AGE"}, false},
		{"shared object name", []any{"age.so"}, false},
		{"suffix match", []any{"stage"}, false},
		{"leading whitespace", []any{" age"}, false},
		{"trailing whitespace", []any{"age "}, false},
		{"comma separated entry", []any{"pg_stat_statements,age"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := &unstructured.Unstructured{Object: map[string]any{
				"spec": map[string]any{"postgresql": map[string]any{
					"parameters": map[string]any{"shared_preload_libraries": "age"},
				}},
				"status": map[string]any{"shared_preload_libraries": []any{"age"}},
			}}
			if tc.preload != nil {
				if err := unstructured.SetNestedField(cluster.Object, tc.preload,
					"spec", "postgresql", "shared_preload_libraries"); err != nil {
					t.Fatal(err)
				}
			}
			if got := hybridAGEPreload(cluster); got != tc.want {
				t.Fatalf("declared AGE preload=%v, want %v", got, tc.want)
			}
		})
	}
}
