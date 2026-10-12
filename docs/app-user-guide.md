# OpenSurge for QNAP User Guide

[简体中文](app-user-guide.zh-CN.md) · **English**

This guide is for people using OpenSurge through QNAP Container Station / Docker. See the [QNAP Docker deployment guide](../deploy/qnap/README.md) for deployment details.

## 1. Open the Web UI

After the container starts, open:

```text
http://<OpenSurge-IP>:8080
```

Open the local management page directly; no username, password or bootstrap token is required. Normal day-to-day configuration is handled in the Web UI.

The QNAP edition is a single-container product; there is no separate Manager or Orchestrator UI in the default deployment.

## 2. Container network vs runtime settings

These values are fixed when the container is created:

- QNAP QNET parent NIC / Virtual Switch;
- OpenSurge static IPv4;
- LAN CIDR;
- upstream-router IPv4;
- persistent `/data` path.

They are deployment parameters, not runtime Web settings. To change them, preserve `/data`, change the Compose/QNET creation parameters, and recreate the container.

The Web UI manages OpenSurge runtime settings such as DNS, TUN, subscriptions, providers, policies, and device rules.

## 3. Import proxy configuration

Open **Sources**.

You can import:

- an HTTPS subscription;
- a local `.yaml` / `.yml` Mihomo configuration.

An import first creates a persistent draft and does not immediately change the running gateway.

Each source shows structural validation, policy groups, providers, rule counts, and running/next-start state. When the gateway is stopped, choose **Use on next start**; while it is running, choose **Apply and reload**.

The Web UI rereads persisted state after the backend operation and reports success only after `desired` or `applied` is confirmed.

## 4. Start the gateway

Open **Network** or the dashboard and choose **Start Gateway**.

The current QNAP same-LAN mode uses TUN plus Linux policy routing. Some QNAP kernels do not provide `nf_tables`; this does not necessarily prevent transparent proxying. Supported same-LAN systems can route client traffic into TUN through ingress-interface policy routing.

After startup, Gateway, mihomo, DNS/dnsmasq, TUN, and IPv4 forwarding should report healthy states.

If startup fails, open **Diagnostics**, run Doctor, and inspect structured errors and component logs.

## 5. Connect one client first

Do not change main-router DHCP during the first validation.

Choose one test client and set:

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

Then validate:

1. direct destinations;
2. proxied destinations;
3. DNS;
4. UDP / QUIC;
5. long-lived video traffic;
6. large downloads.

Add more clients only after the first one is stable.

## 6. Policies and devices

Use **Policies** to inspect policy groups, choose Selector members, and check provider/rule routing.

Use **Devices** to register clients, assign dedicated egress or global-policy behavior, and inspect active connections and traffic.

The first stable target remains same-LAN manual gateway; OpenSurge does not automatically take over DHCP for the whole household LAN.

## 7. Change DNS / TUN runtime settings

Open **Network → Runtime settings**.

Mutable values are persisted in:

```text
/data/config/opensurge.yaml
```

If the gateway is running, the Web flow performs:

```text
stop → persist → reread/verify → restart
```

A failed step should be reported explicitly rather than shown as a false success.

## 8. Diagnostics

The **Diagnostics** page exposes Doctor, providers, live connections, component logs, lifecycle operations, and recovery state.

A normal page refresh does not run the full Doctor. Run it explicitly when needed.

On a QNAP kernel without `nf_tables`, a failing `nft list ruleset` command by itself does not mean the same-LAN data plane failed. Check the selected data plane, TUN state, `ip rule`, and the dedicated routing table.

## 9. Recreate or upgrade the container

Keep the existing `/data` bind mount when loading a newer test image:

1. download and verify the new image archive;
2. `docker load` it;
3. preserve the same QNET parameters and `/data` path;
4. recreate the `opensurge` container;
5. log in and verify configuration, subscriptions, and admin state;
6. start the Gateway again.

Do not delete `/data/runtime`, `/data/control`, or the full persistent directory as part of a normal upgrade.

## 10. Recover after an interrupted restart

If the container or NAS stops while the Gateway is active, OpenSurge may classify the previous runtime as interrupted.

Run **Safe cleanup** first. Cleanup is limited to objects for which persisted OpenSurge ownership can be proven and should not change QTS default routes, DNS, or unrelated container networks.

Start the Gateway again after cleanup completes.

## 11. Current boundaries

The first stable release does not aim to provide:

- automatic QNAP Network & Virtual Switch mutation;
- automatic main-router DHCP takeover;
- downstream IPv6 takeover;
- QPKG packaging;
- automatic migration of the whole household network.

The current priority is making the single-container QNET/TUN/DNS/subscription/policy/device/recovery path stable on real QNAP systems.

## SmartDNS upstream DNS

Under **Network Settings → DNS Service → SmartDNS upstream settings**, select **Custom (recommended: encryption first)**. Add AliDNS (`https://dns.alidns.com/dns-query`, `223.5.5.5`), Tencent DNSPod (`https://doh.pub/dns-query`, `1.12.12.12`), Cloudflare, Google or Quad9 DoH presets, or enter your own trusted DoH/DoT provider. Encrypted upstreams require a hostname URL and a fixed connection IPv4, for example `https://cloudflare-dns.com/dns-query` with `1.1.1.1`, or `tls://dns.google:853` with `8.8.8.8`. The IP is used for transport; certificate and hostname verification remain enabled, with no system DNS bootstrap dependency. Update the fixed IP if the provider changes it.

Plaintext DNS is disabled by default. If explicitly allowed, enter an IPv4 or a `udp://IPv4:53` / `tcp://IPv4:53` URL without a bootstrap IPv4. When encrypted upstreams exist, plaintext is only a fallback after the encrypted upstreams fail. Custom mode never automatically adds system or router DNS. Choose providers you trust first, then let SmartDNS optimize connections among their answers. Encryption prevents in-transit tampering but does not certify provider answers; presets are examples, and Quad9 also filters known malicious domains.

Saving verifies persisted settings and restarts a running gateway.

Choose a provider in **Upstream DNS preset**, then click **Add preset**, or add a custom upstream. Already-added presets cannot be added again until removed. Editing and refresh controls are disabled during saving; switches support both Space and Enter.

Cache handling follows the [official SmartDNS configuration](https://github.com/pymumu/smartdns/blob/21c940edc65520849ba03544c5cf8d9cf326e680/etc/smartdns/smartdns.conf) and [LuCI integration](https://github.com/pymumu/luci-app-smartdns/blob/581e5e816d92d3a663b1b2e331e3f21685968cf1/htdocs/luci-static/resources/view/smartdns/smartdns.js). Prefetch and up to 600 seconds of expired answers remain enabled, with a 3-second expired-answer TTL. Custom encrypted mode with plaintext disabled persists real answers in `/data/runtime/smartdns-cache` on exit and at hourly checkpoints. Restarts reuse an unchanged policy's cache; changes to upstreams, resolver rules or proxy configuration start a fresh cache. Startup prunes only obsolete OpenSurge cache files. Gateway Fake-IP and DHCP local answers remain uncached. Validation uses a separate temporary cache and neither loads nor modifies the live cache. Plaintext-authorized and system modes do not persist caches. Runtime node selection within a group follows the original answer TTL; it does not change the saved proxy configuration.

Two independent options are off by default:

- **Query DNS upstreams through OpenSurge proxy**: enter a node or group name from the active profile. DoH, DoT and TCP queries use a private loopback SOCKS listener pinned to this target; LAN SOCKS need not be enabled. UDP upstreams must be replaced before enabling this option. SmartDNS never bypasses a failed proxy to connect directly. The selected group's route still controls the actual exit, so choose one that uses proxy nodes. Proxy mode compares response times of trusted upstream queries rather than probing answer IPs directly from the NAS.
- **Replace Mihomo DNS upstreams with SmartDNS**: replace ordinary, direct, fallback and domain-policy upstreams with a private SmartDNS Resolver View listener. Preserve Fake-IP, fake-IP filters, hosts and TUN DNS hijacking. Gateway View still enters Mihomo first. Disabling this option restores profile DNS settings.

Enabling either option resolves **proxy node hostnames** directly through the selected encrypted upstreams using fixed IPv4 bootstrap and authenticated TLS hostnames. This works before SmartDNS starts and avoids depending on the same proxy being established. At least one encrypted upstream is required; plaintext never participates in node bootstrap. DoH URLs with query parameters are not supported for this integration. The first option controls proxying for ordinary queries.

Persisted fields are `dns.resolver_proxy_enabled`, `dns.resolver_proxy_target` and `dns.mihomo_use_smartdns`; booleans default to `false`. Unknown target names are rejected before persistence. Internal ports `127.0.0.1:7854` and `127.0.0.1:5354` are reserved when their respective options are enabled. Existing installations retain legacy system resolver mode until explicitly changed, and neither option can be enabled in system mode. NAS host DNS takeover remains independent. Applied runtime cards can differ from saved settings while the gateway is stopped.
