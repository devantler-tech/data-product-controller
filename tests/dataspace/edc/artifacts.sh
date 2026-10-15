#!/usr/bin/env bash
# Sourced by the bounded toolchain runner; no dependency code executes in these checks.

# edc_verify_libraries requires the exact regular-file set and bytes in the authored runtime lock.
edc_verify_libraries() (
	set -euo pipefail
	shopt -s nullglob dotglob
	local directory=$1 lock=$2 artifact_scratch path name
	local -a files
	[[ -d $directory && ! -L $directory && -f $lock && ! -L $lock && $lock == /* ]] || exit 1
	files=("$directory"/*)
	((${#files[@]} > 0 && ${#files[@]} <= 128)) || exit 1
	artifact_scratch=$(mktemp -d) || exit 1
	trap 'rm -rf "$artifact_scratch"' EXIT
	for path in "${files[@]}"; do
		name=${path##*/}
		[[ $name =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*\.jar$ && -f $path && ! -L $path ]] || exit 1
		printf '%s\n' "$name" >>"$artifact_scratch/actual" || exit 1
	done
	LC_ALL=C sort "$artifact_scratch/actual" >"$artifact_scratch/sorted" || exit 1
	awk '{print $2}' "$lock" | LC_ALL=C sort >"$artifact_scratch/expected" || exit 1
	cmp "$artifact_scratch/sorted" "$artifact_scratch/expected" >/dev/null || exit 1
	(cd "$directory" && sha256sum --strict --check "$lock") >"$artifact_scratch/checked" || exit 1
)

# edc_class_manifest emits a complete canonical manifest only after every expected class was read.
edc_class_manifest() (
	set -euo pipefail
	shopt -s nullglob dotglob
	local directory=$1 artifact_scratch path name
	local -a files
	[[ -d $directory && ! -L $directory && -f $directory/EdcImport.class ]] || exit 1
	files=("$directory"/*)
	((${#files[@]} > 0 && ${#files[@]} <= 128)) || exit 1
	artifact_scratch=$(mktemp -d) || exit 1
	trap 'rm -rf "$artifact_scratch"' EXIT
	for path in "${files[@]}"; do
		name=${path##*/}
		[[ $name =~ ^EdcImport[A-Za-z0-9_\$]*\.class$ && -f $path && ! -L $path ]] || exit 1
		(cd "$directory" && sha256sum "$name") >>"$artifact_scratch/hashes" || exit 1
	done
	LC_ALL=C sort -k 2 "$artifact_scratch/hashes" >"$artifact_scratch/manifest" || exit 1
	cat "$artifact_scratch/manifest"
)
