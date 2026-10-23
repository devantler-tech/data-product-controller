#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
ci="$repo_root/.github/workflows/ci.yaml"
cd="$repo_root/.github/workflows/cd.yaml"
publisher="$repo_root/.github/workflows/publish-age.yaml"
chart_publisher="$repo_root/.github/workflows/publish-chart.yaml"
# fail rejects a violated AGE publication contract.
fail() {
	printf 'AGE release contract failed: %s\n' "$1" >&2
	exit 1
}

# An image declaration is not acceptance: both CI and its aggregate must
# require a PostgreSQL process, real Cypher reads and SQL authorization denials.
yq -e '.jobs."age-image".permissions.contents == "read" and .jobs."age-image"."timeout-minutes" <= 15' "$ci" >/dev/null || fail 'bounded read-only image acceptance is required'
AGE_RELEASE_NEEDS_JSON="\${{ toJSON(needs) }}" yq -e '(.jobs."required-checks".needs | contains(["age-image"])) and ([.jobs."required-checks".steps[] | select(.env.NEEDS_JSON == strenv(AGE_RELEASE_NEEDS_JSON)) | .run | contains("ci-trusted/scripts/ci-plan/main.go check")] | any)' "$ci" >/dev/null || fail 'image acceptance must feed the trusted required result checker'
[ ! -e "$publisher" ] || fail 'single-use AGE publisher must not remain a reusable export'
workflow_json=$(yq -o=json '.' "$cd") || fail 'CD workflow must parse completely'
printf '%s\n' "$workflow_json" | jq -se 'length == 1 and (.[0] | type == "object" and
  (.permissions | length == 0) and (.on | keys == ["push"]) and .on.push.tags == ["v*"] and
  .jobs."publish-age"."runs-on" == "ubuntu-latest" and .jobs."publish-age"."timeout-minutes" == 15 and
  (.jobs."publish-age" | has("uses") | not) and
  (.jobs."publish-age".steps | length == 7))' >/dev/null || fail 'AGE publication must be an ordinary bounded CD job'
# A proof tag must publish only the independently versioned AGE image. Publishing
# a chart would point at a controller image intentionally absent from that tag.
for workflow in "$cd" "$chart_publisher"; do
	condition=$(yq '.jobs.publish.if' "$workflow")
	[ "$condition" = "\${{ !startsWith(github.ref_name, 'v0.0.0-age-proof.') }}" ] || fail 'proof tags must exclude controller and chart publication'
done
yq -e '.jobs.smoke.needs == "publish" and (.jobs.smoke | has("if") | not) and (.jobs."publish-age" | has("needs") | not) and (.jobs."publish-age" | has("if") | not)' "$cd" >/dev/null || fail 'proof tags must run AGE publication and skip dependent controller smoke'
yq -e '.jobs."publish-age".permissions.contents == "read" and .jobs."publish-age".permissions.packages == "write" and .jobs."publish-age".permissions."id-token" == "write"' "$cd" >/dev/null || fail 'release must grant only the required publisher permissions'
bash "$repo_root/scripts/age-publisher-identity.test.sh"
printf '%s\n' 'AGE release contract tests passed'
