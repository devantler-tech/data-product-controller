#!/usr/bin/env bash
set -euo pipefail
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
# Stop the fixture with the violated cache-publication invariant.
fail() {
	printf 'EDC scanner cache test failed: %s\n' "$1" >&2
	exit 1
}
[[ -f $repo_root/scripts/prepare-edc-scan.sh ]] || fail 'missing fail-closed Maven scanner cache preparation'
hook=$(yq -o=json '.PRE_COMMANDS' "$repo_root/.mega-linter.yml" |
	jq -r '.[] | select(.output_variables == ["MAVEN_HOME"]) | .command')
[[ -n $hook ]] || fail 'scanner cache hook is missing'
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/repo/scripts" "$scratch/repo/tests/dataspace/edc" "$scratch/bin" \
	"$scratch/archive/apache-maven-3.10.0/bin" "$scratch/archive/apache-maven-3.10.0/conf"
cp "$repo_root/scripts/prepare-edc-scan.sh" "$scratch/repo/scripts/"
cp "$repo_root/tests/dataspace/edc/artifacts.sh" "$scratch/repo/tests/dataspace/edc/"
printf '<project/>\n' >"$scratch/repo/tests/dataspace/edc/pom.xml"
printf 'synthetic runtime artifact\n' >"$scratch/fixture.jar"
printf '%s  fixture.jar\n' "$(sha256sum "$scratch/fixture.jar" | cut -d ' ' -f 1)" \
	>"$scratch/repo/tests/dataspace/edc/dependencies.sha256"

# Only external download/checksum/Maven execution are replaced. Real extraction,
# complete library-lock validation, settings generation, output and cleanup run.
cat >"$scratch/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ $SCAN_CASE != download ]] || exit 7
while (($#)); do
 if [[ $1 == --output ]]; then destination=$2; shift 2
 elif [[ $1 == https://* ]]; then source_url=$1; shift
 else shift; fi
done
# The distribution comes from Maven Central's CDN; the pinned checksum verifies it.
[[ ${source_url:-} == https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.10.0/apache-maven-3.10.0-bin.tar.gz ]] || exit 9
cp "$SCAN_FIXTURE/archive.tar.gz" "${destination:?}"
MOCK
cat >"$scratch/bin/sha512sum" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ $SCAN_CASE != checksum ]] || exit 7
IFS= read -r checksum
[[ $checksum == 908b1501bfb420bf7c8affb855534a9c407fd6099367bfb9f2f2dcb8e9799102bffb84518cde74c679bd76870247c6528683abdd620581bffa90f95d92d175aa\ * ]]
MOCK
cat >"$scratch/bin/mktemp" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${2:-} == /tmp/dpc-edc-scan.* ]]; then
 mkdir "$SCAN_FIXTURE/work"
 printf '%s\n' "$SCAN_FIXTURE/work"
else
 /usr/bin/mktemp "$@"
fi
MOCK
cat >"$scratch/archive/apache-maven-3.10.0/bin/mvn" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
while (($#)); do
 case $1 in
 --settings) settings=$2; shift 2 ;;
 --global-settings) global=$2; shift 2 ;;
 -Dmaven.repo.local=*) repository=${1#*=}; shift ;;
 -Duser.home=*) user_home=${1#*=}; shift ;;
 -DoutputDirectory=*) libraries=${1#*=}; shift ;;
 *) shift ;;
 esac
done
[[ ${settings:?} == "${global:?}" && ${user_home:?} == "$SCAN_FIXTURE/work/home" ]]
[[ ${repository:?} == "$SCAN_FIXTURE/work/repository" && -f $settings ]]
grep -F "<localRepository>$repository</localRepository>" "$settings" >/dev/null
mkdir -p "$repository/org/example" "${libraries:?}"
printf '<project/>\n' >"$repository/org/example/fixture.pom"
cp "$SCAN_FIXTURE/fixture.jar" "$libraries/fixture.jar"
[[ $SCAN_CASE != maven ]] || exit 7
if [[ $SCAN_CASE == artifacts ]]; then printf 'unexpected\n' >"$libraries/extra.jar"; fi
MOCK
chmod +x "$scratch/bin/"* "$scratch/archive/apache-maven-3.10.0/bin/mvn"
tar -czf "$scratch/archive.tar.gz" -C "$scratch/archive" apache-maven-3.10.0
# Execute the actual configured hook, requiring its published Maven path on success.
run_hook() (
	cd "$scratch/repo"
	bash -c 'eval "$1" || exit $?; printf "%s\n" "${MAVEN_HOME:?}"' cache-hook "$hook"
)
for failure in download checksum maven artifacts; do
	result=0
	SCAN_CASE=$failure SCAN_FIXTURE=$scratch PATH="$scratch/bin:$PATH" \
		run_hook >"$scratch/output" 2>"$scratch/error" || result=$?
	[[ $result != 0 && ! -s $scratch/output && ! -e $scratch/work ]] ||
		fail "$failure must reject partial preparation without advertising or retaining its cache"
done
SCAN_CASE=success SCAN_FIXTURE=$scratch PATH="$scratch/bin:$PATH" \
	run_hook >"$scratch/output" 2>"$scratch/error" || {
	cat "$scratch/error" >&2
	fail 'complete preparation must succeed'
}
[[ $(cat "$scratch/output") == "$scratch/work/apache-maven-3.10.0" &&
-f $scratch/work/repository/org/example/fixture.pom ]] || fail 'completed cache must be available to the scanner'
printf '%s\n' 'PASS: scanner cache publishes only after complete verified preparation; failed download/checksum/resolve/artifact validation never publishes a partial cache'
