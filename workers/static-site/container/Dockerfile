# syntax=docker/dockerfile:1
ARG DATA_TAG=latest
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS web
WORKDIR /src
COPY build/src/go.mod build/src/go.sum ./
RUN go mod download
COPY build/src/internal/ ./internal/
COPY build/src/web/main.go ./web/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./web

FROM ghcr.io/taktekhq/lebanese-legislation-data:${DATA_TAG}
COPY --from=web /server /usr/local/bin/lebleg-web
COPY build/src/THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
COPY --chmod=0755 start.sh /usr/local/bin/start
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/start"]
CMD []
