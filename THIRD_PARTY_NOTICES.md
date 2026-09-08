# Third-Party Notices

OpenSurge for QNAP is licensed under `GPL-3.0-only`. Independent programs and
libraries retain their own upstream licenses.

> **Phase 1 status:** this branch does **not yet publish the QNAP Docker image**.
> Therefore this file does not claim that a `docker/Dockerfile`, SBOM pipeline,
> image-embedded license directory, or release-time license verifier already
> exists. Those become release requirements when the Docker packaging phase is
> implemented.

## Upstream runtime components carried by the source baseline

### mihomo

- Upstream: <https://github.com/MetaCubeX/mihomo>
- License: `GPL-3.0-only`
- Baseline version inherited from OpenSurge for Mac: `1.19.30`
- QNAP v1 direction: use unpatched upstream mihomo; the upstream macOS
  `opensurge-packet` patch exists for the removed macOS BPF IPv6 packet broker
  and is not part of the Linux IPv4 design.

**Before the first Docker release:** pin an exact mihomo source commit/archive,
record its checksum, preserve the applicable license text, and ensure the
corresponding source is obtainable for the exact binary shipped.

### dnsmasq

- Upstream: <https://thekelleys.org.uk/dnsmasq/>
- License: `GPL-2.0-only OR GPL-3.0-only`, at the recipient's option
- Baseline version inherited from the upstream fork point: `2.93`
- License texts already retained in this repository:
  [`third_party/licenses/dnsmasq-COPYING`](third_party/licenses/dnsmasq-COPYING)
  and [`LICENSE`](LICENSE)

**Before the first Docker release:** pin the exact source archive, verify its
checksum, and make the corresponding source path part of the release metadata.

## Go dependencies

`go.mod` is the authoritative dependency-version manifest for the current
source tree. Notable direct dependencies include:

### gopkg.in/yaml.v3

- Version: `3.0.1`
- License: MIT / Apache-2.0 according to upstream per-file notices
- Upstream: <https://github.com/go-yaml/yaml/tree/v3.0.1>
- License texts:
  [`third_party/licenses/yaml-v3-LICENSE`](third_party/licenses/yaml-v3-LICENSE)
  and [`third_party/licenses/Apache-2.0.txt`](third_party/licenses/Apache-2.0.txt)

### github.com/metacubex/bbolt

- Version: `v0.0.0-20260706163408-d4ec34ad7c48`
- License: MIT
- Upstream: <https://github.com/metacubex/bbolt/tree/d4ec34ad7c48>
- License text: [`third_party/licenses/bbolt-MIT.txt`](third_party/licenses/bbolt-MIT.txt)

The first distributable container release must additionally generate and archive
a complete Go dependency/license report and SBOM rather than relying on this
human-maintained summary alone.

## Frontend dependencies

The Web UI retains the upstream React-based frontend. `web/package.json` and its
lockfile are the authoritative dependency-version manifests for Phase 1.
Existing third-party license texts are preserved under `third_party/licenses/`.

The first distributable container release must generate a frontend dependency
license report from the actual locked build inputs.

## Required release-time controls (not yet claimed as implemented)

Before publishing the first QNAP/Linux Docker image, the project must add and
verify all of the following:

1. exact pinned mihomo and dnsmasq source/binary versions;
2. source URLs/commit IDs and cryptographic checksums;
3. pinned base-image digest and OS-package manifest;
4. complete SBOM and dependency license reports;
5. corresponding-source instructions for GPL-covered binaries;
6. CI checks that fail when release contents and this notice diverge.

## Upstream attribution

This project is a derivative work of
[OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) by YTwsy,
licensed `GPL-3.0-only`. See [NOTICE.md](NOTICE.md) and
[docs/UPSTREAM.md](docs/UPSTREAM.md). Upstream copyright notices and the
`LICENSE` file are preserved.
