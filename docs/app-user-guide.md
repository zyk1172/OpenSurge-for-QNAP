# OpenSurge for QNAP User Guide

[简体中文](app-user-guide.zh-CN.md) · **English**

This guide is for people using OpenSurge through QNAP Container Station / Docker. See the [QNAP Docker deployment guide](../deploy/qnap/README.md) for deployment details.

## 1. Open the Web UI

After the container starts, open:

```text
http://<OpenSurge-IP>:8080
```

Create the first administrator account. Normal day-to-day configuration is then handled in the Web UI.

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
