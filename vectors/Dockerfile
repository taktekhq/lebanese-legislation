# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS commands
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY db/ ./db/
COPY cmd/vectorize-sync/ ./cmd/vectorize-sync/
COPY cmd/export-vectors/ ./cmd/export-vectors/
ARG TARGETOS
ARG TARGETARCH
RUN for c in vectorize-sync export-vectors; do \
      CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/$c ./cmd/$c || exit 1; \
    done
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip \
 && rm -rf /var/lib/apt/lists/*
ARG TARGETARCH
RUN curl -fsSL --retry 3 -o /tmp/duckdb.zip "https://github.com/duckdb/duckdb/releases/download/v1.5.6/duckdb_cli-linux-${TARGETARCH:-amd64}.zip" \
 && unzip -q /tmp/duckdb.zip -d /usr/local/bin && rm /tmp/duckdb.zip
COPY --from=commands /out/ /usr/local/bin/
COPY --chmod=0755 vectors/start.sh /usr/local/bin/load-vectors
COPY LICENSE THIRD_PARTY_NOTICES.txt /opt/lebleg/
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.licenses="MIT"
WORKDIR /work
ENTRYPOINT ["load-vectors"]
