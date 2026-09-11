# OpenSurge for QNAP — Docker deployment

The supported QNAP deployment runs one `opensurge` container. The QNET parent, static IPv4, LAN CIDR, upstream router, persistent path, and the unprivileged Web identity are fixed when the container is created; the running Web UI manages OpenSurge itself and does not modify QTS host networking.

The primary stabilization target remains **IPv4 same-LAN manual gateway**: main-router DHCP remains unchanged, and only selected clients use OpenSurge as their IPv4 gateway and DNS server.

QNAP same-LAN also has an experimental **IPv6 DNS / fake-IP steering** mode. Clients keep the public IPv6 address and IPv6 default route provided by the main router; the router sends only Mihomo's fake IPv6 prefix to OpenSurge through a static route. This is not a whole-LAN IPv6 default-router takeover.

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
```

The default deployment has no Manager/Orchestrator sidecars, no Docker socket, no `privileged: true`, and no runtime QTS/Virtual Switch mutation.

The Gateway container requires `NET_ADMIN`, `NET_RAW`, and `/dev/net/tun`.

### Same-LAN IPv4 data plane

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

This IPv4 manual-gateway path does not depend on nftables. Isolated downstream topologies that need NAT retain the nftables/fwmark backend and strict capability checks.

### Same-LAN IPv6 directed path

With IPv6 DNS/fake-IP steering enabled:

```text
normal IPv6: client ──► main router ──► Internet

DNS: client ──► main-router DNS ──► OpenSurge DNS
                                    │
                                    └─ fake AAAA = fdfe:dcba:9876::/64
                                                   │
                                         main-router IPv6 static route
                                                   ▼
                                               OpenSurge
                                                   ▼
                                                  tun0
```

OpenSurge no longer installs a broad same-LAN IPv6 ingress/default route. Only `fdfe:dcba:9876::/64` is steered into the TUN. The main router keeps its existing RA, DHCPv6, bridge/passthrough, public IPv6, and client default-router behavior.

See [`../../docs/QNAP_IPV6_DNS_FAKEIP.md`](../../docs/QNAP_IPV6_DNS_FAKEIP.md).

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

The image tag is `opensurge-for-qnap:test`. The default Compose uses `pull_policy: never`, so a missing local image fails explicitly instead of silently pulling another image.

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

Do not infer the physical port from an `eth0`/`eth1` number alone. Check QTS Network & Virtual Switch together with host `ip addr` / `ip route` output. The container-side data interface is normally still `eth0` and is in a different namespace from the selected QNAP host-side NIC/bridge/bond.

### QNAP UID/GID is configurable

Verify the NAS account allowed to use the Container share:

```sh
id <username>
```

Use the numeric UID/GID in `.env`. Do not solve permission failures with recursive `chmod 777` or by blindly chowning the whole OpenSurge data tree. Only `/data/web-auth` needs the unprivileged Web identity; privileged Control owns gateway/runtime state.

## Run the real QNAP preflight first

From `deploy/qnap`:

```sh
cp .env.example .env
# edit .env
sh ./preflight.sh
```

The preflight verifies Docker, Compose, qnet, parent topology, `/dev/net/tun`, the real image and bind mount, durable `/data` filesystem operations, and the configured unprivileged Web identity.

## Compose

Use `deploy/qnap/docker-compose.yml`. It defines one service: `opensurge`.

Example:

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

Do not run `docker compose build` on the NAS for the normal test deployment.

### IPv6 sysctls

The container enables IPv6 forwarding and also keeps:

```text
net.ipv6.conf.eth0.accept_ra=2
net.ipv6.conf.eth0.autoconf=1
```

Linux may otherwise stop accepting RA when forwarding is enabled. The DNS/fake-IP design deliberately preserves the upstream/native IPv6 path, so OpenSurge must still learn the main router's IPv6 route/prefix. These sysctls affect only the OpenSurge container namespace, not QTS.

## First start and persistence

If `/data/config/opensurge.yaml` does not exist, the entrypoint seeds the initial configuration from creation-time values. Existing persistent configuration is never overwritten by later seed values.

The entrypoint also derives a stable link-local IPv6 next hop from the container's fixed IPv4. Example:

```text
192.168.2.241 → fe80::1:0:c0a8:2f1
```

This is the next hop used by the main router for the `fdfe:dcba:9876::/64` static route, avoiding breakage when a recreated container receives a different kernel-generated link-local address.

Recommended host bind mount:

```text
/share/Container/opensurge -> /data
```

Important directories include `/data/config`, `/data/control`, `/data/web-auth`, `/data/profiles`, `/data/providers`, `/data/state`, `/data/backups`, `/data/runtime`, and `/data/logs`.

See [`PERSISTENCE.md`](PERSISTENCE.md).

## Web UI

Open:

```text
http://<OpenSurge-IP>:8080
```

The QNAP Web surface exposes NAS-relevant controls only. It shows container-creation network values as deployment state, manages DNS/TUN/proxy settings, subscriptions, policies and devices, and provides Doctor/log/lifecycle views.

The Network page also manages IPv6 DNS/fake-IP steering and displays the main-router DNS upstream, fake IPv6 prefix, stable link-local next hop, and LAN interface to configure.

The QNAP Doctor checks iproute2, `/dev/net/tun`, interfaces, persistent filesystem semantics, and for same-LAN IPv6 also verifies forwarding, `accept_ra=2`, and the deterministic link-local next hop.

## Optional IPv6 DNS / fake-IP setup

In QNAP Web → Network → **IPv6 DNS-directed steering**:

1. enable the feature;
2. note the displayed OpenSurge DNS IPv4, `fdfe:dcba:9876::/64`, stable link-local next hop, and LAN/bridge interface;
3. point the DNS upstream used by the main router for clients to OpenSurge;
4. add an IPv6 static route on the main router for `fdfe:dcba:9876::/64` via the displayed OpenSurge link-local address;
5. **do not disable the main router's RA and do not route `::/0` to OpenSurge**;
6. confirm the DNS/static-route readiness checkbox in Web;
7. save and restart the Gateway.

Mihomo currently uses unified fake-IP DNS, so A queries may still return IPv4 fake IPs. This feature intentionally does not add or modify IPv4 routes. If OpenSurge DNS is advertised globally to clients that do not use the existing OpenSurge IPv4 data plane, ensure the existing IPv4 fake-IP path is reachable or apply an appropriate DNS policy on the main router.

## Basic validation

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
docker exec opensurge ls -l /dev/net/tun
```

For IPv4 same-LAN after Gateway start:

```sh
docker exec opensurge ip rule show
docker exec opensurge ip route show table 20241
```

When IPv6 DNS/fake-IP steering is enabled:

```sh
docker exec opensurge cat /proc/sys/net/ipv6/conf/eth0/accept_ra
docker exec opensurge ip -6 addr show dev eth0
docker exec opensurge ip -6 rule show
docker exec opensurge ip -6 route show table 20241
```

The IPv6 selector should target only `fdfe:dcba:9876::/64`, not all packets arriving on `eth0`.

## Test clients

For the IPv4 main path, start with one client:

```text
IPv4 gateway = OpenSurge IP
DNS          = OpenSurge IP
```

Verify DNS, DIRECT, PROXY, UDP/QUIC, long-lived traffic, container restart, and NAS reboot.

IPv6 DNS/fake-IP mode needs no per-client IPv6 configuration. Verify that the client still has its original public IPv6/default router, fake AAAA traffic reaches OpenSurge, and ordinary IPv6 is not globally redirected.

## Current boundaries

- IPv4 same-LAN manual-gateway remains the first stable target.
- No automatic main-router DHCP takeover.
- Optional IPv6 steering handles only `fdfe:dcba:9876::/64`; there is no whole-LAN IPv6 default-route takeover.
- OpenSurge does not advertise same-LAN RA/SLAAC.
- The QNAP Linux TUN IPv6 path does not guarantee precise per-device identity.
- No automatic QNAP Network & Virtual Switch mutation.
- Changing QNET parent/static IP/CIDR/upstream router requires container recreation.
- QNET/kernel/ACL behavior still needs broader QNAP hardware/firmware validation.
- ARM64 is buildable but still needs broader real-QNAP validation.
- Real-client, reboot, 24h/72h soak, and update/rollback validation remain stable-release gates.
