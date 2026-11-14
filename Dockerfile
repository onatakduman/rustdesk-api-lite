# Build straight from the repository, no checkout needed:
#   docker build -t rustdesk-api-lite https://github.com/onatakduman/rustdesk-api-lite.git#v0.1.1
# Run:
#   docker run -d --name rustdesk-api-lite --restart unless-stopped \
#     -p 127.0.0.1:21114:21114 -v rustdesk-api-lite:/data rustdesk-api-lite
#   docker logs rustdesk-api-lite | grep 'First start'     # the admin password
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/onatakduman/rustdesk-api-lite/internal/server.Version=${VERSION}" \
    -o /rustdesk-api-lite .

FROM scratch
COPY --from=build /rustdesk-api-lite /rustdesk-api-lite
# Certificates for sign-in providers (OpenID Connect, GitHub).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV RDL_DB=/data/rustdesk-api-lite.db RDL_LISTEN=0.0.0.0:21114 RDL_TRUST_PROXY=1
VOLUME /data
EXPOSE 21114
ENTRYPOINT ["/rustdesk-api-lite"]
CMD ["serve"]
