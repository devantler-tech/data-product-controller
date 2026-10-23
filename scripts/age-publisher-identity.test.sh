#!/usr/bin/env bash
# Exercise the actual pre-publication steps; only the issuer request is synthetic.
set -euo pipefail
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
publisher=$repo_root/.github/workflows/cd.yaml
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/bin"
yq -r '.jobs."publish-age".steps[0].run' "$publisher" >"$scratch/source.sh"
yq -r '.jobs."publish-age".steps[1].run' "$publisher" >"$scratch/identity.sh"
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
	jq -cn --arg repository "$1" --arg workflow "$1/.github/workflows/cd.yaml@refs/tags/${2:-v5.0.0}" \
		--arg sha "$pin" --arg ref "refs/tags/${2:-v5.0.0}" \
		'{iss:"https://token.actions.githubusercontent.com",aud:"sigstore",repository:$repository,
      repository_id:"1350892700",event_name:"push",ref_type:"tag",ref:$ref,
      workflow_ref:$workflow,workflow_sha:$sha,sha:$sha}'
}

# run_case catches missing identity checks through observable failure and output boundaries.
run_case() {
	local wanted=$1 label=$2 repository=$3 id=$4 body=$5 mode=${6:-success}
	local event=${7:-push} ref_type=${8:-tag} tag=${9:-v5.0.0} result=0 encoded expected
	local context_workflow=${10-"$repository/.github/workflows/cd.yaml@refs/tags/$tag"}
	local context_workflow_sha=${11-"$pin"} context_source_sha=${12-"$pin"}
	encoded=$(printf '%s' "$body" | base64 | tr -d '\r\n=' | tr '/+' '_-')
	jq -cn --arg value "fixture.$encoded.fixture" '{value:$value}' >"$scratch/response"
	: >"$scratch/output"
	rm -f "$scratch/request"
	# The nested shell expands its own positional arguments, not this shell's.
	# shellcheck disable=SC2016
	env -i PATH="$scratch/bin:$PATH" REPOSITORY="$repository" REPOSITORY_ID="$id" \
		EVENT="$event" REF_TYPE="$ref_type" RELEASE_TAG="$tag" GITHUB_OUTPUT="$scratch/output" \
		WORKFLOW_REF="$context_workflow" WORKFLOW_SHA="$context_workflow_sha" SOURCE_SHA="$context_source_sha" \
		ACTIONS_ID_TOKEN_REQUEST_URL='https://issuer.example.test/token?fixture=1' \
		ACTIONS_ID_TOKEN_REQUEST_TOKEN=synthetic FIXTURE_REQUEST="$scratch/request" \
		FIXTURE_RESPONSE="$scratch/response" FIXTURE_MODE="$mode" \
		/bin/bash -c '/bin/bash "$1" && /bin/bash "$2"' fixture \
		"$scratch/source.sh" "$scratch/identity.sh" >"$scratch/stdout" 2>"$scratch/stderr" || result=$?
	if [[ $wanted == accept ]]; then
		expected="workflow=$repository/.github/workflows/cd.yaml@refs/tags/$tag"
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
	# The real ordinary-CD issuer supplies matching job metadata as well.
	matching=$(printf '%s' "$valid" | jq -c '.job_workflow_ref=.workflow_ref | .job_workflow_sha=.workflow_sha')
	run_case accept matching-job-metadata "$repository" 1350892700 "$matching"
	for change in \
		'del(.job_workflow_ref)' 'del(.job_workflow_sha)' \
		'.job_workflow_ref=null' '.job_workflow_sha=null' \
		'.job_workflow_ref=[]' '.job_workflow_sha=123' \
		'.job_workflow_ref |= sub("cd.yaml"; "publish-age.yaml")' \
		'.job_workflow_ref |= sub("@.*$"; "@refs/heads/main")' \
		'.job_workflow_sha="ffffffffffffffffffffffffffffffffffffffff"' \
		'.repository_id="1"' '.sha="ffffffffffffffffffffffffffffffffffffffff"' \
		'.event_name="workflow_dispatch"'; do
		changed=$(printf '%s' "$matching" | jq -c "$change")
		run_case closed "matching metadata: $change" "$repository" 1350892700 "$changed"
	done
	changed=$(printf '%s' "$valid" | jq -c '.aud="sensitive-claim-value-must-not-appear"')
	run_case closed sanitized-diagnostics "$repository" 1350892700 "$changed"
	grep -Fq '"audience":false' "$scratch/stderr" || {
		echo 'AGE identity refusal must report a fixed Boolean binding verdict' >&2
		exit 1
	}
	if grep -Fq 'sensitive-claim-value-must-not-appear' "$scratch/stderr" ||
		grep -Fq "$(jq -r .value "$scratch/response")" "$scratch/stderr"; then
		echo 'AGE identity diagnostic exposed a claim value or issuer token' >&2
		exit 1
	fi
	run_case accept proof-tag "$repository" 1350892700 "$(claims "$repository" v0.0.0-age-proof.fixture)" success push tag v0.0.0-age-proof.fixture
	run_case source reused-name "$repository" 1 "$valid"
	run_case source foreign-context-workflow "$repository" 1350892700 "$valid" success push tag v5.0.0 'example/fork/.github/workflows/cd.yaml@refs/tags/v5.0.0'
	run_case source missing-context-workflow "$repository" 1350892700 "$valid" success push tag v5.0.0 ''
	run_case source mismatched-context-workflow-sha "$repository" 1350892700 "$valid" success push tag v5.0.0 "$repository/.github/workflows/cd.yaml@refs/tags/v5.0.0" ffffffffffffffffffffffffffffffffffffffff
	run_case source missing-context-workflow-sha "$repository" 1350892700 "$valid" success push tag v5.0.0 "$repository/.github/workflows/cd.yaml@refs/tags/v5.0.0" ''
	run_case source missing-context-source-sha "$repository" 1350892700 "$valid" success push tag v5.0.0 "$repository/.github/workflows/cd.yaml@refs/tags/v5.0.0" "$pin" ''
	for change in \
		'.repository_id="1"' 'del(.repository_id)' '.repository_id=1350892700' \
		'.repository_id=[]' '.repository="example/fork"' \
		'.workflow_ref="example/fork/.github/workflows/cd.yaml@refs/tags/v5.0.0"' \
		'.workflow_ref |= sub("@.*$"; "@refs/heads/main")' \
		'.workflow_ref |= sub("@.*$"; "@refs/tags/v5.0.1")' \
		'.workflow_ref |= sub("cd.yaml"; "publish-age.yaml")' \
		'del(.workflow_ref)' '.workflow_sha="ffffffffffffffffffffffffffffffffffffffff"' \
		'del(.workflow_sha)' '.sha="ffffffffffffffffffffffffffffffffffffffff"' 'del(.sha)' \
		'.ref="refs/heads/main"' 'del(.ref)' '.ref_type="branch"' 'del(.ref_type)' \
		'.event_name="workflow_dispatch"' 'del(.event_name)' \
		'.iss="https://issuer.example.test"' 'del(.iss)' '.aud="wrong"' 'del(.aud)' \
		'.job_workflow_ref="devantler-tech/khoist/.github/workflows/publish-age.yaml@0123456789abcdef0123456789abcdef01234567"' \
		'.job_workflow_sha="0123456789abcdef0123456789abcdef01234567"'; do
		changed=$(printf '%s' "$valid" | jq -c "$change")
		run_case closed "$change" "$repository" 1350892700 "$changed"
	done
	other=$original
	if [[ $repository == "$original" ]]; then other=$canonical; fi
	changed=$(printf '%s' "$valid" | jq -c --arg other "$other" '.repository=$other')
	run_case closed cross-name-caller "$repository" 1350892700 "$changed"
	changed=$(printf '%s' "$valid" | jq -c --arg other "$other" \
		'.workflow_ref=$other+"/.github/workflows/cd.yaml@refs/tags/v5.0.0"')
	run_case closed cross-name-callee "$repository" 1350892700 "$changed"
	for mode in unavailable partial multi; do
		run_case closed "$mode-issuer-response" "$repository" 1350892700 "$valid" "$mode"
	done
	run_case closed malformed-claims "$repository" 1350892700 '{'
	[[ $(cat "$scratch/stderr") == '{"claims_object":false}' ]] || {
		echo 'Malformed claims must produce only a fixed refusal diagnostic' >&2
		exit 1
	}
	run_case closed non-object-claims "$repository" 1350892700 '[]'
done

# Guard execution is useful only when GitHub supplies its trusted context at both boundaries.
for step in 0 1; do
	# GitHub expressions must remain literal when inspecting workflow wiring.
	# shellcheck disable=SC2016
	STEP=$step yq -e '.jobs."publish-age".steps[env(STEP)].env.REPOSITORY == "${{ github.repository }}" and
    .jobs."publish-age".steps[env(STEP)].env.REPOSITORY_ID == "${{ github.repository_id }}" and
    .jobs."publish-age".steps[env(STEP)].env.EVENT == "${{ github.event_name }}" and
    .jobs."publish-age".steps[env(STEP)].env.REF_TYPE == "${{ github.ref_type }}" and
    .jobs."publish-age".steps[env(STEP)].env.RELEASE_TAG == "${{ github.ref_name }}" and
    .jobs."publish-age".steps[env(STEP)].env.SOURCE_SHA == "${{ github.sha }}" and
    .jobs."publish-age".steps[env(STEP)].env.WORKFLOW_REF == "${{ github.workflow_ref }}" and
    .jobs."publish-age".steps[env(STEP)].env.WORKFLOW_SHA == "${{ github.workflow_sha }}"' "$publisher" >/dev/null
done

# Run the documented independent command through rejecting signer/registry boundaries.
awk '/^```bash$/{copy=1; next} copy && /^```$/{exit} copy{print}' \
	"$repo_root/docs/postgresql-age-image.md" >"$scratch/verify.sh"
awk '/^```bash$/{block++; copy=(block==2); next} copy && /^```$/{exit} copy{print}' \
	"$repo_root/docs/postgresql-age-image.md" >"$scratch/verify-current.sh"
[[ -s $scratch/verify-current.sh ]] || {
	echo 'Current CD verification example is missing' >&2
	exit 1
}
cat >"$scratch/bin/cosign" <<'VERIFIER'
#!/usr/bin/env bash
set -euo pipefail
[[ -n ${DOCKER_CONFIG:-} && -d $DOCKER_CONFIG && $DOCKER_CONFIG != "$FIXTURE_AUTH_CONFIG" ]]
jq -e '. == {auths:{"ghcr.io":{}}}' "$DOCKER_CONFIG/config.json" >/dev/null
[[ -z ${DOCKER_AUTH_CONFIG+x} && -z ${COSIGN_REPOSITORY+x} && -z ${DOCKER_CONTEXT+x} ]]
printf '%s\n' "$DOCKER_CONFIG" >"$FIXTURE_ISOLATED_CONFIG"
touch "$FIXTURE_SIGNER"
printf '%s\n' "$@" | cmp - "$FIXTURE_EXPECTED"
touch "$FIXTURE_VERIFIED"
VERIFIER
cat >"$scratch/bin/docker" <<'REGISTRY'
#!/usr/bin/env bash
set -euo pipefail
[[ -n ${DOCKER_CONFIG:-} && -d $DOCKER_CONFIG && $DOCKER_CONFIG != "$FIXTURE_AUTH_CONFIG" ]]
jq -e '. == {auths:{"ghcr.io":{}}}' "$DOCKER_CONFIG/config.json" >/dev/null
[[ -z ${DOCKER_AUTH_CONFIG+x} && -z ${COSIGN_REPOSITORY+x} && -z ${DOCKER_CONTEXT+x} ]]
[[ $(cat "$FIXTURE_ISOLATED_CONFIG") == "$DOCKER_CONFIG" ]]
touch "$FIXTURE_REGISTRY"
if [[ $1 == pull && $2 == "$AGE_IMAGE" && $# == 2 ]]; then exit 0; fi
if [[ $1 == image && $2 == inspect && $3 == "$AGE_IMAGE" && $# == 5 ]]; then
  printf '%s\n' "$SOURCE_SHA"
  exit 0
fi
exit 1
REGISTRY
chmod +x "$scratch/bin/cosign" "$scratch/bin/docker"
mkdir "$scratch/authenticated"
printf '%s\n' '{"auths":{"ghcr.io":{"auth":"synthetic-saved-login"}}}' >"$scratch/authenticated/config.json"
cp "$scratch/authenticated/config.json" "$scratch/original-auth.json"

# verify_example binds the exact documented filters; mixed identities must stop before a pull.
verify_example() {
	local wanted=$1 repository=$2 expected_repository=$3 profile=${4:-historical} result=0
	local image identity script
	identity="https://github.com/$expected_repository/.github/workflows/publish-age.yaml@$pin"
	script=$scratch/verify.sh
	if [[ $profile == current ]]; then
		identity="https://github.com/$expected_repository/.github/workflows/cd.yaml@refs/tags/v5.0.0"
		script=$scratch/verify-current.sh
	fi
	image="ghcr.io/devantler-tech/data-product-controller-postgresql-age@sha256:$(printf '%064d' 0)"
	printf '%s\n' verify \
		--certificate-identity "$identity" \
		--certificate-oidc-issuer https://token.actions.githubusercontent.com \
		--certificate-github-workflow-repository "$expected_repository" \
		--certificate-github-workflow-ref refs/tags/v5.0.0 \
		--certificate-github-workflow-sha "$pin" "$image" >"$scratch/expected"
	rm -f "$scratch/signer" "$scratch/registry" "$scratch/verified" "$scratch/isolated-config"
	env -i PATH="$scratch/bin:$PATH" RELEASE_REPOSITORY="$repository" AGE_IMAGE="$image" \
		RELEASE_TAG=v5.0.0 SOURCE_SHA="$pin" PUBLISHER_SHA="$pin" \
		DOCKER_CONFIG="$scratch/authenticated" FIXTURE_AUTH_CONFIG="$scratch/authenticated" \
		DOCKER_AUTH_CONFIG='{"auths":{"ghcr.io":{"auth":"synthetic-env-login"}}}' \
		COSIGN_REPOSITORY=ghcr.io/example/signature-override \
		DOCKER_CONTEXT=synthetic-saved-context \
		FIXTURE_ISOLATED_CONFIG="$scratch/isolated-config" \
		FIXTURE_SIGNER="$scratch/signer" FIXTURE_REGISTRY="$scratch/registry" \
		FIXTURE_VERIFIED="$scratch/verified" \
		FIXTURE_EXPECTED="$scratch/expected" /bin/bash "$script" \
		>"$scratch/stdout" 2>"$scratch/stderr" || result=$?
	cmp "$scratch/original-auth.json" "$scratch/authenticated/config.json"
	if [[ -f $scratch/isolated-config ]]; then
		local isolated_config
		IFS= read -r isolated_config <"$scratch/isolated-config"
		[[ -n $isolated_config && ! -e $isolated_config ]] || {
			echo 'AGE documented verification leaked its disposable registry configuration' >&2
			exit 1
		}
	fi
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
verify_example accept "$original" "$original" current
verify_example accept "$canonical" "$canonical" current
verify_example reject "$original" "$canonical" current
verify_example reject "$canonical" "$original" current
verify_example source example/fork "$original" current
verify_example source '' "$original" current
printf '%s\n' 'PASS: actual AGE publisher steps reject foreign callers and mismatched or incomplete issuer evidence without identity output'
