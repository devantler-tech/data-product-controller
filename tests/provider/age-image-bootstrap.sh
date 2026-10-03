#!/usr/bin/env bash
set -euo pipefail

[[ $(id -u) == 26 ]]
[[ $(postgres --version) == 'postgres (PostgreSQL) 17.11'* ]]
for executable in initdb postgres pg_ctl pg_controldata pg_basebackup; do
	command -v "$executable" >/dev/null
done
for executable in gcc make; do
	if command -v "$executable" >/dev/null; then
		echo 'build-only executable leaked into the AGE runtime' >&2
		exit 1
	fi
done
export PGDATA=/tmp/age-acceptance
mkdir "$PGDATA"
# cleanup stops the disposable PostgreSQL server on every exit.
cleanup() { pg_ctl -D "$PGDATA" -m immediate -w stop >/dev/null 2>&1 || true; }
trap cleanup EXIT
initdb -D "$PGDATA" --no-sync --encoding=UTF8 --locale=C.UTF-8 --auth-local=trust --auth-host=scram-sha-256 >/dev/null
if ! pg_ctl -D "$PGDATA" -l /tmp/postgres.log -w -t 30 start \
	-o '-c listen_addresses=127.0.0.1 -c unix_socket_directories=/tmp -c shared_preload_libraries=age -c max_connections=20 -c shared_buffers=32MB' >/dev/null; then
	cat /tmp/postgres.log >&2
	exit 1
fi
# admin runs bootstrap and server checks over the local superuser socket.
admin() { psql -X -q -v ON_ERROR_STOP=1 -h /tmp -U postgres -d postgres "$@"; }
[[ $(admin -At -c 'SHOW shared_preload_libraries') == age ]]
admin -f /acceptance/age-image.sql >/dev/null
[[ $(admin -At -f /acceptance/age-runtime.sql) == t ]]
export PGPASSWORD=synthetic-age-reader
# reader runs authenticated TCP queries through the restricted AGE reader role.
reader() { psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -U age_reader -d postgres "$@"; }
[[ $(reader -At -f /acceptance/age-image-reader.sql) == 1 ]]
for sql in \
	'CREATE ROLE forbidden SUPERUSER' \
	'SET ROLE age_writer' \
	'CREATE SCHEMA forbidden' \
	'INSERT INTO lineage.product DEFAULT VALUES'; do
	if reader --set=VERBOSITY=sqlstate -c "$sql" >/dev/null 2>/tmp/denial.log; then
		echo 'AGE reader unexpectedly changed data or privileges' >&2
		exit 1
	fi
	grep -Eq '^ERROR:[[:space:]]+42501([[:space:]]|$)' /tmp/denial.log || {
		echo 'AGE denial lacked SQL authorization evidence' >&2
		exit 1
	}
done
for mutation in write update delete; do
	if reader --set=VERBOSITY=sqlstate -f "/acceptance/age-image-$mutation.sql" >/dev/null 2>/tmp/denial.log; then
		echo 'AGE reader unexpectedly changed the graph' >&2
		exit 1
	fi
	grep -Eq 'ERROR:[[:space:]]+42501([[:space:]]|$)' /tmp/denial.log || {
		echo 'Cypher denial lacked SQL authorization evidence' >&2
		exit 1
	}
done
[[ $(reader -At -f /acceptance/age-image-reader.sql) == 1 ]]
echo 'PASS: preloaded AGE returns persistent two-hop lineage through an authenticated read-only role'

# A connection-local LOAD still works without preloading. The independent
# owner's runtime assertion must distinguish that state from the supported profile.
pg_ctl -D "$PGDATA" -m fast -w stop >/dev/null
pg_ctl -D "$PGDATA" -l /tmp/postgres.log -w -t 30 start \
	-o '-c listen_addresses=127.0.0.1 -c unix_socket_directories=/tmp -c shared_preload_libraries= -c max_connections=20 -c shared_buffers=32MB' >/dev/null
[[ $(admin -At -f /acceptance/age-runtime.sql) == f ]]
[[ $(reader -At -f /acceptance/age-image-reader.sql) == 1 ]]
echo 'PASS: the owner detects missing server preload even when connection-local Cypher reads succeed'
pg_ctl -D "$PGDATA" -m fast -w stop >/dev/null
pg_ctl -D "$PGDATA" -l /tmp/postgres.log -w -t 30 start \
	-o '-c listen_addresses=127.0.0.1 -c unix_socket_directories=/tmp -c shared_preload_libraries=age -c max_connections=20 -c shared_buffers=32MB' >/dev/null
[[ $(admin -At -f /acceptance/age-runtime.sql) == t ]]
[[ $(reader -At -f /acceptance/age-image-reader.sql) == 1 ]]
echo 'PASS: restoring server preload preserves the persisted graph and reader access'
