#!/bin/sh
set -eu

# The Dockerfile build stage must run a Go toolchain that satisfies go.mod's `go`
# directive. Official golang images default to GOTOOLCHAIN=local, so a builder older
# than the directive refuses the module outright:
#
#   go: go.mod requires go >= 1.26.6 (running go 1.26.0; GOTOOLCHAIN=local)
#
# The same pin also decides which stdlib the shipped binary is built against, so a
# builder behind the directive silently keeps stdlib advisories the directive was
# raised to clear.

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
gomod="$repo_root/go.mod"
dockerfile="$repo_root/Dockerfile"

fail() {
	printf '%s\n' "toolchain test failed: $1" >&2
	exit 1
}

[ -f "$gomod" ] || fail "go.mod not found at $gomod"
[ -f "$dockerfile" ] || fail "Dockerfile not found at $dockerfile"

go_directive=$(awk '$1 == "go" { print $2; exit }' "$gomod")
[ -n "$go_directive" ] || fail "no 'go' directive found in go.mod"

builder=$(awk '
  tolower($1) == "from" && $2 ~ /^golang:/ { print $2; exit }
' "$dockerfile")
[ -n "$builder" ] || fail "no 'FROM golang:...' build stage found in Dockerfile"

# golang:<version>[-variant] -> <version>
builder_version=${builder#golang:}
builder_version=${builder_version%%-*}
[ -n "$builder_version" ] || fail "could not parse a version from Dockerfile image '$builder'"

if [ "$builder_version" != "$go_directive" ]; then
	fail "Dockerfile builder '$builder' does not match go.mod 'go $go_directive'.
  The official golang image defaults to GOTOOLCHAIN=local, so a builder behind the
  go directive fails the build, and one ahead of it ships a stdlib the directive
  does not describe. Pin the build stage to golang:${go_directive}-alpine."
fi

printf '%s\n' "toolchain test passed: go.mod 'go $go_directive' matches Dockerfile '$builder'"
