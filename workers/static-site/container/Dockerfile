# syntax=docker/dockerfile:1
ARG DATA_TAG=latest
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS web
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY web/main.go ./web/
ARG TARGETOS
ARG TARGETARCH
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/taktekhq/lebanese-legislation/internal/site.BuildID=${GIT_SHA}" -o /server ./web

# The MCP search's word lists (internal/wordsearch Index), made from the
# same database: search_legislation then ranks the candidates from them
# instead of reading every matching text from Postgres. 88 MB, 15 s on one
# CPU. The server maps the file read-only, checks it against the database at
# start and searches with the SQL when it does not match.
FROM ghcr.io/taktekhq/lebanese-legislation-db-prod:${DATA_TAG} AS search-index
COPY --from=web /server /usr/local/bin/lebleg-web
RUN set -eu; \
    gosu postgres pg_ctl -D /opt/lebleg/seed -o "-c listen_addresses='' -c unix_socket_directories=/tmp -c dynamic_shared_memory_type=mmap -c max_parallel_workers_per_gather=0" -w start; \
    DATABASE_URL="host=/tmp user=postgres dbname=lebleg" lebleg-web search-index /opt/lebleg/search-index; \
    gosu postgres pg_ctl -D /opt/lebleg/seed -m fast -w stop

# -db-prod: the -db database plus the read-optimized tables of db/prod.sql.
FROM ghcr.io/taktekhq/lebanese-legislation-db-prod:${DATA_TAG}
ARG GIT_SHA=unknown
LABEL org.opencontainers.image.revision="${GIT_SHA}"
LABEL org.opencontainers.image.description="Lebanese Legislation in one image: the database, website, JSON API and MCP server. The same image runs lebaneselegislation.com on Cloudflare Containers. Pull :latest."
ENV PGDATA=/opt/lebleg/seed MCP_SHARED_SERVER=1 MCP_SEARCH_INDEX=/opt/lebleg/search-index
COPY --from=web /server /usr/local/bin/lebleg-web
COPY --from=search-index /opt/lebleg/search-index /opt/lebleg/search-index
COPY --from=web /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
COPY --chmod=0755 workers/static-site/container/start.sh /usr/local/bin/start
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/start"]
CMD []
