# syntax=docker/dockerfile:1
# ghcr.io/taktekhq/lebanese-legislation-db-prod: the -db image plus the
# read-optimized tables of db/prod.sql (law_card, article_card, and
# possibly_affected_t, the possibly_affected view as a table). No new data: every extra table is
# computed from -db. The MCP server reads them; -all is built
# from this image. Strangers who want the dataset pull -db.
# Build from the repository root:
#   docker build -f db/prod/Dockerfile --build-arg DATA_TAG=latest -t lebanese-legislation-db-prod .
ARG DATA_TAG=latest
FROM ghcr.io/taktekhq/lebanese-legislation-db:${DATA_TAG}
LABEL org.opencontainers.image.description="The Lebanese Legislation database (the -db image) plus read-optimized tables computed from it by db/prod.sql, for the MCP server. No new data. For the dataset itself pull lebanese-legislation-db."
COPY db/prod.sql /opt/lebleg/db/prod.sql
# Apply it to the baked cluster and stop cleanly, so the tables are in the
# image. pg_resetwal drops the WAL the build wrote (a clean stop is required).
RUN set -eu; \
    gosu postgres pg_ctl -D /opt/lebleg/seed -o "-c listen_addresses='' -c unix_socket_directories=/tmp -c maintenance_work_mem=1GB -c dynamic_shared_memory_type=mmap -c fsync=off -c full_page_writes=off -c wal_level=minimal -c max_wal_senders=0 -c max_wal_size=8GB" -w start; \
    psql -h /tmp -U postgres -d lebleg -v ON_ERROR_STOP=1 -q -f /opt/lebleg/db/prod.sql; \
    psql -h /tmp -U postgres -d lebleg -At -c "SELECT relname || ' ' || pg_size_pretty(pg_total_relation_size(oid)) FROM pg_class WHERE relname IN ('law_card', 'article_card', 'possibly_affected_t')"; \
    gosu postgres pg_ctl -D /opt/lebleg/seed -m fast -w stop; \
    gosu postgres pg_resetwal -D /opt/lebleg/seed; \
    du -sh /opt/lebleg/seed
