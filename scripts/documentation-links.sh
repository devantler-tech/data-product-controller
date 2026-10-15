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
elif [ "${1:-}" = --prepare-config ]; then
	[ "$#" -eq 1 ] || exit 2
	# MegaLinter captures CLI arguments before PRE_COMMANDS export variables.
	# Its static --config argument points at this generated, ignored copy.
	DOCS_REMAP_TOML="$(jq -rn --arg remap "$remap" '$remap | @json')" \
		awk '{print} /^remap = \[/ {print "  " ENVIRON["DOCS_REMAP_TOML"] ","}' \
		"$root/lychee.toml" >"$root/.lychee-docs.toml"
else
	exec lychee --remap "$remap" "$@"
fi
