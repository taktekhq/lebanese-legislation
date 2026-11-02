#!/bin/bash
set -euo pipefail
export PGDATA=/opt/lebleg/seed
install -d -o postgres -g postgres -m 0700 "$PGDATA"
gosu postgres initdb -D "$PGDATA"
printf 'local all all trust\nhost all all 127.0.0.1/32 trust\nhost all all ::1/128 trust\nhost all all all scram-sha-256\n' > "$PGDATA/pg_hba.conf"
gosu postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='' -c unix_socket_directories=/tmp -c maintenance_work_mem=1GB -c max_parallel_maintenance_workers=4 -c dynamic_shared_memory_type=mmap -c fsync=off -c full_page_writes=off -c max_wal_size=8GB -c wal_level=minimal -c max_wal_senders=0" -w start
trap 'gosu postgres pg_ctl -D "$PGDATA" -m immediate -w stop || true' EXIT
export PGHOST=/tmp PGUSER=postgres
psql -v ON_ERROR_STOP=1 -c 'CREATE ROLE lebleg_web LOGIN' -c 'CREATE ROLE lebleg_reader LOGIN'
createdb lebleg
createdb lebleg_search
sh /input/data/load.sh 'host=/tmp user=postgres dbname=lebleg'
psql -v ON_ERROR_STOP=1 -d lebleg -f /opt/lebleg/db/official.sql -f /opt/lebleg/db/web.sql
build-search -corpus-dsn 'host=/tmp user=postgres dbname=lebleg' -index-dsn 'host=/tmp user=postgres dbname=lebleg_search' -ids-out /opt/lebleg/vector-ids.csv
for db in lebleg lebleg_search; do
  psql -v ON_ERROR_STOP=1 -d "$db" \
    -c "GRANT CONNECT ON DATABASE $db TO lebleg_web, lebleg_reader" \
    -c 'GRANT USAGE ON SCHEMA public TO lebleg_web, lebleg_reader' \
    -c 'GRANT SELECT ON ALL TABLES IN SCHEMA public TO lebleg_web, lebleg_reader' \
    -c 'GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO lebleg_web, lebleg_reader' \
    -c 'GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO lebleg_web, lebleg_reader' \
    -c ANALYZE
done
psql -c "ALTER ROLE lebleg_web SET default_transaction_read_only=on" -c "ALTER ROLE lebleg_reader SET default_transaction_read_only=on"
# Command-line settings override ALTER SYSTEM. Restart with the small WAL cap
# before checkpointing, so the bulk load's recycled segments can be removed.
gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop
gosu postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='' -c unix_socket_directories=/tmp -c dynamic_shared_memory_type=mmap -c max_wal_size=256MB -c min_wal_size=80MB" -w start
psql -c CHECKPOINT -c CHECKPOINT
gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop
trap - EXIT
du -sh "$PGDATA" "$PGDATA/pg_wal" /scans
