# QNAP Docker deployment

This directory is the supported QNAP/Container Station deployment entrypoint for OpenSurge for QNAP.

- Chinese guide: [README.zh-CN.md](README.zh-CN.md)
- Persistence model: [PERSISTENCE.md](PERSISTENCE.md)

## Supported topology

The initial stable deployment is IPv4 same-LAN manual-gateway mode:

```text
Main router           192.168.2.1
QNAP NAS              192.168.2.240
OpenSurge container   192.168.2.241
Client                192.168.2.100
  gateway             192.168.2.241
  DNS                 192.168.2.241
```

The container gets its own LAN IPv4 through QNAP `qnet`. It does not use host networking and does not require `privileged: true`.

## Dual-NIC QNAP NAS

Two interface names exist in the deployment and they are intentionally different concepts:

- `OPENSURGE_PARENT_INTERFACE`: **QNAP host-side QNET parent**. This is the actual dual-NIC selector. Use `eth0`, `eth1`, `br0`, `bond0`, etc. only after checking QNAP Network & Virtual Switch.
- `OPENSURGE_CONTAINER_INTERFACE`: interface visible inside the container. In the supported single-QNET-network topology this is normally `eth0`, even if the QNAP parent is `eth1` or `br0`.

List host candidates before deployment:

```sh
cd deploy/qnap
sh ./preflight.sh --list-interfaces
```

or:

```sh
make qnap-interfaces
```

Do not infer the physical port from the `eth0`/`eth1` number alone.

## Persistent storage

Use one dedicated absolute QNAP host path for the complete `/data` tree, for example:

```env
OPENSURGE_DATA_PATH=/share/Container/opensurge
```

Compose maps:

```text
/share/Container/opensurge -> /data
```

Persisting the whole tree is deliberate. `/data/runtime` contains the ownership/reconciliation journal needed to handle an in-place container recreation safely.

Important directories:

| Path | Purpose |
| --- | --- |
| `/data/config` | Main gateway configuration |
| `/data/control` | Admin credentials and internal control state |
| `/data/profiles` | Imported/managed profiles |
| `/data/providers` | Provider/rule-provider data |
| `/data/state` | Durable optional-feature state |
| `/data/backups` | Configuration backups |
| `/data/runtime` | Runtime journal, applied-state ownership and reconciliation data |
| `/data/logs` | Component logs |

For an in-place update on the same NAS, preserve the complete `/data` tree. For migration to a different NAS, restore user/configuration data but remove stale `runtime/` before the first start on the new host. See [PERSISTENCE.md](PERSISTENCE.md).

## Prepare `.env`

```sh
cd deploy/qnap
cp .env.example .env
```

Set at least:

```env
OPENSURGE_IP=192.168.2.241
OPENSURGE_SUBNET=192.168.2.0/24
OPENSURGE_GATEWAY=192.168.2.1

# QNAP host-side NIC / bridge selector
OPENSURGE_PARENT_INTERFACE=eth1

# Container-side interface; normally eth0
OPENSURGE_CONTAINER_INTERFACE=eth0

# Dedicated absolute QNAP bind-mount path
OPENSURGE_DATA_PATH=/share/Container/opensurge
```

The static container IP should be outside the router's dynamic DHCP pool or reserved on the router.

## Preflight

List interfaces first on a multi-NIC NAS:

```sh
sh ./preflight.sh --list-interfaces
```

Then run the full deployment gate:

```sh
sh ./preflight.sh
```

It validates:

- container IPv4 / subnet / gateway consistency;
- Docker and Compose V2;
- Compose interpolation/schema;
- absolute dedicated `/data` bind path;
- `/dev/net/tun`;
- selected QNET parent-interface existence;
- host route information to the configured gateway;
- persistent-data path writability;
- obvious static-IP conflicts;
- qnet plugin reporting when available.

QNET plugin enumeration is advisory because QNAP versions expose third-party network drivers differently. Actual Compose network creation is authoritative.

CI/static validation:

```sh
sh ./preflight.sh --env-file .env.example --static
```

## Compose

Use the repository file directly:

```text
deploy/qnap/docker-compose.yml
```

It provides:

- QNET static LAN IP;
- explicit QNAP parent-NIC selection;
- one persistent `/data` bind mount;
- first-run config seeding from `OPENSURGE_IP`, `OPENSURGE_SUBNET`, `OPENSURGE_GATEWAY` and `OPENSURGE_CONTAINER_INTERFACE`;
- `NET_ADMIN` + `NET_RAW` + `/dev/net/tun` only;
- container sysctls for IPv4 forwarding/rp_filter;
- `no-new-privileges`;
- bounded Docker log rotation;
- no Docker socket, no host network, no default `privileged: true`.

Existing `/data/config/opensurge.yaml` is **never overwritten**. Deployment variables seed only the first configuration.

## Build and start

The current Compose builds from this repository; a stable registry image is not assumed yet.

```sh
git clone https://github.com/zyk1172/OpenSurge-for-QNAP.git
cd OpenSurge-for-QNAP/deploy/qnap
cp .env.example .env
# edit .env
mkdir -p /share/Container/opensurge
sh ./preflight.sh
docker compose --env-file .env -f docker-compose.yml up -d --build
```

Verify:

```sh
docker compose --env-file .env -f docker-compose.yml ps
docker inspect --format '{{json .State.Health}}' opensurge
docker logs --tail 100 opensurge
```

Then open:

```text
http://<OPENSURGE_IP>:8080
```

Create the first administrator account and verify the detected container-side interface, LAN IP, subnet and upstream gateway.

## Test one client first

Do not migrate the whole LAN immediately. Pick one client and set:

```text
IPv4 gateway = OPENSURGE_IP
DNS          = OPENSURGE_IP
```

Verify DNS, DIRECT, PROXY, UDP and long-lived traffic before moving more clients.

## Upgrade

Keep `OPENSURGE_DATA_PATH` unchanged:

```sh
docker compose --env-file .env -f docker-compose.yml down
docker compose --env-file .env -f docker-compose.yml up -d --build
```

The replacement container gets a new network namespace. The persisted runtime journal lets OpenSurge classify old state as interrupted instead of signalling unrelated reused PIDs.

## Recovery

If the gateway is unhealthy, first return test clients to the main router for gateway/DNS. Then inspect:

```sh
docker logs --tail 200 opensurge

docker exec opensurge \
  omg status --config /data/config/opensurge.yaml --format json

docker exec opensurge \
  omg doctor --config /data/config/opensurge.yaml
```

If an interrupted runtime exists:

```sh
docker exec opensurge \
  omg stop --config /data/config/opensurge.yaml
```

## Current boundaries

- IPv4 same-LAN manual-gateway mode only.
- No automatic QNAP Network & Virtual Switch mutation.
- No full-LAN DHCP takeover in the initial stable release.
- No downstream IPv6 takeover.
- QNET parent-NIC selection is a deployment parameter; the Web UI is not given Docker-socket or QNAP-host network-control access.
