#!/usr/bin/env bash
# Sourced by the real-cluster integration test after the generated CRD is installed.
: "${repo_root:?run through tests/source/run.sh}"
: "${test_dir:?integration scratch directory is required}"

yq -o=json '.' "$repo_root/docs/examples/http-source-product.yaml" |
	jq '.metadata.name="ui-admission-fixture" | .spec.ui={url:"https://product.example/ui",title:"Synthetic interface",
		contract:{apiVersion:"data-product-ui/v2",hostOrigins:["https://catalog.example"],capabilities:["status","resize","appearance"]}}' \
		>"$test_dir/ui-admission.json"
kube apply --dry-run=server -f "$test_dir/ui-admission.json" >/dev/null
echo 'PASS: installed admission accepts the bounded v2 appearance manifest'
jq '.spec.ui.contract.apiVersion="data-product-ui/v1"' "$test_dir/ui-admission.json" >"$test_dir/ui-invalid.json"
if kube apply --dry-run=server -f "$test_dir/ui-invalid.json" >"$test_dir/ui-admission.log" 2>&1; then
	echo 'admission unexpectedly accepted a v1 appearance grant' >&2
	exit 1
fi
case "$(cat "$test_dir/ui-admission.log")" in
*'is invalid:'* | *'(Invalid)'*) ;;
*)
	cat "$test_dir/ui-admission.log" >&2
	exit 1
	;;
esac
grep -F 'Appearance requires data-product-ui/v2' "$test_dir/ui-admission.log" >/dev/null
echo 'PASS: installed CEL admission rejects appearance under v1'
jq '.spec.ui.contract.capabilities=["status","resize"]' "$test_dir/ui-invalid.json" >"$test_dir/ui-v1.json"
kube apply --dry-run=server -f "$test_dir/ui-v1.json" >/dev/null
echo 'PASS: installed admission preserves v1 status and resize grants'
