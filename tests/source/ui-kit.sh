#!/usr/bin/env bash
# Run the real image entrypoint with disposable loopback-only listeners.
set -euo pipefail

image=${1:?provide the built or verified released application image}
[[ $# == 1 && "$image" =~ ^[a-zA-Z0-9][a-zA-Z0-9./_:@-]+$ ]] || {
	echo 'provide exactly one application image reference' >&2
	exit 1
}
for command in docker curl openssl; do
	command -v "$command" >/dev/null || {
		echo "missing prerequisite: $command" >&2
		exit 1
	}
done
[[ $(docker image inspect "$image" --format '{{.Config.User}}') == '65532:65532' ]] || {
	echo 'portable host image must retain its nonroot user' >&2
	exit 1
}

test_dir=$(mktemp -d)
container_id=''
cleanup() {
	local result=$?
	trap - EXIT INT TERM
	if [[ -n "$container_id" ]]; then
		docker rm --force "$container_id" >/dev/null || result=1
	fi
	rm -rf "$test_dir"
	exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
	-keyout "$test_dir/tls/key.pem" -out "$test_dir/tls/cert.pem" \
	-subj '/CN=disposable-ui-kit' -addext 'subjectAltName=IP:127.0.0.1' >/dev/null 2>&1
# Only disposable test keys are shared with the image's nonroot UID.
chmod 755 "$test_dir/tls"
chmod 444 "$test_dir/tls/key.pem" "$test_dir/tls/cert.pem"

request() {
	local path=$1 expected=$2 status
	status=$(curl --silent --show-error --proxy '' --connect-timeout 2 --max-time 5 \
		--cacert "$test_dir/tls/cert.pem" --dump-header "$test_dir/headers" \
		--output "$test_dir/body" --write-out '%{http_code}' "$base$path")
	[[ "$status" == "$expected" ]] || {
		echo "portable host status mismatch: expected $expected, got $status" >&2
		return 1
	}
}

start_host() {
	local mode=$1 state=$2 appearance=${3:-false} address deadline
	local -a options=(--detach --read-only --cap-drop ALL --security-opt no-new-privileges
		--user 65532:65532 --memory 64m --cpus 1 --pids-limit 64
		--publish 127.0.0.1::8443 --env "UI_APPEARANCE_ENABLED=$appearance")
	local -a arguments=(--listen-address 0.0.0.0:8443)
	if [[ "$state" != default ]]; then
		options+=(--env "UI_CONTRACT_ENABLED=$state")
	fi
	if [[ "$mode" == tls ]]; then
		options+=(--mount "type=bind,source=$test_dir/tls,target=/tls,readonly")
		arguments+=(--tls-cert /tls/cert.pem --tls-key /tls/key.pem)
	else
		arguments+=(--http-behind-gateway)
	fi
	container_id=$(docker run "${options[@]}" --entrypoint /ui-kit "$image" "${arguments[@]}")
	address=$(docker port "$container_id" 8443/tcp)
	[[ "$address" =~ ^127\.0\.0\.1:[0-9]+$ ]] || {
		echo 'portable host smoke must publish only one loopback endpoint' >&2
		return 1
	}
	base="http://$address"
	[[ "$mode" != tls ]] || base="https://$address"
	deadline=$((SECONDS + 20))
	until request /healthz 200 >/dev/null 2>&1; do
		if ((SECONDS >= deadline)) || [[ $(docker inspect "$container_id" --format '{{.State.Running}}') != true ]]; then
			echo 'portable host did not become healthy' >&2
			return 1
		fi
		sleep 1
	done
}

for mode in tls gateway; do
	for state in default false true; do
		start_host "$mode" "$state"
		request /healthz 200
		[[ $(wc -c <"$test_dir/body") -eq 3 && $(cat "$test_dir/body") == ok ]]
		grep -Fi 'cache-control: no-store' "$test_dir/headers" >/dev/null
		if [[ "$state" == true ]]; then
			request / 200
			grep -F 'ui-contract.js' "$test_dir/body" >/dev/null
			grep -F "connect-src 'none'" "$test_dir/headers" >/dev/null
			request /ui-contract.js 200
			grep -F 'DataProductUI' "$test_dir/body" >/dev/null
			request /kit.js 200
			request /kit.css 200
		else
			request / 404
			request /ui-contract.js 404
		fi
		docker rm --force "$container_id" >/dev/null
		container_id=''
		echo "PASS: packaged UI host ($mode, UI contract $state)"
	done
done

start_host tls true true
request / 200
grep -F '<body data-appearance-enabled="true">' "$test_dir/body" >/dev/null
request /ui-contract.js 200
grep -F 'DataProductUI' "$test_dir/body" >/dev/null
echo 'PASS: packaged UI host preserves explicit v2 appearance grants'
