# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:24-bookworm-slim
ARG GO_IMAGE=golang:1.25-bookworm
ARG DEBIAN_IMAGE=debian:bookworm-slim

FROM ${NODE_IMAGE} AS web-builder
ARG OPENSURGE_VERSION=dev
WORKDIR /src/web
RUN npm install --global pnpm@10.15.1
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN OPENSURGE_RELEASE_TAG="${OPENSURGE_VERSION}" pnpm build

FROM ${GO_IMAGE} AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/webui/dist/ ./internal/webui/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/opensurge-container ./cmd/opensurge-container \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/omg ./cmd/omg

FROM ${DEBIAN_IMAGE} AS dnsmasq-builder
ARG DNSMASQ_VERSION=2.93
ARG DNSMASQ_SHA256=0c00d4e5c97c8306e5fb932b348b34269c9c29a0e7df0e8e82958b407092bc19
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl xz-utils build-essential \
 && rm -rf /var/lib/apt/lists/*
RUN curl --fail --show-error --location --proto '=https' \
      "https://thekelleys.org.uk/dnsmasq/dnsmasq-${DNSMASQ_VERSION}.tar.xz" \
      -o /tmp/dnsmasq.tar.xz \
 && echo "${DNSMASQ_SHA256}  /tmp/dnsmasq.tar.xz" | sha256sum -c - \
 && mkdir -p /tmp/dnsmasq \
 && tar -xJf /tmp/dnsmasq.tar.xz -C /tmp/dnsmasq --strip-components=1 \
 && make -C /tmp/dnsmasq -j"$(nproc)" \
 && install -Dm0755 /tmp/dnsmasq/src/dnsmasq /out/dnsmasq

FROM ${DEBIAN_IMAGE} AS mihomo-fetcher
ARG TARGETARCH
ARG MIHOMO_VERSION=1.19.30
ARG MIHOMO_AMD64_SHA256=db214c7a2517e63c150d123178d16d102e03a241ccdae4e5e07ffbe9cf56c6f9
ARG MIHOMO_ARM64_SHA256=58896873736d28628f66de3677c8654fa0f180662523148e136cff4f6e890069
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gzip \
 && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    arch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    case "$arch" in \
      amd64) asset="mihomo-linux-amd64-compatible-v${MIHOMO_VERSION}.gz"; sha="${MIHOMO_AMD64_SHA256}" ;; \
      arm64) asset="mihomo-linux-arm64-v${MIHOMO_VERSION}.gz"; sha="${MIHOMO_ARM64_SHA256}" ;; \
      *) echo "unsupported target architecture: $arch" >&2; exit 1 ;; \
    esac; \
    curl --fail --show-error --location --proto '=https' \
      "https://github.com/MetaCubeX/mihomo/releases/download/v${MIHOMO_VERSION}/${asset}" \
      -o /tmp/mihomo.gz; \
    echo "${sha}  /tmp/mihomo.gz" | sha256sum -c -; \
    gunzip -c /tmp/mihomo.gz > /out-mihomo; \
    chmod 0755 /out-mihomo

FROM ${DEBIAN_IMAGE} AS runtime
ARG OPENSURGE_VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
ARG MIHOMO_VERSION=1.19.30
ARG DNSMASQ_VERSION=2.93

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl iproute2 nftables tini tzdata \
 && rm -rf /var/lib/apt/lists/*

COPY --from=go-builder /out/opensurge-container /usr/local/bin/opensurge-container
COPY --from=go-builder /out/omg /usr/local/bin/omg
COPY --from=dnsmasq-builder /out/dnsmasq /usr/local/bin/dnsmasq
COPY --from=mihomo-fetcher /out-mihomo /usr/local/bin/mihomo
COPY docker/entrypoint.sh /usr/local/bin/opensurge-entrypoint
COPY deploy/qnap/config.qnap-docker.yaml /usr/share/opensurge/config.qnap-docker.yaml
COPY LICENSE THIRD_PARTY_NOTICES.md NOTICE.md /usr/share/opensurge/licenses/

RUN chmod 0755 /usr/local/bin/opensurge-entrypoint \
 && mkdir -p /data/config /data/profiles /data/providers /data/runtime /data/state /data/logs /data/backups /data/licenses /data/control

LABEL org.opencontainers.image.title="OpenSurge for QNAP" \
      org.opencontainers.image.description="Web-managed Linux transparent proxy gateway for QNAP NAS" \
      org.opencontainers.image.source="https://github.com/zyk1172/OpenSurge-for-QNAP" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.version="${OPENSURGE_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opensurge.mihomo.version="${MIHOMO_VERSION}" \
      org.opensurge.dnsmasq.version="${DNSMASQ_VERSION}"

VOLUME ["/data"]
EXPOSE 8080/tcp 53/tcp 53/udp

HEALTHCHECK --interval=30s --timeout=4s --start-period=20s --retries=3 \
  CMD curl --fail --silent --show-error http://127.0.0.1:8080/health/live >/dev/null || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/opensurge-entrypoint"]
