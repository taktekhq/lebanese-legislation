# syntax=docker/dockerfile:1
# The MCP server (/mcp), published as ghcr.io/taktekhq/lebanese-legislation-mcp.
# Build from the repository root: docker build -f mcp/Dockerfile -t lebanese-legislation-mcp .
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY mcp/main.go ./mcp/
ARG TARGETOS
ARG TARGETARCH
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/taktekhq/lebanese-legislation/internal/site.BuildID=${GIT_SHA}" -o /server ./mcp

FROM gcr.io/distroless/static-debian12:nonroot
ARG GIT_SHA=unknown
LABEL org.opencontainers.image.revision="${GIT_SHA}"
COPY --from=build /server /server
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.description="The Lebanese Legislation MCP server in Go: ten tools that search and read Lebanese law verbatim, on the -db image. Pull :latest."
LABEL org.opencontainers.image.licenses="MIT"
ENV LISTEN_ADDR=0.0.0.0:8080 RATE_LIMIT_PER_MINUTE=0 PUBLIC_URL=http://localhost:8080 ANALYTICS=off
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=10s --timeout=6s --retries=12 CMD ["/server", "healthcheck"]
ENTRYPOINT ["/server"]
