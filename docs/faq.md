# OpenSurge for QNAP FAQ

See the [OpenSurge for QNAP User Guide](app-user-guide.md) and [QNAP Docker deployment guide](../deploy/qnap/README.md) for the complete workflow.

## Why does the QNAP edition use one container?

The current default architecture keeps the Web UI, Control API, mihomo, dnsmasq, TUN, and policy routing in one `opensurge` container.

QNET, static IP, and the parent interface must be chosen when the container is created, so the default architecture no longer needs Manager/Orchestrator sidecars or a Docker socket exposed to the Web UI.

## Why do I see eth0 inside the container when QNAP uses br0 / eth1?

That is expected.

- `br0`, `eth1`, etc. are QNAP host-side QNET parent interfaces.
- `eth0` is the interface inside the container network namespace.

They do not need to have the same name.

## Why can’t the Web UI change the QNET parent or static IP?

Those are Docker/QNET creation parameters rather than OpenSurge runtime settings.

To change them:

1. stop the Gateway;
2. preserve `/data`;
3. change the Compose creation parameters;
4. recreate the `opensurge` container.

The Web UI manages only runtime settings that can be safely persisted as OpenSurge configuration.

## Does missing nftables mean transparent proxying cannot work?

Not necessarily.

The supported same-LAN QNAP backend can use ingress-interface policy routing when TUN, `NET_ADMIN`, and Linux policy routing are available:

```text
ip rule iif <LAN interface>
→ OpenSurge dedicated table
→ tun0
→ mihomo
```

So same-LAN mode does not require `nf_tables`.

Isolated downstream topologies that require NAT may still require the nftables/fwmark backend.

## What if `/dev/net/tun` is missing?

First distinguish between a missing host TUN driver and a missing container device mapping.

Host checks:

```sh
ls -l /dev/net/tun
grep -w tun /proc/misc
```

If host TUN works, Compose still needs:

```yaml
devices:
  - /dev/net/tun:/dev/net/tun
```

plus `NET_ADMIN` / `NET_RAW`.

If the host kernel itself has no TUN support, the Docker image cannot add a missing host-kernel capability.

## Why can `nft list ruleset` fail while the Gateway still works?

Some QNAP 5.10 kernels do not provide `nf_tables` netlink support. In same-LAN nft-free TUN mode, nftables is not required.

Check:

```sh
ip rule show
ip route show table 20241
```

and the actual TUN, Mihomo, and DNS state instead of treating the `nft` command alone as the Gateway health signal.

## Why doesn’t an imported subscription become active immediately?

Import creates a draft first.

When the Gateway is stopped, choose **Use on next start**. While it is running, choose **Apply and reload**.

The Web UI rereads persisted source state after the backend operation and reports success only after `desired=true` or `applied=true` is confirmed.

## Will “next start” state survive a container restart?

Yes, when persistence is configured correctly. Source state and the selected configuration are stored under `/data`.

Keep the same persistent bind mount when updating or recreating the container.

## Why does saving runtime settings briefly stop the Gateway?

A running network configuration should not be changed only in memory. The current flow is:

```text
stop Gateway
→ persist /data/config/opensurge.yaml
→ reread and verify revision
→ restart Gateway
```

This prevents the UI from showing success when the configuration was not actually persisted.

## Why is runtime state marked interrupted after a NAS/container crash?

OpenSurge does not assume that kernel/network objects from the previous container namespace are still valid.

At container startup, OpenSurge first reconciles the persisted runtime and restores the Gateway only when the persisted desired state says it should be running. If QNAP is still bringing up Docker, QNET, or TUN, the Web and Control processes remain available while the entrypoint retries recovery in the background. An intentionally stopped Gateway is not started by these retries.

If the bounded retries are exhausted, run **Safe cleanup**. Cleanup uses persisted snapshots, journals, and ownership data to remove only objects that can be proven to belong to OpenSurge, then a new Gateway can be started.

## Why doesn’t OpenSurge automatically change QTS default routes, DNS, or Virtual Switch?

That is an intentional security boundary.

The Gateway needs network-management capability inside its container, but the LAN Web UI should not also have arbitrary control over the NAS management network. QNET host parameters are fixed at deployment time; runtime control stays inside the OpenSurge data plane.

## Does the NAS need Go, Node.js, or gcc?

No.

Test images are prebuilt by GitHub Actions. The NAS needs Docker/Container Station, QNET, TUN, and the required host-kernel networking capabilities.

## Why not build the image on the NAS?

The normal workflow is:

```text
GitHub Actions build
→ download amd64/arm64 tar.gz
→ verify SHA256
→ docker load
→ recreate container
```

This avoids installing development dependencies and spending substantial CPU time compiling on a NAS.

## How do I upgrade a test image safely?

1. keep a backup tag for the current image;
2. download and verify the new test archive;
3. `docker load` it;
4. preserve the same `/data` and QNET creation parameters;
5. recreate `opensurge`;
6. verify Web login, configuration, subscriptions, and admin state;
7. start the Gateway.

Do not delete `/data` as part of a normal upgrade.

## How should I test the first real client?

Use one device only:

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

Keep main-router DHCP unchanged during initial testing. Validate DNS, DIRECT, PROXY, UDP/QUIC, long-lived connections, and downloads before adding more clients.
