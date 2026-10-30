ARG API_IMAGE=ghcr.io/taktekhq/lebanese-legislation-api:0.1.5
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /server ./cmd/website

FROM gcr.io/distroless/static-debian12:nonroot AS website
COPY --from=build /server /server
COPY THIRD_PARTY_NOTICES.txt /THIRD_PARTY_NOTICES.txt
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/server"]

FROM ${API_IMAGE} AS mcp
ENV APP_MODE=mcp
EXPOSE 8080
CMD ["serve", "--host", "0.0.0.0", "--port", "8080"]
