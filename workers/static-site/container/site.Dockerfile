# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS server
WORKDIR /src
COPY go.mod ./
COPY cmd/static-serve/ ./cmd/static-serve/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./cmd/static-serve
FROM scratch
COPY --from=server /server /server
COPY site-content/ /site/
COPY LICENSE /LICENSE
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/server"]
