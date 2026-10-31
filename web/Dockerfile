# syntax=docker/dockerfile:1
# The LebaneseLegislation.com website, for local or offline hosting. Not published as an image.
# Build from the repository root: docker build -f web/Dockerfile -t lebanese-legislation-web .
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY web/go.mod web/go.sum ./
RUN go mod download
COPY web/cmd/ ./cmd/
COPY web/internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./cmd/website

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
COPY web/THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
LABEL org.opencontainers.image.source="https://github.com/taktekhq/lebanese-legislation"
LABEL org.opencontainers.image.licenses="MIT"
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/server"]
