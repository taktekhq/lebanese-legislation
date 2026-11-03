# syntax=docker/dockerfile:1
# PostgreSQL 18.6 with pgvector 0.8.2 and both databases baked from data/.
# Build from the repository root: docker build -f db/Dockerfile -t lebanese-legislation-db .
# PostgreSQL and extension source versions are pinned for reproducible restores.
ARG POSTGRES_BASE=postgres:18.6-trixie@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4

FROM ${POSTGRES_BASE} AS pgvector-build
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential ca-certificates curl postgresql-server-dev-18="$PG_VERSION" \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fsSL --retry 3 https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.2.tar.gz -o pgvector.tar.gz \
 && echo '69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa  pgvector.tar.gz' | sha256sum -c - \
 && tar -xzf pgvector.tar.gz \
 && make -C pgvector-0.8.2 PG_CONFIG=/usr/lib/postgresql/18/bin/pg_config OPTFLAGS= \
 && make -C pgvector-0.8.2 PG_CONFIG=/usr/lib/postgresql/18/bin/pg_config DESTDIR=/out install

# Plain PostgreSQL with pgvector and the optional read-only role. Tests use this target.
FROM ${POSTGRES_BASE} AS postgres
LABEL org.lebleg.pgvector-source-sha256="69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa"
COPY --from=pgvector-build /out/usr/lib/postgresql/18/lib/vector.so /usr/lib/postgresql/18/lib/vector.so
COPY --from=pgvector-build /out/usr/share/postgresql/18/extension/vector* /usr/share/postgresql/18/extension/
COPY db/init-reader.sh /docker-entrypoint-initdb.d/20-lebleg-reader.sh
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.description="PostgreSQL 18.6 with pgvector and the whole Lebanese Legislation database baked in: laws, articles, court rulings and Gazette issues from lebaneselegislation.com. Pull :latest for the newest data."
LABEL org.opencontainers.image.licenses="PostgreSQL"

# The release commands (cmd/restore-release, cmd/check-release, cmd/build-release,
# cmd/publish-release), built for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS commands
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY db/*.go db/*.sql db/*.tsv ./db/
COPY db/model/judgements.jsonl ./db/model/
COPY cmd/restore-release/ ./cmd/restore-release/
COPY cmd/check-release/ ./cmd/check-release/
COPY cmd/build-release/ ./cmd/build-release/
COPY cmd/publish-release/ ./cmd/publish-release/
COPY cmd/build-search/ ./cmd/build-search/
ARG TARGETOS
ARG TARGETARCH
RUN for c in restore-release check-release build-release publish-release build-search; do \
      CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/$c ./cmd/$c || exit 1; \
    done

# PostgreSQL with the restore and its check, beside the matching PostgreSQL clients.
FROM postgres AS backend
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/lebleg
COPY LICENSE THIRD_PARTY_NOTICES.txt ./
COPY --from=commands /out/restore-release /out/check-release /usr/local/bin/
LABEL org.opencontainers.image.licenses="MIT"

# Legacy contributor tools, retained for image checks beside pg_dump 18.6.
FROM backend AS publisher
COPY --from=commands /out/build-release /out/publish-release /usr/local/bin/
CMD ["build-release", "-h"]

# Both databases are generated from the checkout, without embedding calls.
FROM backend AS baked
ENV PGDATA=/var/lib/postgresql/18/docker
COPY --from=commands /out/build-search /usr/local/bin/
COPY db/health.sh db/web-schema.sh tools/src/lebleg/sql/web.sql db/official.sql /opt/lebleg/db/
COPY db/bake.sh /opt/lebleg/db/bake.sh
# Optional scans are synced from R2 before the image build. No database dumps enter the build.
COPY db/scans/ /scans/
RUN --mount=type=bind,source=data,target=/input/data bash /opt/lebleg/db/bake.sh
# This is a new, cleanly stopped cluster with no replicas or consumers. Reset
# its WAL without -f (unclean shutdowns are refused), discarding preallocated
# future segments left by the bulk load. Copy only the resulting files into
# the final image so discarded WAL is absent from every published layer.
RUN gosu postgres pg_resetwal -D /opt/lebleg/seed

FROM backend AS db
ENV PGDATA=/var/lib/postgresql/18/docker
COPY --from=baked --chown=postgres:postgres /opt/lebleg/seed /opt/lebleg/seed
COPY --from=baked /opt/lebleg/vector-ids.csv /opt/lebleg/vector-ids.csv
COPY --from=baked /opt/lebleg/db/ /opt/lebleg/db/
COPY --from=baked /scans/ /scans/
COPY --from=commands /out/build-search /usr/local/bin/
COPY --chmod=0755 db/start.sh /usr/local/bin/lebleg-db-start
RUN chmod 0700 /opt/lebleg/seed
HEALTHCHECK --interval=10s --timeout=10s --start-period=5m --retries=3 CMD ["bash", "/opt/lebleg/db/health.sh"]
ENTRYPOINT ["lebleg-db-start"]
CMD ["postgres"]
