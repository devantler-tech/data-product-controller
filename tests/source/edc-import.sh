#!/usr/bin/env bash
set -euo pipefail
# The caller captures the actual enabled controller catalog inside its disposable cluster.
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
snapshot=${1:?provide the actual controller catalog snapshot}
[[ -f $snapshot && ! -L $snapshot && $(wc -c <"$snapshot") -le 2097152 ]]
edc_fixture="$repo_root/tests/dataspace/edc"
budget=${2:-420}
[[ $budget =~ ^[1-9][0-9]*$ ]] || exit 2
((budget <= 420)) || budget=420
deadline=$((SECONDS + budget))
build_uid=$(id -u)
build_gid=$(id -g)
[[ $build_uid =~ ^[1-9][0-9]*$ && $build_gid =~ ^[0-9]+$ ]] || exit 2
edc_work=$(mktemp -d)
container="dpc-edc-${GITHUB_RUN_ID:-local}-$RANDOM"
toolchain='maven:3.10.0-eclipse-temurin-17@sha256:9323bdf61bb503a869956db0a3bec0d82bf649e3f99e6a585929966f8d42e98d'

# cleanup always removes this fixture container and public scratch files, preserving failure.
cleanup() {
	local result=$? running
	trap - EXIT INT TERM
	if running=$(timeout --signal=TERM --kill-after=5 15 docker ps --all \
		--filter "name=^/$container$" --format '{{.ID}}'); then
		if [[ -n $running ]]; then
			timeout --signal=TERM --kill-after=5 15 docker rm --force "$container" >/dev/null || result=1
		fi
	else
		result=1
	fi
	rm -rf "$edc_work"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# bounded shares one absolute allowance across resolution, compilation, export and all controls.
bounded() {
	local remaining=$((deadline - SECONDS))
	((remaining > 0)) || return 124
	timeout --signal=TERM --kill-after=5 "${remaining}s" "$@"
}

bounded bash "$edc_fixture/artifacts.test.sh"
bounded bash "$repo_root/scripts/verify-release-artifacts.sh" --tag v4.0.0 \
	--source-sha 4fbc7d215b3ce30091e74e9909a3ad822e878a05 \
	--image-digest sha256:cc5bc6b165426daec27e70bbeb90f41a2e30699f99455f8b0733c247fbe57862 \
	--chart-digest sha256:338521dc3e85774d6fff817a1af073ed1a462ee67e61459bf37f15e9a370ec89 \
	--publisher-sha ea4df9004d4ab43b6c9af5b0c5f55712f7211328 \
	--platform linux/amd64 --output-dir "$edc_work/release" --timeout 120
released="ghcr.io/devantler-tech/data-product-controller@$(jq -er '.imageDigest' "$edc_work/release/release.json")"
cp "$snapshot" "$edc_work/catalog.json"
cp "$edc_fixture/bindings.json" "$edc_work/bindings.json"
cp "$edc_fixture/expected.json" "$edc_work/expected.json"
mkdir "$edc_work/libraries" "$edc_work/classes"
chmod 755 "$edc_work" "$edc_work/libraries" "$edc_work/classes"
chmod 644 "$edc_work/catalog.json" "$edc_work/bindings.json" "$edc_work/expected.json"

bounded docker pull --platform linux/amd64 "$toolchain"
[[ $(bounded docker image inspect "$toolchain" --format '{{.Os}}/{{.Architecture}}') == linux/amd64 ]]
toolchain_id=$(bounded docker image inspect "$toolchain" --format '{{.Id}}')
[[ $toolchain_id =~ ^sha256:[a-f0-9]{64}$ ]]

# Only this nonroot, resource-bounded phase has network access. No host Maven settings are mounted.
bounded docker run --platform linux/amd64 --pull never --rm --name "$container" --read-only \
	--cap-drop ALL --security-opt no-new-privileges:true --user "$build_uid:$build_gid" \
	--memory 768m --cpus 1 --pids-limit 128 --tmpfs /tmp:rw,noexec,nosuid,size=256m \
	--env MAVEN_CONFIG=/tmp/maven \
	--env 'MAVEN_OPTS=-Xmx384m -XX:ActiveProcessorCount=1 -XX:-UsePerfData' \
	--mount "type=bind,src=$edc_fixture/pom.xml,dst=/fixture/pom.xml,readonly" \
	--mount "type=bind,src=$edc_fixture/settings.xml,dst=/fixture/settings.xml,readonly" \
	--mount "type=bind,src=$edc_work/libraries,dst=/libraries" --entrypoint mvn "$toolchain" \
	-B -ntp -C -Duser.home=/tmp/maven -Dmaven.repo.local=/tmp/maven/repository \
	--settings /fixture/settings.xml -f /fixture/pom.xml \
	org.apache.maven.plugins:maven-dependency-plugin:3.9.0:copy-dependencies \
	-DincludeScope=runtime -DoutputDirectory=/libraries
# shellcheck source=tests/dataspace/edc/artifacts.sh
source "$edc_fixture/artifacts.sh"
edc_verify_libraries "$edc_work/libraries" "$edc_fixture/dependencies.sha256"

# Compilation is offline, with locked dependencies read-only and only its output directory writable.
bounded docker run --platform linux/amd64 --pull never --rm --name "$container" --network none \
	--read-only --cap-drop ALL --security-opt no-new-privileges:true --user "$build_uid:$build_gid" \
	--memory 512m --cpus 1 --pids-limit 64 --tmpfs /tmp:rw,noexec,nosuid,size=16m \
	--mount "type=bind,src=$edc_fixture/EdcImport.java,dst=/fixture/EdcImport.java,readonly" \
	--mount "type=bind,src=$edc_work/libraries,dst=/libraries,readonly" \
	--mount "type=bind,src=$edc_work/classes,dst=/classes" --entrypoint javac "$toolchain" \
	--release 17 -Xlint:all -Werror -J-Xmx256m -J-XX:ActiveProcessorCount=1 -J-XX:-UsePerfData \
	-cp '/libraries/*' -d /classes /fixture/EdcImport.java
edc_class_manifest "$edc_work/classes" >"$edc_work/classes.sha256"

bounded docker run --platform linux/amd64 --rm --name "$container" --network none --read-only \
	--cap-drop ALL --security-opt no-new-privileges:true --user 65532:65532 --memory 64m --cpus 1 \
	--pids-limit 64 --env DSP_CATALOG_EXPORT_ENABLED=true --entrypoint /dsp-catalog \
	--mount "type=bind,src=$edc_work,dst=/input,readonly" "$released" \
	--catalog /input/catalog.json --bindings /input/bindings.json >"$edc_work/export.json"
chmod 644 "$edc_work/export.json"
jq -e '.dataset[0]["@id"]=="urn:example:edc-harbour"' "$edc_work/export.json" >/dev/null

# import_edc executes the real consumer offline, with all mounted inputs and compiled code read-only.
import_edc() {
	bounded docker run --platform linux/amd64 --pull never --rm --name "$container" --network none \
		--read-only --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges:true \
		--memory 512m --cpus 1 --pids-limit 64 --tmpfs /tmp:rw,noexec,nosuid,size=16m \
		--mount "type=bind,src=$edc_work,dst=/input,readonly" \
		--mount "type=bind,src=$edc_work/libraries,dst=/libraries,readonly" \
		--mount "type=bind,src=$edc_work/classes,dst=/classes,readonly" \
		--mount "type=bind,src=$repo_root/internal/dataspace/testdata/dsp-2025-1-err2,dst=/contexts,readonly" \
		--entrypoint java "$toolchain" -Xmx256m -XX:ActiveProcessorCount=1 -XX:-UsePerfData \
		-cp '/classes:/libraries/*' EdcImport "/input/${1##*/}" /contexts "/input/${2##*/}"
}
import_edc "$edc_work/export.json" "$edc_work/expected.json" >"$edc_work/receipt.json"
[[ $(jq -er '.inputSha256' "$edc_work/receipt.json") == $(sha256sum "$edc_work/export.json" | cut -d ' ' -f 1) ]]
jq -e '.compatibility=="incompatible" and .losses==["typed-distribution-id"] and
  (.datasets|length)==2 and (.expandedDistributionIds|length)==3' "$edc_work/receipt.json" >/dev/null
# shellcheck source=tests/dataspace/edc/controls.sh
source "$edc_fixture/controls.sh"

# Recheck complete artifact evidence before emitting the one bounded public receipt.
edc_verify_libraries "$edc_work/libraries" "$edc_fixture/dependencies.sha256"
edc_class_manifest "$edc_work/classes" >"$edc_work/classes-after.sha256"
cmp "$edc_work/classes.sha256" "$edc_work/classes-after.sha256"
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
   observed:$observed[0]}' >"$edc_work/final-receipt.json"
[[ $(wc -c <"$edc_work/final-receipt.json") -le 65536 ]]
cat "$edc_work/final-receipt.json"
echo 'PASS: unchanged signed-release export reaches real pinned offline EDC import; identity loss and artifact/input hashes are retained above'
