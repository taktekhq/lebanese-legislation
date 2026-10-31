# syntax=docker/dockerfile:1
# The LebaneseLegislation.com website, published as ghcr.io/taktekhq/lebanese-legislation-web.
# Build from the repository root: docker build -f web/Dockerfile -t lebanese-legislation-web .
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY web/main.go ./web/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./web

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.licenses="MIT"
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=10s --timeout=6s --retries=12 CMD ["/server", "healthcheck"]
ENTRYPOINT ["/server"]
