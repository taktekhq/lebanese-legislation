#!/bin/bash
set -euo pipefail
if [ "${1:-}" = postgres ]; then
  if [ ! -s "$PGDATA/PG_VERSION" ]; then
    install -d -o postgres -g postgres -m 0700 "$PGDATA"
    cp -a /opt/lebleg/seed/. "$PGDATA/"
    chown -R postgres:postgres "$PGDATA"
    chmod 0700 "$PGDATA"
    gosu postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='' -c unix_socket_directories=/tmp" -w start
    trap 'gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop' EXIT
    psql -h /tmp -U postgres -v ON_ERROR_STOP=1 \
      -v owner="${POSTGRES_USER:-postgres}" -v password="${POSTGRES_PASSWORD:-}" \
      -v reader="${LEBLEG_READER_PASSWORD:-}" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN SUPERUSER', :'owner') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname=:'owner')\gexec
SELECT format('ALTER ROLE %I PASSWORD %L', :'owner', :'password') WHERE :'password'<>''\gexec
SELECT format('ALTER ROLE lebleg_reader PASSWORD %L', :'reader') WHERE :'reader'<>''\gexec
SQL
    gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop
    trap - EXIT
  fi
fi
exec docker-entrypoint.sh "$@"
