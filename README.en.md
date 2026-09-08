# OpenSurge for QNAP

> **Status: Phase 1 / Linux data-plane port in progress. No production QNAP Docker image has been released yet.**

OpenSurge for QNAP is a derivative of
[OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac). Its goal is to
reuse the upstream Web control plane, mihomo configuration/policy features, and
gateway lifecycle while replacing the macOS runtime with a product-grade Linux
backend suitable for QNAP NAS and generic Linux Docker deployments.

This is not an official QNAP edition from the upstream author and is not
endorsed by the upstream project unless explicitly stated otherwise.

- Original project: OpenSurge for Mac
- Original author / organization: YTwsy
- Upstream repository: <https://github.com/YTwsy/OpenSurge-for-Mac>
- Fork baseline: `b03bf2f8a2b02a6fffba9c879ce1980ddb831a67` (v0.2.2)
- Fork date: 2026-09-09
- License: `GPL-3.0-only`, inherited unchanged
- Upstream relationship: [docs/UPSTREAM.md](docs/UPSTREAM.md)
- Attribution/license notes: [NOTICE.md](NOTICE.md), [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)

## Product target

The intended stable topology is:

```text
LAN client
   │ Gateway / DNS = OpenSurge container
   ▼
QNAP / Linux
└─ OpenSurge container
   ├─ Web UI / Control API
   ├─ mihomo TUN
   ├─ DNS / dnsmasq
   ├─ nftables (OpenSurge-owned table only)
   └─ iproute2 policy routing
          ├─ DIRECT
          └─ PROXY
```

The first stable mode will prioritize **same-LAN manual gateway** operation:
main-router DHCP stays enabled and selected clients manually use OpenSurge as
their IPv4 gateway and DNS server. Whole-LAN DHCP takeover and downstream IPv6
takeover are deliberately out of scope for the first stable release.

## Implemented in Phase 1

The current PR/branch provides or is validating the following foundation:

- preserved upstream attribution, Git history and `GPL-3.0-only` licensing;
- removed the macOS menu-bar app, PKG/notarization, launchd helper, pf and macOS
  BPF IPv6 runtime;
- added `platform.NetworkBackend` to isolate business lifecycle logic from Linux
  host commands;
- Linux IPv4 backend based on `nftables + iproute2 + /dev/net/tun`;
- explicit rejection of mihomo `auto-route` on the QNAP/Linux path so OpenSurge
  remains the single routing owner;
- nftables operations scoped to an OpenSurge-owned table; global `nft flush
  ruleset` is forbidden;
- preflight collision checks for nft table, fwmark, routing table and rule
  priority; startup fails when ownership cannot be proven;
- structured `ip -j` policy-rule inspection and exact route/rule deletion rather
  than flushing an entire routing table;
- persisted NAT/routing cleanup recipes independent of process-local backend
  memory;
- fresh-process Stop/rollback/interrupted recovery paths;
- write-ahead cleanup journaling to close the crash window between a successful
  kernel mutation and the next state-file write;
- snapshots bound to the Linux network namespace so an isolated container
  restart never replays old cleanup into a fresh namespace;
- atomic state writes with file and parent-directory `fsync`;
- explicit `Normalize -> Validate` configuration flow for derived LAN CIDR and
  policy-rule priority values;
- initial Linux/QNAP lifecycle regression tests and a minimal GitHub Actions Go
  CI workflow.

## Not implemented yet

Phase 1 is **not a production release**. The following remain pending:

- production `Dockerfile`, Compose, and QNAP Container Station deployment;
- remote Web authentication, sessions, CSRF and login rate limiting;
- full removal/replacement of macOS-specific Web UI wording and settings;
- watchdog, bounded restart and full reconciliation state machine;
- three-namespace Linux lab (`client -> gateway -> upstream`);
- real QNAP validation, NAS reboot tests, and 24h/72h soak tests;
- Docker healthcheck, diagnostic bundle, complete log rotation and secret
  redaction;
- release SBOM/provenance and automated third-party-license verification;
- DHCP takeover;
- downstream IPv6 takeover.

No `stable` tag should be published before those product gates are met.

## Network safety rules

1. Never flush the host firewall globally.
2. Uncommon IDs do not imply ownership; nft table, fwmark, route table and rule
   priority must be conflict-checked.
3. Stop/recovery must work from persisted state in a fresh process.
4. Cleanup must be exact; do not flush a full policy-routing table.
5. Invalid configuration must not destroy the previous usable network state.
6. Linux network namespace identity is a recovery boundary.

## Development and validation

Ordinary tests do not intentionally modify host networking:

```bash
go test ./...
go vet ./...
```

Real Linux network tests are opt-in and should run only inside a disposable
container/network namespace:

```bash
OPEN_SURGE_NETWORK_TESTS=1 go test ./internal/platform/linux/
```

Do not run high-risk integration tests in the namespace currently carrying the
NAS management network or a household gateway.

## Roadmap

### Phase 1 — Linux data-plane foundation

Platform boundary, ownership enforcement, transactional recovery, configuration
migration guards, and regression tests.

### Phase 2 — Docker and Web productization

Production Docker/Compose packaging, persistent layout, LAN Web authentication,
health checks, diagnostics, and a Linux network-namespace lab.

### Phase 3 — QNAP stability validation

Container Station / Virtual Switch topology verification, fault injection, NAS
reboot tests, 24h/72h soak testing, performance and log-capacity validation.

### Phase 4 — Optional extensions

Only after the stable baseline: DHCP takeover, IPv6 takeover, and broader Linux
NAS targets.

## Upstream synchronization

The fork does not automatically merge upstream macOS runtime changes. Prefer
manual cherry-picks for platform-neutral fixes such as:

- mihomo profile/provider/policy logic;
- Web UI improvements;
- configuration parsing and device policies;
- tests and documentation.

See [docs/UPSTREAM.md](docs/UPSTREAM.md) for the detailed policy.

## License

This project remains **GPL-3.0-only**. Upstream copyright history, `LICENSE`, and
third-party notices must remain intact. Future Docker releases must also provide
corresponding-source information for GPL-covered binaries and ensure the SBOM,
notices and actual build contents agree.
