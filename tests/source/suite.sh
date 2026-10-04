#!/usr/bin/env bash
# Pure selection helpers; sourcing this file never creates a fixture.
source_suite_validate() {
	source_test_suite=${SOURCE_TEST_SUITE-all}
	case "$source_test_suite" in
	all | lifecycle | sql | document | graph) ;;
	*)
		printf '%s\n' 'unsupported SOURCE_TEST_SUITE; use all, lifecycle, sql, document or graph' >&2
		return 2
		;;
	esac
}

source_suite_includes() {
	[[ "$source_test_suite" == all || "$source_test_suite" == "$1" ]]
}
