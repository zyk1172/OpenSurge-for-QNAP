# QNAP stable-readiness security model

This document records the security and failure-mode boundaries introduced by the stable-readiness hardening pass.

## Fail-closed same-LAN routing

Same-LAN QNET deployments install two ingress-interface RPDB rules:

1. the OpenSurge lookup rule, which selects the dedicated routing table;
2. a later `prohibit` guard rule on the same ingress interface.

The guard is installed before the dedicated routes and lookup rule and removed last. If the primary rule or the TUN default route disappears unexpectedly, forwarded client traffic is rejected instead of falling through to the namespace `main` table. Explicit direct fallback remains a deliberate table-20241 route to the configured upstream gateway.

The guard, primary rule, and dedicated table remain OpenSurge-owned resources and are covered by the existing durable cleanup journal and exact cleanup rules.

## First administrator bootstrap

QNAP requires a one-time 32-byte bootstrap token before the first administrator account can be created. The token is stored at `/data/web-auth/bootstrap-token` with mode `0600` and is printed to the container log on first creation. A successful setup consumes the token. Existing installations migrate the legacy administrator credential from `/data/control/admin.json` to `/data/web-auth/admin.json` without resetting the account.

## One container, two privilege domains

The QNAP image still deploys exactly one Docker container, but its entrypoint supervises two OpenSurge processes:

- the Control component runs as root with the container's network capabilities and listens only on `127.0.0.1`;
- the LAN-facing Web component runs as UID/GID 65532 with an empty capability bounding/effective set.

The Web process receives the internal Control bearer token and proxies authenticated QNAP operations to loopback. Mac-only LAN API routes are rejected by the QNAP Web boundary. The QNAP runtime image does not include the historical manager or orchestrator binaries.

## Listener exposure

When transparent TUN mode is enabled, Mihomo's mixed proxy port binds only to `127.0.0.1` and `allow-lan` is disabled. Mihomo's internal DNS listener on port 1053 is always loopback-only; dnsmasq remains the LAN-facing DNS service on port 53. Turning transparent mode off is the explicit manual-proxy mode and may expose the mixed port to the LAN.

## Subscription network policy

Remote source imports remain HTTPS-only and re-resolve each connection. All resolved addresses must pass the public-destination policy. Private, loopback, link-local, CGNAT/Tailscale (`100.64.0.0/10`), documentation, benchmarking, and reserved destination ranges are rejected before dialing.

## HTTPS reverse proxy mode

Direct LAN HTTP remains supported for local deployments. When a trusted HTTPS reverse proxy is used, set `OPENSURGE_SECURE_COOKIES=true`; administrator cookies then carry `Secure` and Web responses include HSTS. Do not enable this flag when accessing the backend directly over plain HTTP.

## Readiness and release supply chain

`/health/ready` now performs an authenticated request to the loopback Control configuration endpoint rather than treating any HTTP response as readiness.

Rolling test releases pin the privileged GitHub Actions used by the publishing workflow, build from digest-pinned base images, publish SPDX JSON SBOMs, and create GitHub build-provenance and SBOM attestations for each architecture archive. `SHA256SUMS` covers both Docker archives and both SBOM files.
