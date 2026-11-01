# syntax=docker/dockerfile:1
# PostgreSQL 18.6 with pgvector 0.8.2. On first start with an empty volume it
# downloads, verifies and restores the public database release (restore-release).
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
LABEL org.opencontainers.image.licenses="PostgreSQL"

# The release commands (cmd/restore-release, cmd/check-release, cmd/build-release,
# cmd/publish-release), built for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS commands
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY cmd/restore-release/ ./cmd/restore-release/
COPY cmd/check-release/ ./cmd/check-release/
COPY cmd/build-release/ ./cmd/build-release/
COPY cmd/publish-release/ ./cmd/publish-release/
ARG TARGETOS
ARG TARGETARCH
RUN for c in restore-release check-release build-release publish-release; do \
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

# Contributor tools: build and publish a release. The workflow in
# .github/workflows/release.yml runs them here, beside pg_dump 18.6.
FROM backend AS publisher
COPY --from=commands /out/build-release /out/publish-release /usr/local/bin/
CMD ["build-release", "-h"]

# Default target, published as ghcr.io/taktekhq/lebanese-legislation-db.
FROM backend AS db
COPY db/health.sh db/web-schema.sh tools/src/lebleg/sql/web.sql ./db/
COPY db/init-release.sh /docker-entrypoint-initdb.d/30-lebleg-release.sh
HEALTHCHECK --interval=10s --timeout=10s --start-period=2h --retries=3 CMD ["bash", "/opt/lebleg/db/health.sh"]
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["postgres"]
