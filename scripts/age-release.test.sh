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
yq -e '(.jobs."required-checks".needs | contains(["age-image"])) and ([.jobs."required-checks".steps[] | select(.env.NEEDS_JSON == "${{ toJSON(needs) }}") | .run | contains("ci-trusted/scripts/ci-plan/main.go check")] | any)' "$ci" >/dev/null || fail 'image acceptance must feed the trusted required result checker'
[ -f "$publisher" ] || fail 'owned image publisher is required'
yq -e '(.on | has("workflow_call")) and (.on | keys | length == 1)' "$publisher" >/dev/null || fail 'publisher must only be callable as a reusable workflow'
yq -e '(.permissions | length == 0) and .jobs.publish.permissions.contents == "read" and .jobs.publish.permissions.packages == "write" and .jobs.publish.permissions."id-token" == "write"' "$publisher" >/dev/null || fail 'publisher must use scoped identity and package permissions'
# A proof tag must publish only the independently versioned AGE image. Publishing
# a chart would point at a controller image intentionally absent from that tag.
for workflow in "$cd" "$chart_publisher"; do
	condition=$(yq '.jobs.publish.if' "$workflow")
	[ "$condition" = "\${{ !startsWith(github.ref_name, 'v0.0.0-age-proof.') }}" ] || fail 'proof tags must exclude controller and chart publication'
done
yq -e '.jobs.smoke.needs == "publish" and (.jobs.smoke | has("if") | not) and (.jobs."publish-age" | has("needs") | not) and (.jobs."publish-age" | has("if") | not)' "$cd" >/dev/null || fail 'proof tags must run AGE publication and skip dependent controller smoke'
callee=$(yq '.jobs."publish-age".uses' "$cd")
printf '%s\n' "$callee" | grep -Eq '^devantler-tech/data-product-controller/\.github/workflows/publish-age\.yaml@[a-f0-9]{40}$' || fail 'release must use the immutable owned publisher'
yq -e '.jobs."publish-age".permissions.contents == "read" and .jobs."publish-age".permissions.packages == "write" and .jobs."publish-age".permissions."id-token" == "write"' "$cd" >/dev/null || fail 'release must grant only the required publisher permissions'
printf '%s\n' 'AGE release contract tests passed'
