#!/usr/bin/env bash
set -euo pipefail
fixture=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
deadline=$((SECONDS + 30))
# shellcheck source=tests/dataspace/edc/deadline.sh
source "$fixture/deadline.sh"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/classes" "$scratch/bin" "$scratch/release"
printf '%s' 'compiled fixture class' >"$scratch/classes/EdcImport.class"
bounded bash "$fixture/artifacts.sh" classes "$scratch/classes" >"$scratch/classes.sha256"
[[ -s $scratch/classes.sha256 ]]
printf '{}\n' >"$scratch/catalog.json"
cp "$scratch/catalog.json" "$scratch/bindings.json"
cp "$scratch/catalog.json" "$scratch/receipt.json"
cp "$scratch/catalog.json" "$scratch/release/release.json"
bounded bash "$fixture/receipt.sh" "$scratch" "$fixture" synthetic-toolchain synthetic-image \
	>"$scratch/receipt-positive.json"
jq -e '.version=="edc-import/v1" and (.importer.compiledClassManifestSha256|length)==64' \
	"$scratch/receipt-positive.json" >/dev/null
cat >"$scratch/bin/sha256sum" <<'EOF'
#!/usr/bin/env bash
sleep 3
exit 1
EOF
chmod +x "$scratch/bin/sha256sum"
original_path=$PATH
PATH="$scratch/bin:$PATH"
deadline=$((SECONDS + 1))
result=0
bounded bash "$fixture/artifacts.sh" classes "$scratch/classes" >"$scratch/result" 2>"$scratch/error" || result=$?
[[ $result == 124 && ! -s $scratch/result ]]
deadline=$((SECONDS + 1))
result=0
bounded bash "$fixture/receipt.sh" "$scratch" "$fixture" synthetic-toolchain synthetic-image \
	>"$scratch/result" 2>"$scratch/error" || result=$?
[[ $result == 124 && ! -s $scratch/result ]]
PATH=$original_path
deadline=$SECONDS
result=0
bounded cat "$scratch/receipt-positive.json" >"$scratch/result" || result=$?
[[ $result == 124 && ! -s $scratch/result ]]
echo 'PASS: delayed artifact reads and receipt finalization honor the shared deadline without publishing success'
