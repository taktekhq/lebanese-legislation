# syntax=docker/dockerfile:1
# PostgreSQL 18.6 with pgvector 0.8.2. On first start with an empty volume it
# downloads, verifies and restores the public database release.
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

# The restore code is the tools/ package, installed with matching PostgreSQL clients.
FROM postgres AS backend
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 python3-venv ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && python3 -m venv /opt/venv
WORKDIR /opt/lebleg
COPY LICENSE ./
COPY tools/pyproject.toml ./tools/
COPY tools/src/ ./tools/src/
RUN /opt/venv/bin/pip install --no-cache-dir ./tools
LABEL org.opencontainers.image.licenses="MIT"
ENV PATH="/opt/venv/bin:$PATH" PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

# Optional contributor tools: rebuild and publish snapshots from public Parquet.
FROM backend AS publisher
RUN /opt/venv/bin/pip install --no-cache-dir './tools[build]'
ENTRYPOINT ["lebleg"]
CMD ["--help"]

# Default target, published as ghcr.io/taktekhq/lebanese-legislation-db.
FROM backend AS db
COPY db/release.py db/health.sh db/web-schema.sh db/web.sql ./db/
COPY db/init-release.sh /docker-entrypoint-initdb.d/30-lebleg-release.sh
HEALTHCHECK --interval=10s --timeout=10s --start-period=2h --retries=3 CMD ["bash", "/opt/lebleg/db/health.sh"]
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["postgres"]
