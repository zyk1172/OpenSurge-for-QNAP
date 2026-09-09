# QNAP Docker deployment

This directory is the supported QNAP/Container Station deployment entrypoint for OpenSurge for QNAP.

## Topology

OpenSurge runs as a Docker container with its own LAN IPv4 address through QNAP's `qnet` network driver.
It is intentionally **not** deployed with host networking and does not require `privileged: true`.

Example:

```text
Main router           192.168.2.1
QNAP NAS              192.168.2.240
OpenSurge container   192.168.2.241
Client                192.168.2.100
  gateway             192.168.2.241
  DNS                 192.168.2.241
```

The v1 deployment is IPv4 same-LAN bypass-router mode. DHCP takeover and downstream IPv6 takeover are out of scope.

## 1. Prepare the environment

Copy the example file and edit every network value:

```sh
cd deploy/qnap
cp .env.example .env
```

Required values:

- `OPENSURGE_IP`: free static LAN address for the container; keep it outside the router's dynamic DHCP pool or reserve it.
- `OPENSURGE_SUBNET`: LAN CIDR, for example `192.168.2.0/24`.
- `OPENSURGE_GATEWAY`: main router IPv4 address.
- `OPENSURGE_PARENT_INTERFACE`: QNAP interface used by the target LAN. Verify it on the NAS; common systems may expose `eth*`, `bond*`, `br*`, or a virtual-switch interface.
- `OPENSURGE_DATA_PATH`: persistent host directory for `/data`.

Do not copy the example network values blindly.

## 2. Run the deployment preflight

Before creating the application:

```sh
sh ./preflight.sh
```

The full preflight validates:

- IPv4 address/subnet/gateway consistency;
- Docker and Compose V2 availability;
- Compose interpolation/schema;
- Docker daemon reachability;
- `/dev/net/tun`;
- QNAP parent-interface existence when `ip` or `ifconfig` is available;
- persistent-data path writability;
- obvious static-IP conflicts using ping when available;
- whether Docker advertises the `qnet` network driver.

`qnet` plugin reporting is advisory because QNAP builds do not expose third-party network drivers identically. The actual Compose application creation remains the authoritative driver check.

For CI or syntax-only validation:

```sh
sh ./preflight.sh --env-file .env.example --static
```

## 3. Create the application

From SSH:

```sh
docker compose --env-file .env -f docker-compose.yml up -d --build
```

Or create an Application in Container Station using the same Compose file and values.

The container receives only the network privileges required by the v1 data plane:

- `CAP_NET_ADMIN`
- `CAP_NET_RAW`
- `/dev/net/tun`
- container sysctls for IPv4 forwarding and reverse-path filtering

The Compose definition also enables `no-new-privileges` and bounded Docker log rotation. It does not use host networking or `privileged: true`.

## 4. Verify startup

```sh
docker compose --env-file .env -f docker-compose.yml ps
docker inspect --format '{{json .State.Health}}' opensurge
docker logs --tail 100 opensurge
```

Then open:

```text
http://<OPENSURGE_IP>:8080
```

Create the first administrator account in the Web UI before configuring the gateway.

## 5. Configure a test client first

Do not change the whole LAN at once. Pick one client and set:

```text
IPv4 address: normal LAN address
Gateway:      OPENSURGE_IP
DNS:          OPENSURGE_IP
```

Verify Web access, DNS resolution, DIRECT traffic and proxied traffic before migrating more devices.

## 6. Upgrade

Keep `/data` persistent. Before an upgrade, back up `OPENSURGE_DATA_PATH` with the gateway stopped or by using a filesystem/storage snapshot that gives a consistent view.

Then rebuild/recreate the container while preserving the same data directory:

```sh
docker compose --env-file .env -f docker-compose.yml down
docker compose --env-file .env -f docker-compose.yml up -d --build
```

Container recreation creates a new network namespace. OpenSurge records runtime ownership and treats persisted runtime from the previous namespace as interrupted state instead of signalling unrelated reused PIDs.

## 7. Recovery

If the gateway is unhealthy:

1. Point test clients back to the main router for gateway/DNS.
2. Run `docker logs --tail 200 opensurge`.
3. Run `docker exec opensurge omg status --config /data/config/opensurge.yaml --format json`.
4. Run `docker exec opensurge omg doctor --config /data/config/opensurge.yaml`.
5. If a stale/interrupted runtime exists, run `docker exec opensurge omg stop --config /data/config/opensurge.yaml` before recreating the container.

OpenSurge owns only its own nftables table/policy-routing state and must never flush the host ruleset.

## Known v1 boundaries

- IPv4 same-LAN bypass-router mode only.
- No automatic QNAP Network & Virtual Switch reconfiguration.
- No full-LAN DHCP takeover in the initial QNAP release.
- No downstream IPv6 takeover; clients must not be allowed to bypass the gateway over unmanaged IPv6 when strict whole-device routing is required.
