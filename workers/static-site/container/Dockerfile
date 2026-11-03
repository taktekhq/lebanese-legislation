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

FROM ghcr.io/taktekhq/lebanese-legislation-db:${DATA_TAG}
ARG GIT_SHA=unknown
LABEL org.opencontainers.image.revision="${GIT_SHA}"
LABEL org.opencontainers.image.description="Lebanese Legislation in one image: the database, website, JSON API and MCP server. The same image runs lebaneselegislation.com on Cloudflare Containers. Pull :latest."
ENV PGDATA=/opt/lebleg/seed MCP_SHARED_SERVER=1
COPY --from=web /server /usr/local/bin/lebleg-web
COPY --from=web /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
COPY --chmod=0755 workers/static-site/container/start.sh /usr/local/bin/start
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/start"]
CMD []
