#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Encode filesystem characters before placing the URL inside linter arguments.
# The checker combines this remap with the existing lychee.toml mappings.
encoded="$(jq -rn --arg path "$root/website/dist" '$path | split("/") | map(@uri) | join("/")')"
remap="^https://khoist[.]devantler[.]tech([/?#]|$) file://${encoded}\$1"
if [ "${1:-}" = --arguments ]; then
	[ "$#" -eq 1 ] || exit 2
	printf -- '--remap %q\n' "$remap"
else
	exec lychee --remap "$remap" "$@"
fi
