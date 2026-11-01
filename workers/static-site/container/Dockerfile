# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS web
WORKDIR /src
COPY build/src/go.mod build/src/go.sum ./
RUN go mod download
COPY build/src/internal/ ./internal/
COPY build/src/web/main.go ./web/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./web

FROM postgres:18.6-trixie@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4 AS pgvector-build
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential ca-certificates curl postgresql-server-dev-18="$PG_VERSION" \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fsSL --retry 3 https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.2.tar.gz -o pgvector.tar.gz \
 && echo '69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa  pgvector.tar.gz' | sha256sum -c - \
 && tar -xzf pgvector.tar.gz \
 && make -C pgvector-0.8.2 PG_CONFIG=/usr/lib/postgresql/18/bin/pg_config OPTFLAGS= \
 && make -C pgvector-0.8.2 PG_CONFIG=/usr/lib/postgresql/18/bin/pg_config DESTDIR=/out install

FROM postgres:18.6-trixie@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4 AS pg
COPY --from=pgvector-build /out/usr/lib/postgresql/18/lib/vector.so /usr/lib/postgresql/18/lib/vector.so
COPY --from=pgvector-build /out/usr/share/postgresql/18/extension/vector* /usr/share/postgresql/18/extension/

FROM pg AS origin
ENV PGDATA=/var/lib/postgresql/data
COPY build/scans/ /scans/
# The dumps are mounted for this step only, so they never become an image layer.
RUN --mount=type=bind,source=build,target=/dumps <<'SH'
set -eu
install -d -o postgres -g postgres -m 0700 "$PGDATA"
gosu postgres initdb -D "$PGDATA"
printf 'local all all trust\nhost all all 127.0.0.1/32 trust\n' > "$PGDATA/pg_hba.conf"
gosu postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='' -c unix_socket_directories=/tmp -c maintenance_work_mem=2GB -c max_parallel_maintenance_workers=4 -c fsync=off -c full_page_writes=off -c max_wal_size=8GB" -w start
psql -h /tmp -U postgres -v ON_ERROR_STOP=1 -c 'CREATE ROLE lebleg_web LOGIN' -c 'CREATE ROLE lebleg'
for db in lebleg lebleg_search; do
  createdb -h /tmp -U postgres -O lebleg "$db"
  if ! pg_restore -h /tmp -U postgres --no-owner --no-privileges -j 4 -d "$db" "/dumps/$db.dump" 2> /tmp/restore.log; then
    cat /tmp/restore.log >&2
    # Only absent roles referenced by the dump's GRANTs may be ignored.
    awk '
      /^pg_restore: error:/ {
        errors++
        if ($0 !~ /could not execute query: ERROR:  role ".*" does not exist/) bad=1
        grant=0
        while ((getline line) > 0) {
          if (line ~ /^Command was: GRANT /) { grant=1; break }
          if (line ~ /^Command was:/) break
        }
        if (!grant) bad=1
      }
      END { exit (bad || !errors) }
    ' /tmp/restore.log
  fi
  psql -h /tmp -U postgres -d "$db" -v ON_ERROR_STOP=1 \
    -c "GRANT CONNECT ON DATABASE $db TO lebleg_web" \
    -c 'GRANT USAGE ON SCHEMA public TO lebleg_web' \
    -c 'GRANT SELECT ON ALL TABLES IN SCHEMA public TO lebleg_web' \
    -c 'GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO lebleg_web' \
    -c 'GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO lebleg_web' \
    -c 'ANALYZE'
done
gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop
rm -f /tmp/restore.log
SH
COPY --from=web /server /usr/local/bin/lebleg-web
COPY build/src/THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
RUN <<'SH'
set -eu
cat > /usr/local/bin/start <<'START'
#!/bin/sh
set -eu
export LEBLEG_VECTOR_DSN="${LEBLEG_VECTOR_DSN:-host=127.0.0.1 user=lebleg_web dbname=lebleg_search}"
gosu postgres postgres -D "$PGDATA" \
  -c listen_addresses=127.0.0.1 -c shared_buffers=1GB \
  -c max_connections=50 -c default_transaction_read_only=on &
ready=0
for i in $(seq 60); do
  if pg_isready -h 127.0.0.1 -U lebleg_web -d lebleg -q; then
    ready=1
    break
  fi
  sleep 1
done
[ "$ready" = 1 ] || { echo 'PostgreSQL did not become ready within 60 seconds' >&2; exit 1; }
install -d -o 65532 -g 65532 /tmp/lebleg-events
exec gosu 65532:65532 /usr/local/bin/lebleg-web \
  -addr 0.0.0.0:8080 -events /tmp/lebleg-events -scans /scans \
  -db "host=127.0.0.1 user=lebleg_web dbname=lebleg"
START
chmod 0755 /usr/local/bin/start
SH
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/start"]
