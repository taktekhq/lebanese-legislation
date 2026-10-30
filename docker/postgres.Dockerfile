# PostgreSQL and extension source versions are pinned for reproducible restores.
FROM postgres:18.6-trixie@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4 AS build
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential ca-certificates curl postgresql-server-dev-18="$PG_VERSION" \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fsSL --retry 3 https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.2.tar.gz -o pgvector.tar.gz \
 && echo '69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa  pgvector.tar.gz' | sha256sum -c - \
 && tar -xzf pgvector.tar.gz \
 && make -C pgvector-0.8.2 PG_CONFIG=/usr/lib/postgresql/18/bin/pg_config OPTFLAGS= \
 && make -C pgvector-0.8.2 PG_CONFIG=/usr/lib/postgresql/18/bin/pg_config DESTDIR=/out install

FROM postgres:18.6-trixie@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4
LABEL org.lebleg.pgvector-source-sha256="69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa"
COPY --from=build /out/usr/lib/postgresql/18/lib/vector.so /usr/lib/postgresql/18/lib/vector.so
COPY --from=build /out/usr/share/postgresql/18/extension/vector* /usr/share/postgresql/18/extension/

LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation-starter"
LABEL org.opencontainers.image.licenses="PostgreSQL"
COPY docker/init-reader.sh /docker-entrypoint-initdb.d/20-lebleg-reader.sh
