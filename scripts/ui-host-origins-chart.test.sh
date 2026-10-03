#!/bin/sh
set -eu
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
chart="$repo_root/charts/data-product-controller"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail() {
	printf '%s\n' "UI host origin chart test: $1" >&2
	exit 1
}

# Catch a renderer that approves the kit in only one side of the publisher contract.
for contract in false true; do
	for appearance in false true; do
		rendered=$(helm template controller "$chart" --namespace data-product-system \
			--set route.enabled=true --set route.host=catalog.example \
			--set uiContract.enabled="$contract" --set uiAppearance.enabled="$appearance" \
			--set-string 'uiContract.additionalHostOrigins[0]=https://kit.example:8443')
		manifest=$(printf '%s' "$rendered" | yq ea 'select(.kind == "DataProduct") | .spec.ui.contract.hostOrigins[]' -)
		publisher=$(printf '%s' "$rendered" | yq ea 'select(.kind == "Deployment") | .spec.template.spec.containers[] | select(.name == "product") | .env[] | select(.name == "UI_HOST_ORIGINS") | .value' -)
		if [ "$contract" = true ]; then
			[ "$manifest" = "$(printf 'https://catalog.example\nhttps://kit.example:8443')" ] || fail 'descriptor is missing a configured host'
			[ "$publisher" = 'https://catalog.example,https://kit.example:8443' ] || fail 'publisher approval differs from descriptor'
		else
			[ -z "$publisher" ] || fail 'disabled publisher received host grants'
			[ -z "$manifest" ] || fail 'disabled descriptor received host grants'
		fi
	done
done

reject() {
	if helm template controller "$chart" --set route.enabled=true --set route.host=catalog.example \
		--set uiContract.enabled=true -f "$work/origins.yaml" >/dev/null 2>&1; then
		fail "accepted $1"
	fi
}
for origin in 'http://kit.example' 'https://kit.example/' 'https://*.example' \
	'https://user:secret@kit.example' 'https://kit.example?x=1' 'https://kit.example#x' \
	'https://KIT.example' 'https://-kit.example' 'https://kit..example' 'https://kit.example.' \
	'https://127.1' 'https://0177.0.0.1' 'https://256.1.1.1' 'https://0x7f000001' \
	'https://kit.example:443' 'https://kit.example:0' 'https://kit.example:08443' \
	'https://kit.example:65536' 'https://kit.example:' 'https://catalog.example'; do
	printf 'uiContract:\n  additionalHostOrigins: ["%s"]\n' "$origin" >"$work/origins.yaml"
	reject "$origin"
done
printf 'uiContract:\n  additionalHostOrigins: ["https://kit.example", "https://kit.example"]\n' >"$work/origins.yaml"
reject 'duplicate additional origin'
printf 'uiContract:\n  additionalHostOrigins: [42]\n' >"$work/origins.yaml"
reject 'non-string origin'
printf 'uiContract:\n  additionalHostOrigins:\n' >"$work/origins.yaml"
for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
	printf '    - "https://kit%s.example"\n' "$n" >>"$work/origins.yaml"
done
rendered=$(helm template controller "$chart" --set route.enabled=true --set route.host=catalog.example --set uiContract.enabled=true -f "$work/origins.yaml")
count=$(printf '%s' "$rendered" | yq ea 'select(.kind == "DataProduct") | .spec.ui.contract.hostOrigins | length' -)
[ "$count" = 16 ] || fail 'maximum configured list must include the registry'
printf '    - "https://kit16.example"\n' >>"$work/origins.yaml"
reject 'more than sixteen approved hosts'

for origin in 'https://127.0.0.1:8443' 'https://kit.example:65535'; do
	printf 'uiContract:\n  additionalHostOrigins: ["%s"]\n' "$origin" >"$work/origins.yaml"
	helm template controller "$chart" --set route.enabled=true --set route.host=catalog.example --set uiContract.enabled=true -f "$work/origins.yaml" >/dev/null
done
printf '%s\n' 'UI host origin chart tests passed'
