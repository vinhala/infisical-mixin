# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.23
ARG ALPINE_VERSION=3.20
ARG INFISICAL_CLI_VERSION=0.43.100

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS go-base
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

FROM go-base AS test
COPY . .
RUN go test ./...

FROM go-base AS entrypoint-builder
ARG TARGETOS
ARG TARGETARCH
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/infisical-mixin ./cmd/infisical-mixin

FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS infisical-cli
ARG INFISICAL_CLI_VERSION
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache ca-certificates curl tar
RUN <<EOF
set -eu
case "${TARGETARCH:-amd64}" in
  amd64|arm64) arch="${TARGETARCH:-amd64}" ;;
  *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;;
esac
os="${TARGETOS:-linux}"
url="https://github.com/Infisical/cli/releases/download/v${INFISICAL_CLI_VERSION}/cli_${INFISICAL_CLI_VERSION}_${os}_${arch}.tar.gz"
curl -fsSL -o /tmp/infisical-cli.tar.gz "$url"
tar -xzf /tmp/infisical-cli.tar.gz -C /usr/local/bin infisical
chmod 0755 /usr/local/bin/infisical
EOF

FROM entrypoint-builder AS smoke
RUN apk add --no-cache busybox
RUN <<'EOF'
set -eu
cat > /usr/local/bin/infisical <<'SCRIPT'
#!/bin/sh
if [ "$1" = "export" ]; then
  printf '%s' '{"SERVICE_1_DATABASE_URL":"postgres://one","SERVICE_2_DATABASE_URL":"postgres://two"}'
  exit 0
fi
echo "unexpected fake infisical command: $*" >&2
exit 1
SCRIPT
chmod 0755 /usr/local/bin/infisical
mkdir -p /smoke
cat > /smoke/infisical_mapping.yml <<'YAML'
SERVICE_1_DATABASE_URL:
  aliases:
    - POSTGRES_URL
SERVICE_2_DATABASE_URL:
  aliases:
    - POSTGRES_URL
YAML
EOF
WORKDIR /smoke
RUN INFISICAL_TOKEN=fake INFISICAL_PROJECT_ID=project-a \
    /out/infisical-mixin /bin/sh -c 'test "$SERVICE_1_DATABASE_URL" = "postgres://one" && test "$POSTGRES_URL" = "postgres://two"'

FROM scratch
ARG INFISICAL_CLI_VERSION
LABEL org.opencontainers.image.title="infisical-mixin"
LABEL org.opencontainers.image.description="Mixin layer for injecting Infisical secrets into container environments"
LABEL org.opencontainers.image.source="https://github.com/vinhala/infisical-mixin"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.version="${INFISICAL_CLI_VERSION}"
COPY --from=infisical-cli /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=infisical-cli /usr/local/bin/infisical /usr/local/bin/infisical
COPY --from=entrypoint-builder /out/infisical-mixin /usr/local/bin/infisical-mixin
COPY LICENSE /LICENSE
