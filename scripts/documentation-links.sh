#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Encode filesystem characters before placing the URL inside linter arguments.
# The checker combines this remap with the existing lychee.toml mappings.
encode_path() {
	local input="$1" output='' byte escaped i LC_ALL=C
	for ((i = 0; i < ${#input}; i++)); do
		byte="${input:i:1}"
		case "$byte" in
		[a-zA-Z0-9/.~_-]) output+="$byte" ;;
		*)
			printf -v escaped '%%%02X' "'$byte"
			output+="$escaped"
			;;
		esac
	done
	printf '%s' "$output"
}
encoded="$(encode_path "$root/website/dist")"
remap="^https://khoist[.]devantler[.]tech([/?#]|$) file://${encoded}\$1"
if [ "${1:-}" = --arguments ]; then
	[ "$#" -eq 1 ] || exit 2
	printf -- '--remap %q\n' "$remap"
elif [ "${1:-}" = --prepare-config ]; then
	[ "$#" -eq 1 ] || exit 2
	# MegaLinter captures CLI arguments before PRE_COMMANDS export variables.
	# Its static --config argument points at this generated, ignored copy.
	# The encoded URL has no quotes, so TOML literal strings need no escaping.
	DOCS_REMAP_TOML="'$remap'" \
		awk '{print} /^remap = \[/ {print "  " ENVIRON["DOCS_REMAP_TOML"] ","}' \
		"$root/lychee.toml" >"$root/.lychee-docs.toml"
else
	exec lychee --remap "$remap" "$@"
fi
