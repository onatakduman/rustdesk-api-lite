# The published image, for x86-64 and ARM64:
#   docker run -d --name rustdesk-api-lite --restart unless-stopped \
#     -p 127.0.0.1:21114:21114 -v rustdesk-api-lite:/data ghcr.io/onatakduman/rustdesk-api-lite
#   docker logs rustdesk-api-lite | grep 'First start'     # the admin password
# Or build it straight from the repository, no checkout needed:
#   docker build -t rustdesk-api-lite https://github.com/onatakduman/rustdesk-api-lite.git#v0.2.0

# The build stage runs on the CPU of the machine that builds and cross-compiles,
# so an image for another platform needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/onatakduman/rustdesk-api-lite/internal/server.Version=${VERSION}" \
    -o /rustdesk-api-lite .

FROM scratch
LABEL org.opencontainers.image.title="RustDesk API Lite" \
      org.opencontainers.image.description="Accounts, address books and a web console for self-hosted RustDesk" \
      org.opencontainers.image.source="https://github.com/onatakduman/rustdesk-api-lite" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /rustdesk-api-lite /rustdesk-api-lite
# Certificates for sign-in providers (OpenID Connect, GitHub).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV RDL_DB=/data/rustdesk-api-lite.db RDL_LISTEN=0.0.0.0:21114 RDL_TRUST_PROXY=1
VOLUME /data
EXPOSE 21114
ENTRYPOINT ["/rustdesk-api-lite"]
CMD ["serve"]
