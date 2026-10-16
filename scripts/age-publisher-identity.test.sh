#!/usr/bin/env bash
# Exercise the actual pre-publication steps; only the issuer request is synthetic.
set -euo pipefail
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
publisher=$repo_root/.github/workflows/publish-age.yaml
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/bin"
yq -r '.jobs.publish.steps[0].run' "$publisher" >"$scratch/source.sh"
yq -r '.jobs.publish.steps[1].run' "$publisher" >"$scratch/identity.sh"
cat >"$scratch/bin/curl" <<'ISSUER'
#!/usr/bin/env bash
set -euo pipefail
touch "$FIXTURE_REQUEST"
[[ $FIXTURE_MODE != unavailable ]] || exit 7
cat "$FIXTURE_RESPONSE"
if [[ $FIXTURE_MODE == partial ]]; then exit 7; fi
if [[ $FIXTURE_MODE == multi ]]; then cat "$FIXTURE_RESPONSE"; fi
ISSUER
chmod +x "$scratch/bin/curl"
pin=0123456789abcdef0123456789abcdef01234567

# claims creates public synthetic issuer data, never a real credential.
claims() {
	jq -cn --arg repository "$1" --arg workflow "$1/.github/workflows/publish-age.yaml@$pin" \
		'{repository:$repository,repository_id:"1350892700",job_workflow_ref:$workflow}'
}

# run_case catches missing identity checks through observable failure and output boundaries.
run_case() {
	local wanted=$1 label=$2 repository=$3 id=$4 body=$5 mode=${6:-success}
	local event=${7:-push} ref_type=${8:-tag} tag=${9:-v5.0.0} result=0 encoded expected
	encoded=$(printf '%s' "$body" | base64 | tr -d '\r\n=' | tr '/+' '_-')
	jq -cn --arg value "fixture.$encoded.fixture" '{value:$value}' >"$scratch/response"
	: >"$scratch/output"
	rm -f "$scratch/request"
	# The nested shell expands its own positional arguments, not this shell's.
	# shellcheck disable=SC2016
	env -i PATH="$scratch/bin:$PATH" REPOSITORY="$repository" REPOSITORY_ID="$id" \
		EVENT="$event" REF_TYPE="$ref_type" RELEASE_TAG="$tag" GITHUB_OUTPUT="$scratch/output" \
		ACTIONS_ID_TOKEN_REQUEST_URL='https://issuer.example.test/token?fixture=1' \
		ACTIONS_ID_TOKEN_REQUEST_TOKEN=synthetic FIXTURE_REQUEST="$scratch/request" \
		FIXTURE_RESPONSE="$scratch/response" FIXTURE_MODE="$mode" \
		/bin/bash -c '/bin/bash "$1" && /bin/bash "$2"' fixture \
		"$scratch/source.sh" "$scratch/identity.sh" >"$scratch/stdout" 2>"$scratch/stderr" || result=$?
	if [[ $wanted == accept ]]; then
		expected="workflow=$repository/.github/workflows/publish-age.yaml@$pin"
		[[ $result == 0 && $(cat "$scratch/output") == "$expected" && -f $scratch/request ]] || {
			cat "$scratch/stderr" >&2
			echo "AGE identity positive control failed: $label" >&2
			exit 1
		}
	else
		[[ $result != 0 && $result != 124 && $result != 127 && ! -s $scratch/output ]] || {
			echo "AGE identity accepted or published an invalid identity: $label (exit $result)" >&2
			exit 1
		}
		if [[ $wanted == source ]]; then
			[[ ! -e $scratch/request ]] || {
				echo "AGE identity contacted issuer before rejecting caller: $label" >&2
				exit 1
			}
		fi
	fi
}

original=devantler-tech/data-product-controller
canonical=devantler-tech/khoist
original_claims=$(claims "$original")
# This must fail against the original name-only admission, after a real positive control.
run_case accept current "$original" 1350892700 "$original_claims"
run_case source reused-original-name "$original" 1 "$original_claims"
run_case source missing-context-id "$original" '' "$original_claims"
run_case source fork example/data-product-controller 1 "$original_claims"
run_case source non-release "$original" 1350892700 "$original_claims" success workflow_dispatch
run_case source branch "$original" 1350892700 "$original_claims" success push branch
run_case source invalid-tag "$original" 1350892700 "$original_claims" success push tag main
for repository in "$original" "$canonical"; do
	valid=$(claims "$repository")
	run_case accept "$repository" "$repository" 1350892700 "$valid"
	run_case accept proof-tag "$repository" 1350892700 "$valid" success push tag v0.0.0-age-proof.fixture
	run_case source reused-name "$repository" 1 "$valid"
	for change in \
		'.repository_id="1"' 'del(.repository_id)' '.repository_id=1350892700' \
		'.repository_id=[]' '.repository="example/fork"' \
		'.job_workflow_ref="example/fork/.github/workflows/publish-age.yaml@0123456789abcdef0123456789abcdef01234567"' \
		'.job_workflow_ref |= sub("@.*$"; "@refs/heads/main")' \
		'.job_workflow_ref |= sub("@.*$"; "@v5.0.0")' \
		'.job_workflow_ref |= sub("@.*$"; "@0123456")' \
		'del(.job_workflow_ref)'; do
		changed=$(printf '%s' "$valid" | jq -c "$change")
		run_case closed "$change" "$repository" 1350892700 "$changed"
	done
	other=$original
	if [[ $repository == "$original" ]]; then other=$canonical; fi
	changed=$(printf '%s' "$valid" | jq -c --arg other "$other" '.repository=$other')
	run_case closed cross-name-caller "$repository" 1350892700 "$changed"
	changed=$(printf '%s' "$valid" | jq -c --arg other "$other" \
		'.job_workflow_ref=$other+"/.github/workflows/publish-age.yaml@0123456789abcdef0123456789abcdef01234567"')
	run_case closed cross-name-callee "$repository" 1350892700 "$changed"
	for mode in unavailable partial multi; do
		run_case closed "$mode-issuer-response" "$repository" 1350892700 "$valid" "$mode"
	done
	run_case closed malformed-claims "$repository" 1350892700 '{'
	run_case closed non-object-claims "$repository" 1350892700 '[]'
done

# Guard execution is useful only when GitHub supplies its trusted context at both boundaries.
for step in 0 1; do
	# GitHub expressions must remain literal when inspecting workflow wiring.
	# shellcheck disable=SC2016
	STEP=$step yq -e '.jobs.publish.steps[env(STEP)].env.REPOSITORY == "${{ github.repository }}" and
    .jobs.publish.steps[env(STEP)].env.REPOSITORY_ID == "${{ github.repository_id }}"' "$publisher" >/dev/null
done

# Run the documented independent command through rejecting signer/registry boundaries.
awk '/^```bash$/{copy=1; next} copy && /^```$/{exit} copy{print}' \
	"$repo_root/docs/postgresql-age-image.md" >"$scratch/verify.sh"
cat >"$scratch/bin/cosign" <<'VERIFIER'
#!/usr/bin/env bash
set -euo pipefail
touch "$FIXTURE_SIGNER"
printf '%s\n' "$@" | cmp - "$FIXTURE_EXPECTED"
touch "$FIXTURE_VERIFIED"
VERIFIER
cat >"$scratch/bin/docker" <<'REGISTRY'
#!/usr/bin/env bash
set -euo pipefail
touch "$FIXTURE_REGISTRY"
if [[ $1 == pull && $2 == "$AGE_IMAGE" && $# == 2 ]]; then exit 0; fi
if [[ $1 == image && $2 == inspect && $3 == "$AGE_IMAGE" && $# == 5 ]]; then
  printf '%s\n' "$SOURCE_SHA"
  exit 0
fi
exit 1
REGISTRY
chmod +x "$scratch/bin/cosign" "$scratch/bin/docker"

# verify_example binds the exact documented filters; mixed identities must stop before a pull.
verify_example() {
	local wanted=$1 repository=$2 expected_repository=$3 result=0
	local image
	image="ghcr.io/devantler-tech/data-product-controller-postgresql-age@sha256:$(printf '%064d' 0)"
	printf '%s\n' verify \
		--certificate-identity "https://github.com/$expected_repository/.github/workflows/publish-age.yaml@$pin" \
		--certificate-oidc-issuer https://token.actions.githubusercontent.com \
		--certificate-github-workflow-repository "$expected_repository" \
		--certificate-github-workflow-ref refs/tags/v5.0.0 \
		--certificate-github-workflow-sha "$pin" "$image" >"$scratch/expected"
	rm -f "$scratch/signer" "$scratch/registry" "$scratch/verified"
	env -i PATH="$scratch/bin:$PATH" RELEASE_REPOSITORY="$repository" AGE_IMAGE="$image" \
		RELEASE_TAG=v5.0.0 SOURCE_SHA="$pin" PUBLISHER_SHA="$pin" \
		FIXTURE_SIGNER="$scratch/signer" FIXTURE_REGISTRY="$scratch/registry" \
		FIXTURE_VERIFIED="$scratch/verified" \
		FIXTURE_EXPECTED="$scratch/expected" /bin/bash "$scratch/verify.sh" \
		>"$scratch/stdout" 2>"$scratch/stderr" || result=$?
	if [[ $wanted == accept ]]; then
		[[ $result == 0 && -f $scratch/verified && -f $scratch/registry ]] || {
			echo "AGE documented verification rejected permitted repository: $repository" >&2
			exit 1
		}
	else
		[[ $result != 0 && ! -e $scratch/registry ]] || {
			echo "AGE documented verification continued after identity rejection: $repository" >&2
			exit 1
		}
		if [[ $wanted == source && -e $scratch/signer ]]; then
			echo "AGE documented verification contacted signer for unsupported repository" >&2
			exit 1
		fi
	fi
}
verify_example accept "$original" "$original"
verify_example accept "$canonical" "$canonical"
verify_example reject "$original" "$canonical"
verify_example reject "$canonical" "$original"
verify_example source example/fork "$original"
verify_example source '' "$original"
printf '%s\n' 'PASS: actual AGE publisher steps reject foreign callers and mismatched or incomplete issuer evidence without identity output'
