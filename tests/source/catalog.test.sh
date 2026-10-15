#!/usr/bin/env bash
set -euo pipefail
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
cat >"$scratch/run.sh" <<'FIXTURE'
#!/usr/bin/env bash
set -euo pipefail
test_dir=$CATALOG_TEST_WORK
repo_root=$CATALOG_TEST_ROOT
cluster_context=owned-fixture
integration_deadline=$((SECONDS + 60))
KUBECONFIG=$test_dir/config
# This unit fixture isolates orchestration; endpoint behavior is exercised by hosted acceptance.
probe() { :; }
# Chart installation is outside the absence-read regression under test.
install_chart() { :; }
# Evaluate the real absence predicate immediately instead of retrying a synthetic response.
wait_for() { shift 2; "$@"; }
# Inject absent, present, partial or failed reads at the selected pre/post-import boundary.
kube() {
 case "$*" in
 'get dataproduct edc-harbour edc-buoy --ignore-not-found -o name')
  local read=1
  if [[ -f $test_dir/read ]]; then read=$(($(cat "$test_dir/read") + 1)); fi
  printf '%s\n' "$read" >"$test_dir/read"
  if [[ $read == "$CATALOG_TEST_READ" ]]; then
   case "$CATALOG_TEST_MODE" in
   absent) ;;
   present) printf 'dataproduct.data.devantler.tech/edc-buoy\n' ;;
   unavailable) return 8 ;;
   partial) printf 'dataproduct.data.devantler.tech/edc-harbour\n'; return 8 ;;
   *) return 99 ;;
   esac
  fi ;;
 "apply -f $repo_root/tests/dataspace/edc/products.yaml") touch "$test_dir/applied" ;;
 "delete -f $repo_root/tests/dataspace/edc/products.yaml --wait=true --timeout=20s") touch "$test_dir/deleted" ;;
 'annotate dataproduct catalog-harbour data.devantler.tech/dcat-type-') touch "$test_dir/finished"; return 77 ;;
 "apply -f $repo_root/docs/examples/dcat-product.yaml"|'--request-timeout=0 rollout status deployment/dpc --timeout=180s') ;;
 *) return 99 ;;
 esac
}
# Supply synthetic catalog bytes without contacting Kubernetes.
kubectl() { printf '{}\n'; }
# Record importer reachability while refusing every other subprocess request.
bash() {
 [[ $1 == "$repo_root/tests/source/edc-import.sh" ]] || return 99
 touch "$test_dir/imported"
}
source "$repo_root/tests/source/catalog.sh"
FIXTURE
export CATALOG_TEST_ROOT=$repo_root
for stage in 1 2; do
	for mode in absent present unavailable partial; do
		work=$scratch/$stage-$mode
		mkdir "$work"
		result=0
		CATALOG_TEST_WORK=$work CATALOG_TEST_READ=$stage CATALOG_TEST_MODE=$mode \
			"$BASH" "$scratch/run.sh" >"$work/output" 2>&1 || result=$?
		if [[ $mode == absent ]]; then
			[[ $result == 77 && -f $work/finished && -f $work/imported && -f $work/deleted &&
				$(cat "$work/read") == 2 ]] || {
				cat "$work/output" >&2
				echo 'EDC catalog preflight/cleanup must accept confirmed absence and reach the importer' >&2
				exit 1
			}
		else
			[[ $result != 0 && $result != 77 && ! -f $work/finished ]] || exit 1
			if [[ $stage == 1 ]]; then
				[[ ! -f $work/applied && ! -f $work/imported ]]
			else
				[[ -f $work/imported && -f $work/deleted ]]
			fi
		fi
	done
done
echo 'PASS: actual catalog harness reaches EDC only after confirmed absence and rejects present, partial or unavailable reads before/after import'
