#!/usr/bin/env bash
set -euo pipefail

# Run the actual release-image command with networking disabled. A missing
# binary must fail the disabled check too, rather than masquerade as flag-off.
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
image=${1:?provide the locally built controller image}
evidence=$(mktemp -d)
trap 'rm -rf "$evidence"' EXIT

run_export() {
	docker run --rm --network none --read-only --cap-drop ALL \
		--security-opt no-new-privileges:true --memory 64m --cpus 1 \
		--entrypoint /dsp-catalog "$@"
}

if run_export "$image" --catalog /missing --bindings /missing \
	>"$evidence/disabled.json" 2>"$evidence/disabled.log"; then
	echo 'DSP exporter unexpectedly enabled by default' >&2
	exit 1
fi
[[ ! -s "$evidence/disabled.json" ]]
grep -F 'DSP catalog export is disabled' "$evidence/disabled.log"

run_export --env DSP_CATALOG_EXPORT_ENABLED=true \
	--mount "type=bind,src=$repo_root/docs/examples/dsp-catalog,dst=/input,readonly" \
	"$image" --catalog /input/catalog.json --bindings /input/bindings.json \
	>"$evidence/catalog.json"
jq -e '
  .["@type"] == "Catalog" and .participantId == "urn:example:provider" and
  (.dataset | length) == 1 and
  .dataset[0]["@id"] == "urn:example:harbour" and
  .dataset[0].hasPolicy[0].assigner == .participantId and
  .dataset[0].distribution[0].accessService["@id"] == .service[0]["@id"] and
  .service[0].endpointURL == "https://connector.example/dsp"
' "$evidence/catalog.json" >/dev/null

if run_export --env DSP_CATALOG_EXPORT_ENABLED=true \
	--mount "type=bind,src=$repo_root/docs/examples/dsp-catalog,dst=/input,readonly" \
	"$image" --catalog /input/catalog.json --bindings /input/catalog.json \
	>"$evidence/invalid.json" 2>"$evidence/invalid.log"; then
	echo 'DSP exporter accepted an invalid binding document' >&2
	exit 1
fi
[[ ! -s "$evidence/invalid.json" ]]
grep -F 'bindings:' "$evidence/invalid.log"
echo 'PASS: packaged DSP exporter is default-off, works offline, and fails without partial output'
