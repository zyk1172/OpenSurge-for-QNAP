# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e
ARG GO_IMAGE=golang:1.25.14-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437
ARG DEBIAN_IMAGE=debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web-builder
ARG OPENSURGE_VERSION=dev
WORKDIR /src/web
RUN npm install --global pnpm@10.15.1
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN OPENSURGE_RELEASE_TAG="${OPENSURGE_VERSION}" pnpm build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/webui/dist/ ./internal/webui/dist/
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -o /out/opensurge-container ./cmd/opensurge-container \
 && CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -o /out/omg ./cmd/omg

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

# Upstream v1.19.30 is the latest stable Mihomo release, but its published
# binaries were built before several Go dependency security fixes. Rebuild the
# exact stable source commit with the patched module versions instead of
# switching a stable OpenSurge release to an upstream alpha binary.
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS mihomo-builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG MIHOMO_VERSION=1.19.30
ARG MIHOMO_COMMIT=ac017cdd246ce8bd547653d927e7bf77d7ee73d5
WORKDIR /src/mihomo
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    git init; \
    git remote add origin https://github.com/MetaCubeX/mihomo.git; \
    git fetch --depth=1 origin "refs/tags/v${MIHOMO_VERSION}"; \
    git checkout --detach FETCH_HEAD; \
    test "$(git rev-parse HEAD)" = "${MIHOMO_COMMIT}"; \
    go mod edit -go=1.25.14; \
    go get golang.org/x/crypto@v0.55.0; \
    go mod tidy; \
    case "${TARGETARCH}" in \
      amd64) export GOAMD64=v1 ;; \
      arm64) : ;; \
      *) echo "unsupported target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
      go build -tags with_gvisor -trimpath \
      -ldflags="-X github.com/metacubex/mihomo/constant.Version=v${MIHOMO_VERSION}+opensurge-security -X github.com/metacubex/mihomo/constant.BuildTime=OpenSurge-security-rebuild -w -s -buildid=" \
      -o /out-mihomo .

FROM ${DEBIAN_IMAGE} AS runtime
ARG OPENSURGE_VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
ARG MIHOMO_VERSION=1.19.30
ARG MIHOMO_COMMIT=ac017cdd246ce8bd547653d927e7bf77d7ee73d5
ARG DNSMASQ_VERSION=2.93

RUN apt-get update \
 && apt-get upgrade -y \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl iproute2 netbase nftables tini tzdata util-linux \
 && grep -Eq '^udp[[:space:]]+17([[:space:]]|$)' /etc/protocols \
 && grep -Eq '^tcp[[:space:]]+6([[:space:]]|$)' /etc/protocols \
 && rm -rf /var/lib/apt/lists/*

COPY --from=go-builder /out/opensurge-container /usr/local/bin/opensurge-container
COPY --from=go-builder /out/omg /usr/local/bin/omg
COPY --from=dnsmasq-builder /out/dnsmasq /usr/local/bin/dnsmasq
COPY --from=mihomo-builder /out-mihomo /usr/local/bin/mihomo
COPY docker/entrypoint.sh /usr/local/bin/opensurge-entrypoint
COPY deploy/qnap/config.qnap-docker.yaml /usr/share/opensurge/config.qnap-docker.yaml
COPY LICENSE THIRD_PARTY_NOTICES.md NOTICE.md /usr/share/opensurge/licenses/
COPY third_party/licenses/ /usr/share/opensurge/licenses/third_party/

RUN chmod 0755 /usr/local/bin/opensurge-entrypoint \
 && mkdir -p /data/config /data/profiles /data/providers /data/runtime /data/state /data/logs /data/backups /data/licenses /data/control /data/web-auth /run/opensurge

LABEL org.opencontainers.image.title="OpenSurge for QNAP" \
      org.opencontainers.image.description="Web-managed Linux transparent proxy gateway for QNAP NAS" \
      org.opencontainers.image.source="https://github.com/zyk1172/OpenSurge-for-QNAP" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.version="${OPENSURGE_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opensurge.mihomo.version="${MIHOMO_VERSION}" \
      org.opensurge.mihomo.source-revision="${MIHOMO_COMMIT}" \
      org.opensurge.dnsmasq.version="${DNSMASQ_VERSION}"

VOLUME ["/data"]
EXPOSE 8080/tcp 61780/tcp 53/tcp 53/udp

# Health is defined by desired-vs-observed gateway state, not merely by the Web
# and Control processes answering HTTP. An intentionally stopped gateway is
# healthy; when the persisted intent is running, Mihomo/DNS/TUN/policy routing
# must all be active in the current container network namespace.
HEALTHCHECK --interval=30s --timeout=8s --start-period=30s --retries=3 \
  CMD /bin/sh -c '/usr/local/bin/opensurge-container --component health --config "${OPENSURGE_CONFIG:-/data/config/opensurge.yaml}" >/dev/null' || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/opensurge-entrypoint"]
