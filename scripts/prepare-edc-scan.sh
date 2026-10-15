#!/usr/bin/env bash
set -euo pipefail
# Prepare metadata once before parallel Java scans. Never advertise a partial cache.
repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
[[ $# == 0 ]] || {
	echo 'EDC scanner cache takes no arguments' >&2
	exit 2
}
if [[ -e $HOME/.m2/settings.xml || -L $HOME/.m2/settings.xml ]]; then
	echo 'EDC scanner cache refuses ambient Maven user settings' >&2
	exit 1
fi
scan_deadline=$((SECONDS + 300))
scan_work=$(mktemp -d /tmp/dpc-edc-scan.XXXXXX)
scan_complete=false
# Preserve the failure status and remove the cache unless every preparation step completed.
cleanup() {
	local result=$?
	trap - EXIT
	if [[ $scan_complete != true ]]; then rm -rf "$scan_work"; fi
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Charge each download, resolver and verification command to the same preparation deadline.
scan_run() {
	local remaining=$((scan_deadline - SECONDS))
	((remaining > 0)) || return 124
	# GNU and Alpine/BusyBox timeout share these short options.
	timeout -s TERM -k 5 "$remaining" "$@"
}
scan_run curl --fail --location --proto '=https' --proto-redir '=https' --proxy '' \
	--max-time 90 --max-filesize 16777216 --output "$scan_work/maven.tar.gz" \
	https://archive.apache.org/dist/maven/maven-3/3.10.0/binaries/apache-maven-3.10.0-bin.tar.gz >&2
printf '%s  %s\n' \
	908b1501bfb420bf7c8affb855534a9c407fd6099367bfb9f2f2dcb8e9799102bffb84518cde74c679bd76870247c6528683abdd620581bffa90f95d92d175aa \
	"$scan_work/maven.tar.gz" | scan_run sha512sum -c - >&2
scan_run tar -xzf "$scan_work/maven.tar.gz" -C "$scan_work"
scan_maven="$scan_work/apache-maven-3.10.0"
mkdir "$scan_work/home" "$scan_work/repository" "$scan_work/libraries"
# Trivy reads MAVEN_HOME/conf/settings.xml. Do not change HOME or read host credentials.
cat >"$scan_maven/conf/settings.xml" <<EOF
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0">
  <localRepository>$scan_work/repository</localRepository>
  <mirrors><mirror><id>public-central</id><mirrorOf>*</mirrorOf>
    <url>https://repo.maven.apache.org/maven2</url>
  </mirror></mirrors>
</settings>
EOF
scan_run env -u MAVEN_ARGS -u MAVEN_HOME -u M2_HOME -u JAVA_TOOL_OPTIONS -u JDK_JAVA_OPTIONS \
	MAVEN_SKIP_RC=true MAVEN_OPTS='-Xmx384m -XX:ActiveProcessorCount=1 -XX:-UsePerfData' \
	"$scan_maven/bin/mvn" -B -ntp -C -Duser.home="$scan_work/home" \
	-Dmaven.repo.local="$scan_work/repository" \
	--settings "$scan_maven/conf/settings.xml" --global-settings "$scan_maven/conf/settings.xml" \
	-f "$repo_root/tests/dataspace/edc/pom.xml" \
	org.apache.maven.plugins:maven-dependency-plugin:3.9.0:copy-dependencies \
	-DincludeScope=runtime -DoutputDirectory="$scan_work/libraries" >&2
scan_run bash "$repo_root/tests/dataspace/edc/artifacts.sh" libraries \
	"$scan_work/libraries" "$repo_root/tests/dataspace/edc/dependencies.sha256" >&2
((scan_deadline > SECONDS))
scan_complete=true
printf '%s\n' "$scan_maven"
