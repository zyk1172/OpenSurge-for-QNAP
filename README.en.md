# OpenSurge for QNAP

OpenSurge for QNAP is a single-container transparent proxy gateway for QNAP NAS. It combines the Web control plane, mihomo, DNS, TUN, policy routing, and persistent recovery in one Docker container with its own LAN IPv4 through QNAP QNET.

The project is currently in **real-QNAP stabilization / test-image** stage. The default target is IPv4 same-LAN manual-gateway mode: the main router keeps DHCP enabled, while selected clients point their IPv4 gateway and DNS to OpenSurge.

> The current rolling image is for testing only and is not a stable release.

## Architecture

```text
LAN client
 gateway / DNS = OpenSurge IP
        │
        ▼
QNAP QNET LAN IP
┌──────────────────────────────┐
│ opensurge                    │
│                              │
│ Web UI :8080                 │
│ Control API (loopback)       │
│ mihomo                       │
│ dnsmasq                      │
│ TUN                          │
│ iproute2 policy routing      │
│ persistent /data             │
└──────────────────────────────┘
        │
        ├─ DIRECT
        └─ PROXY
```

The default deployment contains one product container only:

```text
opensurge
```

There is no Manager/Orchestrator pair and the LAN-facing Web UI is not given the Docker socket.

### Same-LAN QNAP data plane

For QNAP kernels that provide TUN and Linux policy routing but not `nf_tables`, OpenSurge uses ingress-interface policy routing:

```text
eth0 ingress
   ↓
ip rule iif eth0
   ↓
OpenSurge dedicated routing table
   ↓
tun0
   ↓
mihomo
```

The supported same-LAN QNET mode therefore **does not require nftables**. Both TCP and UDP remain on the TUN data plane.

An nftables/fwmark backend is retained for isolated downstream topologies that require NAT, with strict capability and ownership checks.

## Test image

Rolling test release:

<https://github.com/zyk1172/OpenSurge-for-QNAP/releases/tag/qnap-test-latest>

QNAP TS-264C / x86_64 users should use:

```text
OpenSurge-for-QNAP-test-amd64.tar.gz
```

Load it with:

```sh
gzip -dc OpenSurge-for-QNAP-test-amd64.tar.gz | docker load
```

The loaded tag is:

```text
opensurge-for-qnap:test
```

The image is built by GitHub Actions. The NAS does not need Go, Node.js, pnpm, gcc, or project build dependencies and should not be used as the image build machine.

## QNAP deployment

- [Chinese deployment guide](deploy/qnap/README.zh-CN.md)
- [English deployment guide](deploy/qnap/README.md)
- [Persistence model](deploy/qnap/PERSISTENCE.md)
- [Default Compose](deploy/qnap/docker-compose.yml)

Creation-time parameters:

| Variable | Purpose |
|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET parent NIC / Virtual Switch |
| `OPENSURGE_IP` | OpenSurge LAN IPv4 |
| `OPENSURGE_SUBNET` | LAN CIDR |
| `OPENSURGE_GATEWAY` | Upstream router IPv4 |
| `OPENSURGE_DATA_PATH` | Persistent QNAP host path |

The physical NIC, QNET, static IP, CIDR and upstream gateway are container-creation parameters. The running Web UI does not modify QTS Network & Virtual Switch.

The container-side interface is normally `eth0`; it is not the same name as the selected QNAP host-side NIC, bridge, bond, or Virtual Switch.

## Web management

Open:

```text
http://<OpenSurge-IP>:8080
```

The QNAP Web surface currently provides:

- first-admin creation and authenticated login;
- gateway start/stop and interrupted-state recovery;
- actual container network status;
- mutable DNS/TUN runtime settings;
- HTTPS subscription and local YAML import;
- persistent draft, running, and next-start versions;
- providers, policy groups and rules;
- device policies;
- connection and traffic views;
- Doctor, logs, and lifecycle operation history.

The QNAP build exposes NAS-relevant controls only; desktop-only host controls are not presented as QNAP features.

## Persistence

Recommended bind mount:

```text
/share/Container/opensurge -> /data
```

Important directories:

```text
/data/config      main configuration
/data/control     admin/control state
/data/profiles    imported/managed profiles
/data/providers   provider data
/data/state       durable feature state
/data/backups     configuration backups
/data/runtime     ownership/crash-recovery journal
/data/logs        component logs
```

Preserve the full `/data` tree for updates and container recreation on the same NAS. An existing `/data/config/opensurge.yaml` is not overwritten by new first-run seed values.

## Real QNAP compatibility discovered so far

A QNAP 5.10.60-qnap x86_64 environment has confirmed:

- working TUN kernel support;
- working `/dev/net/tun` container mapping;
- `NET_ADMIN` / `NET_RAW` capability support;
- working `iproute2`;
- missing `nf_tables` netlink support on that kernel;
- working manual mihomo HTTP / SOCKS5 / DNS paths.

The project now includes nft-free ingress-interface TUN policy routing for this same-LAN QNAP class, plus dedicated TCP/UDP namespace CI.

Physical-client, reboot, and long-duration validation remain in progress.

## Network safety boundaries

1. Never run `nft flush ruleset`.
2. Never flush the host policy-routing table globally.
3. Do not require `privileged: true` by default.
4. Do not use host networking for the Gateway data plane.
5. Do not expose the Docker socket to the LAN-facing Web UI.
6. Kernel objects must be owned, snapshotted, journaled, and removed precisely.
7. A recreated container network namespace is a recovery boundary.
8. Normal QNAP Web operations do not change QTS default routes, DNS, DHCP, or Virtual Switch configuration.

## Current scope

First stable release priorities:

- single-container QNAP Docker deployment;
- QNET independent LAN IPv4;
- IPv4 same-LAN manual gateway;
- TUN transparent proxying;
- DNS;
- subscriptions/providers/policies/device management;
- persistent recovery across container recreation.

Not first-stable goals:

- automatic main-router DHCP takeover;
- downstream IPv6 takeover;
- automatic QNAP Network & Virtual Switch mutation;
- QPKG packaging;
- fully automatic household-network migration.

## Remaining stable gates

- real client TCP / UDP / QUIC end-to-end validation;
- QNAP reboot recovery;
- 24h / 72h soak testing;
- long-running CPU, memory, FD, and log-capacity checks;
- production update/rollback rehearsal;
- SBOM, provenance, checksums, and third-party-license verification;
- stable image publication workflow.

## Development

```sh
go test ./...
go vet ./...
```

Web:

```sh
cd web
pnpm install --frozen-lockfile
pnpm test
OPENSURGE_TARGET=qnap pnpm build
```

Linux namespace lab:

```sh
make lab-test-linux
```

Do not run high-risk network integration tests in the host namespace currently carrying NAS management or household-gateway traffic.

## Upstream and license

OpenSurge for QNAP is derived from [OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) and productized for QNAP/Linux. It is not an official QNAP edition from, or an endorsed release of, the upstream project.

- Upstream project: OpenSurge for Mac
- Upstream author / organization: YTwsy
- Fork baseline: `b03bf2f8a2b02a6fffba9c879ce1980ddb831a67` (v0.2.2)
- Fork date: 2026-09-09
- License: `GPL-3.0-only`
- Relationship details: [docs/UPSTREAM.md](docs/UPSTREAM.md)
- Attribution/notices: [NOTICE.md](NOTICE.md), [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)

Upstream copyright history, `LICENSE`, and third-party notices remain intact. Platform-neutral upstream fixes may be cherry-picked manually; macOS runtime changes are not automatically merged back into the QNAP default path.
