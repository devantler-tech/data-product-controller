#!/usr/bin/env bash
# Shared semantic controls; the caller supplies import_edc and a synthetic work directory.
set -euo pipefail
: "${edc_work:?}" "${edc_fixture:?}"

# reject_import requires an actual importer rejection, never a failed container launch or timeout.
reject_import() {
	local label=$1 result=0
	import_edc "$edc_work/changed.json" "$edc_work/expected.json" \
		>"$edc_work/rejected.json" 2>"$edc_work/rejected.log" || result=$?
	[[ $result == 1 && ! -s "$edc_work/rejected.json" ]] || {
		echo "EDC control did not reject without a receipt: $label (exit $result)" >&2
		return 1
	}
}

# Semantic corruption must differ from the independent oracle even when EDC accepts the document.
for change in \
	'.["@id"]="urn:example:changed-catalog"' \
	'.participantId="urn:example:changed-participant"' \
	'.dataset[0]["@id"]="urn:example:changed-dataset"' \
	'.dataset[0].hasPolicy[0]["@id"]="urn:example:changed-offer"' \
	'.dataset[0].hasPolicy[0].assigner="urn:example:changed-assigner"' \
	'.dataset[0].hasPolicy[0].permission=[]' \
	'.dataset[0].hasPolicy[0].prohibition=[]' \
	'.dataset[0].hasPolicy[0].obligation=[]' \
	'.dataset[0].hasPolicy[0].permission[0].action="http://www.w3.org/ns/odrl/2/read"' \
	'.dataset[0].hasPolicy[0].permission[0].constraint[0].operator="neq"' \
	'.dataset[0].hasPolicy[0].permission[0].constraint[1].rightOperand=true' \
	'.dataset[0].hasPolicy[0].permission[0].constraint[2].rightOperand=7' \
	'.dataset[0].distribution[0].format="https://example.com/transfer/changed"' \
	'.dataset[0].distribution[0].accessService["@id"]="urn:example:changed-service"' \
	'.service[0].endpointURL="https://connector.example/changed"' \
	'.dataset[0].distribution[0].accessService.endpointURL="https://connector.example/changed-inline"' \
	'[., [.dataset[1].distribution[0]["@id"], .dataset[0].distribution[0]["@id"]]] |
      .[0].dataset[0].distribution[0]["@id"]=.[1][0] |
      .[0].dataset[1].distribution[0]["@id"]=.[1][1] | .[0]' \
	'.dataset[0].distribution[0]["@id"]="urn:example:changed-output"' \
	'.dataset[0].distribution |= .[0:1]' \
	'.dataset[0]["dct:title"]="Changed title"' \
	'.dataset[0]["dcat:version"]="v9.0.0"'; do
	jq "$change" "$edc_work/export.json" >"$edc_work/changed.json"
	reject_import semantic-drift
done

# Reject ambiguity before EDC's first-value selection or offer map can conceal it.
for change in \
	'.participantId=[.participantId,"urn:example:unexpected-participant"]' \
	'.service[0].endpointURL=[.service[0].endpointURL,"https://connector.example/unexpected"]' \
	'.dataset[0].hasPolicy |= [(.[0] | .permission=[]), .[0]]' \
	'.["@graph"]=[{"@id":"urn:example:unexpected-node"}]'; do
	jq "$change" "$edc_work/export.json" >"$edc_work/changed.json"
	reject_import ambiguous-profile
done

# Valid JSON must separately reach the real expansion and typed-transform failure paths.
jq '.["@id"]=17' "$edc_work/export.json" >"$edc_work/changed.json"
reject_import expansion-failure
grep -F 'EDC import rejected (expansion):' "$edc_work/rejected.log" >/dev/null
jq 'del(.dataset[0].distribution[0].accessService)' "$edc_work/export.json" >"$edc_work/changed.json"
reject_import transformation-failure
grep -F 'EDC import rejected (transformation):' "$edc_work/rejected.log" >/dev/null

for context in 'file:///etc/passwd' 'jar:file:///fixture/example.jar!/context' 'https://example.invalid/context'; do
	jq --arg context "$context" '.["@context"]=[$context]' "$edc_work/export.json" >"$edc_work/changed.json"
	reject_import foreign-context
done
jq '.dataset[0]["@context"]={"attacker":"https://example.invalid/"}' \
	"$edc_work/export.json" >"$edc_work/changed.json"
reject_import nested-context
printf '%s' '{' >"$edc_work/changed.json"
reject_import malformed-json
cat "$edc_work/export.json" >"$edc_work/changed.json"
printf '%s' 'true' >>"$edc_work/changed.json"
reject_import trailing-json
printf '%s' '{"@id":"urn:example:ignored",' >"$edc_work/changed.json"
tail -c +2 "$edc_work/export.json" >>"$edc_work/changed.json"
reject_import duplicate-key
dd if=/dev/zero of="$edc_work/changed.json" bs=2097153 count=1 2>/dev/null
reject_import input-bound
rm "$edc_work/changed.json"
mkfifo "$edc_work/changed.json"
reject_import special-file
rm "$edc_work/changed.json"
ln -s export.json "$edc_work/changed.json"
reject_import symlink
rm "$edc_work/changed.json"
jq '.overdeep=(reduce range(0;34) as $i ({}; {x:.}))' \
	"$edc_work/export.json" >"$edc_work/changed.json"
reject_import nesting-bound

# A one-output selection is still a loss receipt, not a full compatibility approval.
jq '.dataset |= [.[] | select(.["@id"]=="urn:example:edc-buoy")]' \
	"$edc_work/export.json" >"$edc_work/changed.json"
jq '.datasets |= [.[] | select(.id=="urn:example:edc-buoy")] |
  .expandedDistributions |= [.[] | select(.dataset=="urn:example:edc-buoy")] |
  .expandedDistributionIds=["urn:example:dsp-buoy"]' \
	"$edc_fixture/expected.json" >"$edc_work/one-expected.json"
import_edc "$edc_work/changed.json" "$edc_work/one-expected.json" >"$edc_work/one-receipt.json"
jq -e '.compatibility=="incompatible" and .losses==["typed-distribution-id"] and
  (.datasets|length)==1 and (.expandedDistributionIds|length)==1' "$edc_work/one-receipt.json" >/dev/null
echo 'PASS: real EDC import oracle detects semantic drift, context attacks and input bounds without partial receipts'
