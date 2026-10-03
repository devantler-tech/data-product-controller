package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Orphan deletion removes credential owners. CNPG does not adopt an existing
// unowned application Secret, even though retained storage and passwords work.
func TestPostgresRecoveryRebindsRetainedSQLPublication(t *testing.T) {
	script, err := os.ReadFile("../postgres.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, recovery, found := strings.Cut(string(script), "\nphase 'independent source recreation and generation rebinding' 300\n")
	if !found {
		t.Fatal("source recovery phase missing")
	}
	recovery, _, found = strings.Cut(recovery, "\nphase 'independent gates")
	if !found {
		t.Fatal("source recovery phase incomplete")
	}
	var publisher string
	if _, body, exists := strings.Cut(string(script), "\nbind_sql_publication() {\n"); exists {
		body, _, complete := strings.Cut(body, "\n}\n")
		if !complete {
			t.Fatal("SQL publisher incomplete")
		}
		publisher = "\nbind_sql_publication() {\n" + body + "\n}\n"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", `set -euo pipefail
repo_root=unused
source_identity=old-source
sql_owner=old-source
document_owner=old-source
graph_owner=old-source
verified=false
remaining() { echo 30; }
retained_identities() {
	jq -nc --arg uid "$source_identity" '[{kind:"Cluster",name:"warehouse",uid:$uid},{kind:"Secret",name:"warehouse-app",uid:"retained-password"},{kind:"PersistentVolumeClaim",name:"warehouse-1",uid:"retained-storage"}]'
}
bounded() {
	[[ " $* " == *" delete cluster warehouse "* ]]
	sql_owner=''
	document_owner=''
	graph_owner=''
}
kube() {
	case "$1 $2" in
	"get cluster") printf '%s' "$source_identity" ;;
	"get configmap") printf 'independent-anchor' ;;
	"apply -f") source_identity=new-source ;;
	"patch secret")
		if [[ $3 == warehouse-app ]]; then
			[[ $verified == true ]]
			jq -e --arg uid "$source_identity" '
				(has("data")|not) and (has("stringData")|not) and
				.metadata.ownerReferences == [{apiVersion:"postgresql.cnpg.io/v1",kind:"Cluster",name:"warehouse",uid:$uid}]
			' <<<"${@: -1}" >/dev/null
			sql_owner=$source_identity
		fi
		;;
	*) echo "unexpected Kubernetes operation" >&2; return 1 ;;
	esac
}
database_ready() { return 0; }
age_profile_ready() { return 0; }
query_all() { verified=true; }
model_unavailable() {
	local owner
	case "$1" in
	sql) owner=$sql_owner ;;
	document) owner=$document_owner ;;
	graph) owner=$graph_owner ;;
	*) return 1 ;;
	esac
	[[ $2 == ConnectionOwnerMismatch && $owner != "$source_identity" ]]
}
hybrid_unavailable() { model_unavailable "$@"; }
bind_hybrid_publications() {
	document_owner=$source_identity
	graph_owner=$source_identity
}
matrix_ready() {
	[[ $sql_owner == "$source_identity" && $document_owner == "$source_identity" && $graph_owner == "$source_identity" ]] || {
		echo 'retained SQL publication has no current source owner' >&2
		return 1
	}
}
wait_for() { shift; "$@"; }
`+publisher+recovery)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("retained source recovery: %v %s", err, output)
	}
}
