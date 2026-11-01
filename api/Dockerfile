# syntax=docker/dockerfile:1
# The REST API (/api/v1/), published as ghcr.io/taktekhq/lebanese-legislation-api.
# Build from the repository root: docker build -f api/Dockerfile -t lebanese-legislation-api .
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
# internal/calculatedcurrent reads the wording guard (internal/provenance), which needs the db package.
COPY db/ ./db/
COPY api/main.go ./api/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.licenses="MIT"
ENV LISTEN_ADDR=0.0.0.0:8000 RATE_LIMIT_PER_MINUTE=0 PUBLIC_URL=http://localhost:8080 ANALYTICS=off
EXPOSE 8000
USER 65532:65532
HEALTHCHECK --interval=10s --timeout=6s --retries=12 CMD ["/server", "healthcheck"]
ENTRYPOINT ["/server"]
