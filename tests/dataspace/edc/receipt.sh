#!/usr/bin/env bash
set -euo pipefail
# Called under the runner's remaining allowance; emit only after complete hashing and encoding.
edc_work=${1:?}
edc_fixture=${2:?}
toolchain=${3:?}
toolchain_id=${4:?}
snapshot_hash=$(sha256sum "$edc_work/catalog.json" | cut -d ' ' -f 1)
bindings_hash=$(sha256sum "$edc_work/bindings.json" | cut -d ' ' -f 1)
source_hash=$(sha256sum "$edc_fixture/EdcImport.java" | cut -d ' ' -f 1)
pom_hash=$(sha256sum "$edc_fixture/pom.xml" | cut -d ' ' -f 1)
settings_hash=$(sha256sum "$edc_fixture/settings.xml" | cut -d ' ' -f 1)
lock_hash=$(sha256sum "$edc_fixture/dependencies.sha256" | cut -d ' ' -f 1)
classes_hash=$(sha256sum "$edc_work/classes.sha256" | cut -d ' ' -f 1)
jq -n --slurpfile observed "$edc_work/receipt.json" --slurpfile release "$edc_work/release/release.json" \
	--arg snapshot "$snapshot_hash" --arg bindings "$bindings_hash" \
	--arg toolchain "$toolchain" --arg image "$toolchain_id" --arg source "$source_hash" \
	--arg pom "$pom_hash" --arg settings "$settings_hash" --arg lock "$lock_hash" --arg classes "$classes_hash" '
  {version:"edc-import/v1",result:"observed-incompatibility",
   suppliedSnapshotSha256:$snapshot,publicBindingsSha256:$bindings,
   releasedExporter:$release[0],
   importer:{version:"0.18.1",source:"3a22ff4aa90213a564309b5a05851e6d7f27f9ff",
     toolchainRef:$toolchain,toolchainImageId:$image,javaSourceSha256:$source,
     pomSha256:$pom,settingsSha256:$settings,dependencyLockSha256:$lock,
     compiledClassManifestSha256:$classes},
   observed:$observed[0]}' >"$edc_work/receipt-encoded.json"
[[ $(wc -c <"$edc_work/receipt-encoded.json") -le 65536 ]]
cat "$edc_work/receipt-encoded.json"
