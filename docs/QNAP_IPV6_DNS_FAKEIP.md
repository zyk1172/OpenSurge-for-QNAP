# QNAP IPv6 DNS / fake-IP steering

In QNAP `same_lan` mode, OpenSurge can steer only Mihomo IPv6 fake-IP traffic without becoming the LAN's IPv6 default router.

Clients keep the main router's existing RA, public IPv6 address, DHCPv6/bridge behavior, and IPv6 default route. The main router sends DNS queries to OpenSurge and adds one static IPv6 route for Mihomo's synthetic IPv6 prefix.

```text
normal IPv6
client ───────────────► main router ─────────► Internet
  │
  │ DNS
  ▼
main-router DNS
  │ upstream = OpenSurge IPv4
  ▼
OpenSurge / Mihomo DNS
  │
  └─ fake AAAA → fdfe:dcba:9876::/64
                         │
                         │ main-router static route
                         ▼
                     OpenSurge
                         │
                         ▼
                        TUN
                         │
                  DIRECT / PROXY / REJECT
```

## Address plan

| Purpose | Address / prefix |
|---|---|
| Mihomo IPv6 fake-IP | `fdfe:dcba:9876::/64` |
| Mihomo TUN IPv6 | `fdfe:dcba:9877::1/126` |
| Legacy compatibility ULA | `fdfe:dcba:9878::1/64` |
| Main-router next hop | Stable OpenSurge link-local IPv6 |

The old `fdfe:dcba:9878::/64` client ULA is no longer the QNAP same-LAN takeover path. It remains only for compatibility with older/manual configurations and other topologies.

## Stable link-local next hop

The entrypoint derives a predictable link-local IPv6 address from the container's stable IPv4. For example:

```text
192.168.2.241 → fe80::1:0:c0a8:2f1
```

The QNAP Web UI shows the exact value for the current OpenSurge IPv4. If the router requires an interface for a link-local next hop, select the LAN/bridge that contains OpenSurge.

## Main-router configuration

For an OpenSurge IPv4 of `192.168.2.241`:

1. Point the DNS upstream used for LAN clients to `192.168.2.241`.
2. Add an IPv6 static route:

```text
destination: fdfe:dcba:9876::/64
next hop:    fe80::1:0:c0a8:2f1
interface:   LAN / bridge
```

Do not point `::/0` at OpenSurge.

Keep the router's existing IPv6 RA, DHCPv6, bridge/passthrough, public prefix, and client default-router behavior unchanged.

## OpenSurge routing behavior

When enabled, QNAP installs an IPv6 selector only for the fake-IP prefix:

```text
ip -6 rule:
  to fdfe:dcba:9876::/64 lookup 20241

IPv6 table 20241:
  fdfe:dcba:9876::/64 dev tun0
```

A fail-closed guard prevents the synthetic prefix from falling through to the main IPv6 route if the owned TUN route disappears.

The previous broad same-LAN IPv6 path is intentionally removed:

```text
ip -6 rule iif eth0 lookup 20241
IPv6 table 20241 default dev tun0
```

Normal IPv6 therefore remains on the main router.

## Why `accept_ra=2` is required

The container still enables IPv6 forwarding for the fake-IP path. Linux normally stops accepting Router Advertisements while forwarding, so QNAP Compose sets:

```text
net.ipv6.conf.eth0.accept_ra=2
net.ipv6.conf.eth0.autoconf=1
```

This allows OpenSurge to retain the upstream/native IPv6 route while forwarding synthetic destinations into the TUN.

## Per-device IPv6 policy

The native Linux TUN is a layer-3 ingress and does not retain the original Ethernet source MAC. The guaranteed policy scope is therefore:

```text
fake IPv6 → global Mihomo rules → DIRECT / PROXY / REJECT
```

The old fixed-ULA derivation used to emulate per-device IPv6 identity has been removed from the QNAP same-LAN path. Existing IPv4 device policy behavior is unchanged.

## Important note about IPv4 A queries

This feature does not add or change IPv4 routes. However, OpenSurge currently uses Mihomo's unified `fake-ip` DNS mode, which may also return IPv4 fake IPs for A queries.

If the main router advertises OpenSurge DNS globally, make sure either the existing IPv4 fake-IP path is already reachable for those clients or the router applies a suitable DNS policy. This IPv6 change intentionally does not add a new `198.18.0.0/16` IPv4 static route.

## Web setup

In QNAP Web → Network → **IPv6 DNS-directed steering**:

1. Enable IPv6 DNS/fake-IP steering.
2. Copy the displayed main-router DNS upstream, fake IPv6 prefix, stable link-local next hop, and LAN interface.
3. Apply the DNS and static-route settings on the main router.
4. Confirm that the main-router DNS and IPv6 static route are configured.
5. Save and restart the Gateway.

For API/config compatibility, the persisted field is still named `transparent.ipv6_shared_l2_ready`. In QNAP `same_lan`, it now means that the main-router DNS and fake-IP IPv6 static route are ready.

## Validation

```sh
docker exec opensurge ip -6 addr show dev eth0
docker exec opensurge cat /proc/sys/net/ipv6/conf/eth0/accept_ra
docker exec opensurge ip -6 rule show
docker exec opensurge ip -6 route show table 20241
```

Expected state:

- `accept_ra = 2`;
- the deterministic `fe80::1:0:...` next hop is present;
- a rule selects only `fdfe:dcba:9876::/64`;
- IPv6 table 20241 contains only the fake-prefix route to `tun0` for this feature;
- clients still have their main-router/public IPv6 address and default route.

## Rollback

Disable IPv6 DNS/fake-IP steering in OpenSurge, restore the router's previous DNS upstream, and remove the `fdfe:dcba:9876::/64` static route. Client IPv6 addressing/default routing does not require per-device rollback because OpenSurge never replaced it.
