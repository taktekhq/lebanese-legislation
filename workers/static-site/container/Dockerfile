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
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./web

FROM ghcr.io/taktekhq/lebanese-legislation-db:${DATA_TAG}
ENV PGDATA=/opt/lebleg/seed MCP_SHARED_SERVER=1
COPY --from=web /server /usr/local/bin/lebleg-web
COPY --from=web /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
COPY --chmod=0755 workers/static-site/container/start.sh /usr/local/bin/start
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/start"]
CMD []
