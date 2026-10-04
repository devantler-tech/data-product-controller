#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
source "$repo_root/tests/source/suite.sh"

for selected in all lifecycle sql document graph; do
	SOURCE_TEST_SUITE=$selected source_suite_validate
	observed=()
	for family in lifecycle sql document graph; do
		if source_suite_includes "$family"; then observed+=("$family"); fi
	done
	if [[ "$selected" == all ]]; then
		[[ "${observed[*]}" == 'lifecycle sql document graph' ]]
	else
		[[ "${observed[*]}" == "$selected" ]]
	fi
done
unset SOURCE_TEST_SUITE
source_suite_validate
[[ "$source_test_suite" == all ]]

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
# Invalid configuration must fail before even asking for runtime prerequisites.
# This PATH contains no cluster tools, so a prerequisite failure is distinguishable.
for invalid in '' unknown ALL 'sql document' '../graph'; do
	for entry in "$repo_root/tests/source/run.sh" run.sh; do
		if (cd "$repo_root/tests/source" && SOURCE_TEST_SUITE=$invalid PATH="$scratch" /bin/bash "$entry") >"$scratch/result" 2>&1; then
			echo 'invalid source suite was accepted' >&2
			exit 1
		else
			result=$?
		fi
		[[ "$result" == 2 ]]
		case "$(cat "$scratch/result")" in
		*'unsupported SOURCE_TEST_SUITE'*) ;;
		*)
			cat "$scratch/result" >&2
			exit 1
			;;
		esac
	done
done
echo 'PASS: source suites dispatch once and reject invalid configuration before setup'
