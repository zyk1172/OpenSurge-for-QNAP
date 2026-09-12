# OpenSurge for QNAP — Docker deployment

OpenSurge for QNAP `v1.0.x` supports **IPv4 same-LAN manual gateway** as its stable QNAP mode. Main-router DHCP remains unchanged; selected clients use OpenSurge as their IPv4 gateway and DNS server.

## Stable image

Docker Hub:

```text
zyk1172/opensurge-for-qnap:1.0.0
zyk1172/opensurge-for-qnap:latest
```

The default Compose is pinned to `1.0.0`:

```text
deploy/qnap/docker-compose.yml
```

Override it when needed:

```sh
export OPENSURGE_IMAGE=zyk1172/opensurge-for-qnap:1.0.0
```

GitHub Releases also publish amd64/arm64 Docker archives, SPDX JSON SBOMs, `SHA256SUMS`, build provenance and SBOM attestations.

## Architecture and default privilege boundary

```text
QNAP NIC / Virtual Switch
          │
          │ qnet
          ▼
┌───────────────────────────────┐
│ opensurge                     │
│ Web :8080                     │
│ Control API loopback          │
│ mihomo + dnsmasq              │
│ TUN + policy routing          │
│ persistent /data              │
└───────────────────────────────┘
          │
          ▼
LAN clients: Gateway/DNS = OpenSurge IP
```

The normal deployment has one container, no Docker socket, no `privileged: true`, and no QTS Network & Virtual Switch mutation.

Default capabilities/devices:

```text
NET_ADMIN
NET_RAW
/dev/net/tun
```

The default Compose does **not** grant `SYS_ADMIN` and does **not** mount the QNAP host network namespace.

## Creation-time parameters

| Variable | Purpose | Example |
|---|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET parent NIC / Virtual Switch | `eth1` / `br0` |
| `OPENSURGE_IP` | OpenSurge LAN IPv4 | `192.168.2.241` |
| `OPENSURGE_SUBNET` | LAN CIDR | `192.168.2.0/24` |
| `OPENSURGE_GATEWAY` | Upstream router IPv4 | `192.168.2.1` |
| `OPENSURGE_DATA_PATH` | Persistent QNAP path | `/share/Container/opensurge` |
| `OPENSURGE_WEB_UID` | Unprivileged Web UID | `1000` |
| `OPENSURGE_WEB_GID` | Unprivileged Web GID | `100` |

Do not infer a physical NIC from its `eth0/eth1` number alone. Verify it in QTS Network & Virtual Switch together with host `ip addr` / `ip route` output.

Verify the QNAP UID/GID instead of assuming it:

```sh
id <username>
```

Do not hide QTS ACL problems with recursive `chmod 777`.

## Preflight

From `deploy/qnap`:

```sh
cp .env.example .env
# edit .env
sh ./preflight.sh
```

The preflight checks Docker, Compose, qnet, `/dev/net/tun`, topology, the real `/share/...` bind mount, and Web UID/GID write semantics for `/data/web-auth`.

## Start the default LAN gateway

```sh
export OPENSURGE_PARENT_INTERFACE=br0
export OPENSURGE_IP=192.168.2.241
export OPENSURGE_SUBNET=192.168.2.0/24
export OPENSURGE_GATEWAY=192.168.2.1
export OPENSURGE_DATA_PATH=/share/Container/opensurge
export OPENSURGE_WEB_UID=1000
export OPENSURGE_WEB_GID=100

docker compose -f docker-compose.yml config
docker compose -f docker-compose.yml up -d
```

A normal deployment does not build the project on the NAS.

## Optional: route the NAS host through OpenSurge

NAS Host Takeover needs access to the QNAP host network namespace, so it is isolated in a separate high-privilege override:

```text
deploy/qnap/docker-compose.host-takeover.yml
```

Only enable it when required:

```sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.host-takeover.yml \
  up -d
```

The override adds only:

```text
CAP_SYS_ADMIN
/proc/1/ns/net:/run/opensurge/host-netns:ro
```

The Web UI can then enable **NAS Host Takeover**. OpenSurge does not replace the QTS default route; it installs an owned table `20242` and dynamically allocated RPDB priorities. Existing QTS rules using the NAS address, a CIDR containing it, or `from all` are considered when selecting the OpenSurge priority block. Conflicts or failed route verification cause a rollback instead of overwriting QTS state.

See [`../../docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md`](../../docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md).

## Persistence

Recommended bind mount:

```text
/share/Container/opensurge -> /data
```

If `/data/config/opensurge.yaml` does not exist, the entrypoint seeds it from creation-time network parameters. Existing persistent configuration is never overwritten by later seed values.

Preserve the complete `/data` tree when recreating or updating the container.

## Web UI

Open:

```text
http://<OpenSurge-IP>:8080
```

First-admin creation requires the one-time bootstrap token printed to the container log. The token is deleted after successful administrator creation.

The QNAP Web surface manages gateway lifecycle, DNS/TUN, subscriptions, providers, policies, device rules, connection/traffic analysis, diagnostics/logs and optional NAS Host Takeover.

## Same-LAN data plane

The QNAP same-LAN mode uses:

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

This path does not depend on nftables, so it works on supported QNAP kernels that have TUN and policy routing but lack `nf_tables` netlink support. Isolated downstream topologies that need NAT retain the nftables/fwmark backend.

## Basic validation

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
docker exec opensurge ls -l /dev/net/tun
```

For the same-LAN data plane:

```sh
docker exec opensurge ip rule show
docker exec opensurge ip route show table 20241
```

A failing `nft list ruleset` is not a same-LAN failure when the nft-free TUN backend is selected.

## Stable boundaries

- IPv4-only QNAP product boundary.
- No automatic main-router DHCP takeover.
- No downstream IPv6 takeover.
- No automatic QTS Network & Virtual Switch mutation.
- Changing QNET parent/static IP/CIDR/upstream router requires container recreation.
- ARM64 images are published, but QNET/kernel behavior can still differ between QNAP ARM models.
- A hard `SIGKILL` or host power loss cannot execute graceful Host Takeover cleanup; a short interruption may remain until the container restarts and reconciles.
