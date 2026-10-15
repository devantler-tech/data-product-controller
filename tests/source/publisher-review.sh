#!/usr/bin/env bash
# Exercise the publisher-only binary from the built or signature-verified image.
set -euo pipefail

image=${1:?provide the application image}
[[ $# == 1 && "$image" =~ ^[a-zA-Z0-9][a-zA-Z0-9./_:@-]+$ ]] || exit 1
for tool in docker curl openssl timeout; do
	command -v "$tool" >/dev/null || {
		echo "missing prerequisite: $tool" >&2
		exit 1
	}
done
[[ $(docker image inspect "$image" --format '{{.Config.User}}') == '65532:65532' ]]
test_dir=$(mktemp -d)
container_id=''
# cleanup removes only this fixture's container and scratch files while preserving failure status.
cleanup() {
	local result=$?
	trap - EXIT INT TERM
	if [[ -n "$container_id" ]]; then docker rm --force "$container_id" >/dev/null || result=1; fi
	rm -rf "$test_dir"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
	-keyout "$test_dir/tls/key.pem" -out "$test_dir/tls/cert.pem" \
	-subj '/CN=disposable-publisher-review' -addext 'subjectAltName=IP:127.0.0.1' >/dev/null 2>&1
# This key exists only for this disposable acceptance run.
chmod 755 "$test_dir/tls"
chmod 444 "$test_dir/tls/key.pem" "$test_dir/tls/cert.pem"

# request checks the fixture route with bounded, proxy-free curl and the generated test CA for TLS.
request() {
	local path=$1 expected=$2 status
	status=$(curl --silent --show-error --proxy '' --connect-timeout 2 --max-time 5 \
		--cacert "$test_dir/tls/cert.pem" --dump-header "$test_dir/headers" \
		--output "$test_dir/body" --write-out '%{http_code}' "$base$path")
	[[ "$status" == "$expected" ]] || {
		echo "publisher route $path: expected $expected, got $status" >&2
		return 1
	}
}

for mode in tls gateway; do
	for state in default false true; do
		options=(--detach --read-only --cap-drop ALL --security-opt no-new-privileges
			--user 65532:65532 --memory 64m --cpus 1 --pids-limit 64
			--publish 127.0.0.1::8443 --env UI_CONTRACT_ENABLED=invalid
			--env UI_APPEARANCE_ENABLED=true --env REGISTRY_DISCOVERY_ENABLED=invalid)
		arguments=(--listen-address 0.0.0.0:8443)
		if [[ "$state" != default ]]; then options+=(--env "PUBLISHER_PREFLIGHT_ENABLED=$state"); fi
		if [[ "$mode" == tls ]]; then
			options+=(--mount "type=bind,source=$test_dir/tls,target=/tls,readonly")
			arguments+=(--tls-cert /tls/cert.pem --tls-key /tls/key.pem)
		else
			arguments+=(--http-behind-gateway)
		fi
		container_id=$(docker run "${options[@]}" --entrypoint /publisher-review "$image" "${arguments[@]}")
		address=$(docker port "$container_id" 8443/tcp)
		[[ "$address" =~ ^127\.0\.0\.1:[0-9]+$ ]]
		base="http://$address"
		[[ "$mode" != tls ]] || base="https://$address"
		deadline=$((SECONDS + 20))
		until request /healthz 200 >/dev/null 2>&1; do
			if ((SECONDS >= deadline)) || [[ $(docker inspect "$container_id" --format '{{.State.Running}}') != true ]]; then
				echo 'packaged publisher-review did not become healthy' >&2
				exit 1
			fi
			sleep 1
		done
		request /healthz 200
		[[ $(wc -c <"$test_dir/body") -eq 3 && $(cat "$test_dir/body") == ok ]]
		for path in /ui-contract.js /kit.js /kit.css /api/products /missing; do request "$path" 404; done
		if [[ "$state" == true ]]; then
			for path in / /index.html; do
				request "$path?ignored=1" 307
				[[ $(awk 'tolower($1) == "location:" {sub(/\r$/, "", $2); print $2}' "$test_dir/headers") == /publisher-review ]]
			done
			request /publisher-review 200
			grep -F 'preflight-report.js' "$test_dir/body" >/dev/null
			if grep -F 'ui-contract.js' "$test_dir/body" >/dev/null ||
				grep -F 'id="workspace-navigation"' "$test_dir/body" >/dev/null; then
				echo 'publisher-only page exposed compatibility content' >&2
				exit 1
			fi
			grep -Fi 'cache-control: no-store' "$test_dir/headers" >/dev/null
			grep -F "frame-src 'none'" "$test_dir/headers" >/dev/null
			grep -F "connect-src 'none'" "$test_dir/headers" >/dev/null
			for path in /publisher.html /publisher.js /publisher.css /preflight-report.js /metadata.js /descriptor.js; do request "$path" 200; done
		else
			for path in / /index.html /publisher-review /publisher.js /metadata.js /descriptor.js; do request "$path" 404; done
		fi
		docker stop --time 12 "$container_id" >/dev/null
		[[ $(docker inspect "$container_id" --format '{{.State.ExitCode}}') == 0 ]]
		docker rm "$container_id" >/dev/null
		container_id=''
		echo "PASS: packaged publisher-only host ($mode, publisher preflight $state)"
	done
done

result=0
timeout 15 docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
	--network none --user 65532:65532 --memory 64m --pids-limit 64 \
	--env PUBLISHER_PREFLIGHT_ENABLED=invalid --entrypoint /publisher-review "$image" \
	--http-behind-gateway --listen-address 127.0.0.1:8443 >"$test_dir/invalid.log" 2>&1 || result=$?
[[ "$result" == 1 ]]
grep -F 'PUBLISHER_PREFLIGHT_ENABLED must be true or false' "$test_dir/invalid.log" >/dev/null
echo 'PASS: packaged publisher rejects malformed opt-in before serving'
