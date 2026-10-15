#!/usr/bin/env bash
set -euo pipefail
fixture=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
# shellcheck source=tests/dataspace/edc/artifacts.sh
source "$fixture/artifacts.sh"
mkdir "$scratch/libraries" "$scratch/classes" "$scratch/bin"
printf '%s' 'locked synthetic artifact' >"$scratch/libraries/example.jar"
(cd "$scratch/libraries" && sha256sum example.jar) >"$scratch/lock"
edc_verify_libraries "$scratch/libraries" "$scratch/lock"

# rejected requires a failure and no plausible success manifest from an incomplete read.
rejected() {
	if "$@" >"$scratch/result" 2>"$scratch/error"; then
		echo 'invalid artifact set was accepted' >&2
		exit 1
	fi
	[[ ! -s $scratch/result ]]
}
printf '%s' 'unexpected' >"$scratch/libraries/extra.jar"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
rm "$scratch/libraries/extra.jar"
printf '%s' 'modified' >>"$scratch/libraries/example.jar"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
printf '%s' 'locked synthetic artifact' >"$scratch/libraries/example.jar"
mv "$scratch/libraries/example.jar" "$scratch/example.jar"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
ln -s ../example.jar "$scratch/libraries/example.jar"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
rm "$scratch/libraries/example.jar"
mkfifo "$scratch/libraries/example.jar"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
rm "$scratch/libraries/example.jar"
mv "$scratch/example.jar" "$scratch/libraries/example.jar"
mkdir "$scratch/libraries/unexpected-directory"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
rmdir "$scratch/libraries/unexpected-directory"

# The checksum checker may emit an apparently complete answer before reporting a failed read.
cat >"$scratch/bin/sha256sum" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' 'example.jar: OK'
exit 1
EOF
chmod +x "$scratch/bin/sha256sum"
original_path=$PATH
PATH="$scratch/bin:$PATH"
rejected edc_verify_libraries "$scratch/libraries" "$scratch/lock"
PATH=$original_path
edc_verify_libraries "$scratch/libraries" "$scratch/lock"

printf '%s' 'compiled fixture class' >"$scratch/classes/EdcImport.class"
edc_class_manifest "$scratch/classes" >"$scratch/manifest"
[[ -s $scratch/manifest ]]
(cd "$scratch/classes" && sha256sum --check "$scratch/manifest") >/dev/null
printf '%s' 'nested fixture class' >"$scratch/classes/EdcImport\$Dataset.class"
edc_class_manifest "$scratch/classes" >"$scratch/manifest"
[[ $(wc -l <"$scratch/manifest") -eq 2 ]]
printf '%s' 'unrelated class' >"$scratch/classes/Other.class"
rejected edc_class_manifest "$scratch/classes"
rm "$scratch/classes/Other.class"
mv "$scratch/classes/EdcImport.class" "$scratch/EdcImport.class"
rejected edc_class_manifest "$scratch/classes"
ln -s ../EdcImport.class "$scratch/classes/EdcImport.class"
rejected edc_class_manifest "$scratch/classes"
rm "$scratch/classes/EdcImport.class"
mv "$scratch/EdcImport.class" "$scratch/classes/EdcImport.class"
PATH="$scratch/bin:$PATH"
rejected edc_class_manifest "$scratch/classes"
PATH=$original_path
edc_class_manifest "$scratch/classes" >/dev/null
echo 'EDC artifact checks reject missing, changed, extra, special and incomplete artifact evidence'
