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

# Exercise the actual workflow guards with synthetic contexts; no network or
# release credentials are involved. Repository names may change, their ID may not.
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
yq -r '.jobs.publish.steps[] | select(.name == "Require the owned release source") | .run' "$publisher" > "$scratch/source.sh"
yq -r '.jobs.publish.steps[] | select(.name == "Resolve and require immutable signing workflow") | .run' "$publisher" > "$scratch/identity.sh"
mkdir "$scratch/bin"
cat > "$scratch/bin/curl" <<'MOCK'
#!/bin/sh
printf '%s\n' "$AGE_TEST_OIDC_RESPONSE"
MOCK
chmod +x "$scratch/bin/curl"

source_context() {
	REPOSITORY="$1" REPOSITORY_ID="$2" EVENT="${3:-push}" REF_TYPE="${4:-tag}" RELEASE_TAG="${5:-v4.0.0}" bash "$scratch/source.sh"
}
source_context devantler-tech/data-product-controller 1350892700 || fail 'original repository must remain admitted during preparation'
if source_context devantler-tech/data-product-controller 999; then fail 'reused former name was admitted'; fi
if source_context devantler-tech/kweft 999; then fail 'foreign renamed repository ID was admitted'; fi
if source_context someone/kweft 1350892700; then fail 'fork repository was admitted'; fi
if source_context devantler-tech/another 1350892700; then fail 'unregistered repository name was admitted'; fi
source_context devantler-tech/kweft 1350892700 || fail 'renamed owned repository must be admitted'
if source_context devantler-tech/kweft 1350892700 pull_request; then fail 'pull-request context was admitted'; fi
if source_context devantler-tech/kweft 1350892700 push branch; then fail 'branch context was admitted'; fi
if source_context devantler-tech/kweft 1350892700 push tag latest; then fail 'non-release tag was admitted'; fi

identity_context() {
	repository="$1"
	workflow="$2"
	claim_id="${3:-1350892700}"
	claim_repository="${4:-$repository}"
	payload=$(jq -cn --arg workflow "$workflow" --arg id "$claim_id" --arg repository "$claim_repository" '{job_workflow_ref:$workflow,repository_id:$id,repository:$repository}' | base64 | tr -d '\n=' | tr '/+' '_-')
	response=$(jq -cn --arg token "test.$payload.test" '{value:$token}')
	PATH="$scratch/bin:$PATH" AGE_TEST_OIDC_RESPONSE="$response" REPOSITORY="$repository" REPOSITORY_ID=1350892700 ACTIONS_ID_TOKEN_REQUEST_TOKEN=synthetic ACTIONS_ID_TOKEN_REQUEST_URL=https://oidc.invalid GITHUB_OUTPUT="$scratch/output" bash "$scratch/identity.sh"
}
pin=0123456789abcdef0123456789abcdef01234567
for repository in devantler-tech/data-product-controller devantler-tech/kweft; do
	identity_context "$repository" "$repository/.github/workflows/publish-age.yaml@$pin" || fail 'owned immutable signing workflow must be admitted'
	if identity_context "$repository" "$repository/.github/workflows/publish-age.yaml@main"; then fail 'floating signing workflow was admitted'; fi
	if identity_context "$repository" "someone/kweft/.github/workflows/publish-age.yaml@$pin"; then fail 'foreign signing workflow was admitted'; fi
	if identity_context "$repository" "$repository/.github/workflows/publish-age.yaml@$pin" 999; then fail 'foreign OIDC repository ID was admitted'; fi
	if identity_context "$repository" "$repository/.github/workflows/publish-age.yaml@$pin" 1350892700 someone/kweft; then fail 'mismatched OIDC repository name was admitted'; fi
done
if identity_context devantler-tech/kweft "devantler-tech/data-product-controller/.github/workflows/publish-age.yaml@$pin"; then fail 'signing workflow from another repository name was admitted'; fi
printf '%s\n' 'AGE release contract tests passed'
