# Third-Party Notices

OpenSurge for QNAP is licensed under `GPL-3.0-only`. The independent programs and
libraries listed below retain their upstream licenses. The container image places
this notice and the referenced license texts under `/data/licenses/`.

> **Accuracy requirement.** The versions below must describe what is actually
> shipped in the published container image — they are not copied from the
> upstream macOS project. The authoritative values live in the `ARG` pins of
> `docker/Dockerfile` (`MIHOMO_VERSION`, `DNSMASQ_VERSION`) and are re-emitted and
> verified by CI on every build (see [§ Generated and verified in CI](#generated-and-verified-in-ci)).
> If the two ever disagree, the build fails.

---

## Programs distributed inside the container image

### mihomo

- **Patched?** **No.** The QNAP/Linux image ships **unpatched upstream mihomo**.
  The upstream macOS project applied `patches/mihomo/0001-opensurge-packet-listener.patch`
  to add an `opensurge-packet` listener for its macOS BPF downstream IPv6 packet
  broker. That mechanism is macOS-only (`/dev/bpf`) and IPv6 downstream takeover is
  out of scope for QNAP v1, so **no mihomo patch is applied in this fork**. If a
  patched build is ever introduced, this section must say so explicitly and record
  the patch.
- Version: pinned by `ARG MIHOMO_VERSION` in `docker/Dockerfile`.
  Current pin: `1.19.30` *(inherited from the upstream baseline; re-verified on the
  first QNAP image build — see below)*.
- License: `GPL-3.0-only`
- Distributed form: architecture-specific binary compiled from the pinned upstream
  source, unmodified.
- Upstream: <https://github.com/MetaCubeX/mihomo>
- Corresponding source: the source tarball / commit referenced by the pin in
  `docker/Dockerfile`, plus <https://github.com/MetaCubeX/mihomo>
- Source archive SHA-256: recorded in the build provenance output; verified by CI.
- License text: [`LICENSE`](LICENSE)

### dnsmasq

- Version: pinned by `ARG DNSMASQ_VERSION` in `docker/Dockerfile`.
  Current pin: `2.93` *(inherited from the upstream baseline; re-verified on the
  first QNAP image build)*.
- License: `GPL-2.0-only OR GPL-3.0-only`, at the recipient's option
- Distributed form: built from unmodified upstream source inside the image build
  (multi-stage), for `linux/amd64` and `linux/arm64`.
- Upstream: <https://thekelleys.org.uk/dnsmasq/>
- Corresponding source: `https://thekelleys.org.uk/dnsmasq/dnsmasq-<VERSION>.tar.gz`
- Source archive SHA-256: recorded in the build provenance output; verified by CI.
- License texts: [`third_party/licenses/dnsmasq-COPYING`](third_party/licenses/dnsmasq-COPYING)
  and [`LICENSE`](LICENSE)

### Base image and OS packages

The image is built on a pinned digest of a base distribution image. OS packages
installed into the final stage (`iproute2`, `nftables`, `ca-certificates`, `tzdata`,
and runtime libraries) retain their own upstream licenses. The base image reference
and the installed package manifest are emitted into the SBOM for every release
(see below), which is the authoritative list for the OS layer.

---

## Go dependencies (statically linked into the OpenSurge binary)

### gopkg.in/yaml.v3

- Version: `3.0.1`
- License: MIT and Apache-2.0, according to the upstream per-file notice
- Upstream: <https://github.com/go-yaml/yaml/tree/v3.0.1>
- License texts: [`third_party/licenses/yaml-v3-LICENSE`](third_party/licenses/yaml-v3-LICENSE)
  and [`third_party/licenses/Apache-2.0.txt`](third_party/licenses/Apache-2.0.txt)

### github.com/metacubex/bbolt

- Version: `v0.0.0-20260706163408-d4ec34ad7c48`, matching the pinned Mihomo cache
  implementation
- Use: read-only recovery of device selector choices from the core's cache
- License: MIT
- Upstream: <https://github.com/metacubex/bbolt/tree/d4ec34ad7c48>
- License text: [`third_party/licenses/bbolt-MIT.txt`](third_party/licenses/bbolt-MIT.txt)

### Other Go modules

The complete, authoritative list of Go module dependencies and their licenses for
any given release is produced by `go-licenses` during CI and attached to the
release as `licenses-go.csv` / part of the SBOM. `go.mod` pins every dependency;
release builds do **not** float dependencies to `latest`.

---

## Frontend dependencies (bundled into the Web UI)

### React, React DOM, and scheduler

- Versions: React `19.2.7`, React DOM `19.2.7`, scheduler `0.27.0`
- License: MIT
- Upstream: <https://github.com/facebook/react>
- License text: [`third_party/licenses/react-MIT.txt`](third_party/licenses/react-MIT.txt)

The complete frontend dependency list for a release is produced by the frontend
build (license report artifact) and attached to the release.

---

## Generated and verified in CI

To guarantee that this file never drifts from what is actually shipped:

1. **Build args are the source of truth.** `docker/Dockerfile` pins
   `MIHOMO_VERSION` and `DNSMASQ_VERSION` as `ARG`s. Release builds never use
   `latest`.
2. **Version emission.** The build records the resolved versions, source URLs and
   source archive checksums into the build provenance / SBOM artifact.
3. **Verification.** `make verify-third-party-notices` (run in CI on every PR and
   every release) fails if the values asserted here do not match the build
   provenance.
4. **Corresponding source.** For every GPL program shipped in the image, the
   release records the exact upstream source URL and checksum so recipients can
   obtain the corresponding source, as required by GPL-3.0 §6.

---

## Upstream attribution

This project is a derivative work of
[OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) by YTwsy, licensed
`GPL-3.0-only`. See [NOTICE.md](NOTICE.md) and [docs/UPSTREAM.md](docs/UPSTREAM.md).
Upstream copyright notices and the `LICENSE` file are preserved unchanged.
