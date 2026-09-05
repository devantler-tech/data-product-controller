#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir "$work/bin"

fail() {
	printf '%s\n' "chart signing test failed: $1" >&2
	exit 1
}

# Run the publication code with controlled registry and signer boundaries.
yq '.jobs.publish.steps[] | select(.name == "Package and publish") | .run' \
	"$repo_root/.github/workflows/publish-chart.yaml" >"$work/publish.sh"
cat >"$work/bin/helm" <<'EOF'
#!/bin/sh
set -eu
printf 'helm %s\n' "$*" >>"$CALLS"
case "$1" in
registry) cat >/dev/null ;;
package) exit "${PACKAGE_EXIT:-0}" ;;
push)
	printf '%s\n' 'Pushed: ghcr.io/devantler-tech/charts/data-product-controller:1.2.3' >&2
	printf '%s\n' "$PUSH_OUTPUT" >&2
	exit "${PUSH_EXIT:-0}"
	;;
*) exit 90 ;;
esac
EOF
cat >"$work/bin/cosign" <<'EOF'
#!/bin/sh
set -eu
printf 'cosign %s\n' "$*" >>"$CALLS"
case "$1" in
login) cat >/dev/null ;;
sign) exit "${SIGN_EXIT:-0}" ;;
verify) exit "${VERIFY_EXIT:-0}" ;;
*) exit 91 ;;
esac
EOF
chmod +x "$work/bin/helm" "$work/bin/cosign"

export PATH="$work/bin:$PATH" CALLS="$work/calls"
export GHCR_TOKEN='test-token' GHCR_USER='test-user' RELEASE_TAG='v1.2.3'
export GITHUB_WORKFLOW_REF='devantler-tech/data-product-controller/.github/workflows/publish-chart.yaml@refs/tags/v1.2.3'
digest='sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
artifact="ghcr.io/devantler-tech/charts/data-product-controller@$digest"
export PUSH_OUTPUT="Digest: $digest"

run_publish() {
	: >"$CALLS"
	bash "$work/publish.sh" >"$work/output" 2>&1
}

run_publish || fail 'valid publication failed'
grep -Fx "cosign sign --yes $artifact" "$CALLS" >/dev/null ||
	fail 'the digest returned by helm push must be signed'
grep -Fx "cosign verify --certificate-identity https://github.com/$GITHUB_WORKFLOW_REF --certificate-oidc-issuer https://token.actions.githubusercontent.com $artifact" "$CALLS" >/dev/null ||
	fail 'the same digest must be verified against the tagged workflow identity'
tail -n 2 "$CALLS" >"$work/signing-calls"
[ "$(sed -n '1p' "$work/signing-calls")" = "cosign sign --yes $artifact" ] ||
	fail 'signing must follow publication and precede verification'

for output in '' 'Digest: sha256:bad' "Digest: $digest
Digest: $digest"; do
	export PUSH_OUTPUT="$output"
	if run_publish; then fail 'missing, malformed, or ambiguous push digest was accepted'; fi
	if grep '^cosign sign ' "$CALLS" >/dev/null; then fail 'an invalid digest was signed'; fi
done
export PUSH_OUTPUT="Digest: $digest"
export PUSH_EXIT=42
if run_publish; then fail 'failed push was accepted'; fi
if grep '^cosign sign ' "$CALLS" >/dev/null; then fail 'a failed push was signed'; fi
unset PUSH_EXIT
export PACKAGE_EXIT=43
if run_publish; then fail 'failed packaging was accepted'; fi
if grep '^helm push ' "$CALLS" >/dev/null; then fail 'a failed package was published'; fi
unset PACKAGE_EXIT
export SIGN_EXIT=44
if run_publish; then fail 'failed signing was accepted'; fi
if grep '^cosign verify ' "$CALLS" >/dev/null; then fail 'failed signing reached verification'; fi
unset SIGN_EXIT
export VERIFY_EXIT=45
if run_publish; then fail 'failed signature verification was accepted'; fi
unset VERIFY_EXIT

printf '%s\n' 'chart signing behavior tests passed'
