# Third-Party Notices

OpenSurge for QNAP is licensed under `GPL-3.0-only`. Independent programs and
libraries retain their own upstream licenses.

> **Phase 2 status:** `docker/Dockerfile` now builds reproducible development
> images for `linux/amd64` and `linux/arm64`, and CI verifies both architectures.
> No public stable image is claimed yet. Base-image digest pinning, full SBOM,
> provenance/signing and release-time license reports remain release gates.

## Programs distributed in the Docker image

### mihomo

- Upstream: <https://github.com/MetaCubeX/mihomo>
- License: `GPL-3.0-only`
- Version: `1.19.30`
- Patched: **No.** The Linux/QNAP image uses unmodified upstream mihomo. The
  macOS-only `opensurge-packet` patch from the original project is not applied.
- Release source: <https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.30>
- `linux/amd64` asset: `mihomo-linux-amd64-compatible-v1.19.30.gz`
  - SHA-256: `db214c7a2517e63c150d123178d16d102e03a241ccdae4e5e07ffbe9cf56c6f9`
- `linux/arm64` asset: `mihomo-linux-arm64-v1.19.30.gz`
  - SHA-256: `58896873736d28628f66de3677c8654fa0f180662523148e136cff4f6e890069`

`docker/Dockerfile` verifies the selected release asset checksum before
installing `/usr/local/bin/mihomo`. The applicable GPL text is included in the
image under `/usr/share/opensurge/licenses/`.

### dnsmasq

- Upstream: <https://thekelleys.org.uk/dnsmasq/>
- License: `GPL-2.0-only OR GPL-3.0-only`, at the recipient's option
- Version: `2.93`
- Source archive: <https://thekelleys.org.uk/dnsmasq/dnsmasq-2.93.tar.xz>
- Source archive SHA-256:
  `0c00d4e5c97c8306e5fb932b348b34269c9c29a0e7df0e8e82958b407092bc19`
- Distributed form: built from that verified, unmodified source archive inside
  the target-platform Docker build stage.

The retained dnsmasq COPYING text is included under
`/usr/share/opensurge/licenses/third_party/` in the image.

### Base image and OS packages

The development image currently uses Debian Bookworm-based tagged build/runtime
images and installs runtime packages including `ca-certificates`, `curl`,
`iproute2`, `nftables`, `tini`, and `tzdata`.

**Stable-release gate:** replace floating base-image tags with explicit immutable
digests and archive the resolved OS package manifest/SBOM. A development build
passing CI is not evidence that those release-supply-chain controls are complete.

## Go dependencies

`go.mod` is the authoritative dependency-version manifest. Direct dependencies
currently include:

### golang.org/x/crypto

- Version: `v0.26.0`
- Use: Argon2id administrator password hashing for the LAN-facing Web Gateway
- License: BSD-3-Clause
- Upstream: <https://cs.opensource.google/go/x/crypto>

### gopkg.in/yaml.v3

- Version: `3.0.1`
- License: MIT / Apache-2.0 according to upstream per-file notices
- Upstream: <https://github.com/go-yaml/yaml/tree/v3.0.1>
- Retained license texts:
  [`third_party/licenses/yaml-v3-LICENSE`](third_party/licenses/yaml-v3-LICENSE)
  and [`third_party/licenses/Apache-2.0.txt`](third_party/licenses/Apache-2.0.txt)

### github.com/metacubex/bbolt

- Version: `v0.0.0-20260706163408-d4ec34ad7c48`
- License: MIT
- Upstream: <https://github.com/metacubex/bbolt/tree/d4ec34ad7c48>
- Retained license text:
  [`third_party/licenses/bbolt-MIT.txt`](third_party/licenses/bbolt-MIT.txt)

### github.com/quic-go/quic-go

- Version: `v0.54.1`
- License: MIT
- Upstream: <https://github.com/quic-go/quic-go>

A stable container release must generate and archive a complete Go module
license report and SBOM rather than relying on this human-maintained summary.

## Frontend dependencies

The image embeds the React Web UI produced from the exact `web/pnpm-lock.yaml`
lockfile using the pnpm version pinned in `docker/Dockerfile`. Existing retained
third-party license texts are copied into the image.

A stable release must additionally generate a complete frontend dependency
license report from the actual locked build inputs.

## Implemented build-time controls

The Phase 2 development image already enforces:

1. exact mihomo version and architecture-specific binary SHA-256;
2. exact dnsmasq source version and source archive SHA-256;
3. source URLs recorded in this notice and build file;
4. third-party retained license texts copied into the image;
5. amd64 and arm64 Docker builds exercised in CI.

## Remaining stable-release controls

Before publishing the first stable QNAP/Linux image, the project must still:

1. pin Node, Go and Debian base images by immutable digest;
2. generate an OS package manifest for the final stage;
3. generate a complete SPDX/CycloneDX SBOM;
4. generate Go/frontend license reports from the actual release build;
5. publish build provenance and checksums/signatures for release artifacts;
6. verify this notice against the exact final image contents;
7. document corresponding-source availability for every GPL-covered component.

## Upstream attribution

This project is a derivative work of
[OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) by YTwsy,
licensed `GPL-3.0-only`. See [NOTICE.md](NOTICE.md) and
[docs/UPSTREAM.md](docs/UPSTREAM.md). Upstream copyright notices and the
`LICENSE` file are preserved.
