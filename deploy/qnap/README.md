# OpenSurge for QNAP — Docker deployment

The supported QNAP deployment runs one `opensurge` container. The QNET parent, static IPv4, LAN CIDR, upstream router, and persistent path are fixed when the container is created; the running Web UI manages OpenSurge itself and does not modify QTS host networking.

The current stabilization target is **IPv4 same-LAN manual gateway**: main-router DHCP remains unchanged, and only selected clients use OpenSurge as their IPv4 gateway and DNS server.

## Architecture

```text
QNAP NIC / Virtual Switch
          │
          │ qnet (bound at container creation)
          ▼
┌───────────────────────────────┐
│ opensurge                     │
│ Web :8080                     │
│ Control API :61767 loopback   │
│ mihomo + dnsmasq              │
│ TUN + policy routing          │
│ persistent /data              │
└───────────────────────────────┘
          │
          ▼
LAN clients use OpenSurge IP as gateway/DNS
```

The default deployment has no Manager/Orchestrator sidecars, no Docker socket, no `privileged: true`, and no runtime QTS/Virtual Switch mutation.

The Gateway container requires:

- `NET_ADMIN`;
- `NET_RAW`;
- `/dev/net/tun`.

### Same-LAN data plane

For QNAP kernels with TUN and policy routing but no `nf_tables` netlink support, OpenSurge uses:

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

This same-LAN path does not depend on nftables. Isolated downstream topologies that need NAT retain the nftables/fwmark backend and strict capability checks.

## Prebuilt test image

Do not build the project on the NAS.

Rolling test release:

<https://github.com/zyk1172/OpenSurge-for-QNAP/releases/tag/qnap-test-latest>

TS-264C / x86_64 users should download:

```text
OpenSurge-for-QNAP-test-amd64.tar.gz
```

Load it with:

```sh
gzip -dc OpenSurge-for-QNAP-test-amd64.tar.gz | docker load
```

The image tag is:

```text
opensurge-for-qnap:test
```

The default Compose uses `pull_policy: never`, so a missing local test image fails explicitly instead of silently pulling a different image.

## Creation-time parameters

| Variable | Purpose | Example |
|---|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET parent NIC / Virtual Switch | `eth1` / `br0` |
| `OPENSURGE_IP` | OpenSurge LAN IPv4 | `192.168.2.241` |
| `OPENSURGE_SUBNET` | LAN CIDR | `192.168.2.0/24` |
| `OPENSURGE_GATEWAY` | Upstream router IPv4 | `192.168.2.1` |
| `OPENSURGE_DATA_PATH` | Persistent QNAP path | `/share/Container/opensurge` |

Do not infer the physical port from an `eth0`/`eth1` number alone. Check QTS Network & Virtual Switch together with host `ip addr` / `ip route` output.

The container-side data interface is normally still `eth0`; it is a different namespace from the selected QNAP host-side NIC/bridge/bond.

## Compose

Use:

```text
deploy/qnap/docker-compose.yml
```

It defines one service:

```text
opensurge
```

Provide creation-time values through the installer, Codex/Hermes, Container Station, or the shell environment. Example:

```sh
export OPENSURGE_PARENT_INTERFACE=br0
export OPENSURGE_IP=192.168.2.241
export OPENSURGE_SUBNET=192.168.2.0/24
export OPENSURGE_GATEWAY=192.168.2.1
export OPENSURGE_DATA_PATH=/share/Container/opensurge

docker compose -f docker-compose.yml config
docker compose -f docker-compose.yml up -d
```

Do not run `docker compose build` on the NAS for the normal test deployment.

## First start and persistence

If this file does not exist:

```text
/data/config/opensurge.yaml
```

the entrypoint seeds the initial configuration from creation-time values. An existing persistent configuration is not overwritten by later seed values.

Therefore:

- preserve `/data` when recreating/updating the container on the same NAS;
- change parent NIC/IP/CIDR/upstream router as container-creation parameters and recreate the container;
- manage subscriptions, policies, DNS, TUN, providers, and devices in the Web UI.

Recommended host bind mount:

```text
/share/Container/opensurge -> /data
```

Important directories:

| Path | Purpose |
|---|---|
| `/data/config` | Main configuration |
| `/data/control` | Admin/control state |
| `/data/profiles` | Imported/managed profiles |
| `/data/providers` | Provider data |
| `/data/state` | Durable feature state |
| `/data/backups` | Configuration backups |
| `/data/runtime` | Ownership/recovery journal |
| `/data/logs` | Component logs |

## Web UI

Open:

```text
http://<OpenSurge-IP>:8080
```

Create the first administrator account, then use the QNAP Web surface to manage runtime settings and proxy configuration. Container-creation network values are shown as deployment state rather than mutable QTS controls.

The QNAP build exposes NAS-relevant functions only; desktop-host controls are not presented as QNAP features.

## Basic validation

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
docker exec opensurge ls -l /dev/net/tun
```

For the same-LAN nft-free data plane after Gateway start:

```sh
docker exec opensurge ip rule show
docker exec opensurge ip route show table 20241
```

A QNAP kernel may legitimately reject `nft list ruleset`; that is not a same-LAN failure when OpenSurge is using the TUN ingress-interface backend.

## Test one client first

Do not migrate the whole LAN immediately. Pick one client and set:

```text
IPv4 gateway = OpenSurge IP
DNS          = OpenSurge IP
```

Verify DNS, DIRECT, PROXY, UDP/QUIC, long-lived traffic, container restart recovery, and then NAS reboot recovery before moving more clients.

## Current boundaries

- IPv4 same-LAN manual-gateway mode is the first stable target.
- No automatic main-router DHCP takeover.
- No downstream IPv6 takeover.
- No automatic QNAP Network & Virtual Switch mutation.
- Changing QNET parent/static IP/CIDR/upstream router requires container recreation.
- QNET/kernel behavior still needs coverage across QNAP models and firmware versions.
- Real-client, reboot, 24h/72h soak, and update/rollback validation remain stable-release gates.
