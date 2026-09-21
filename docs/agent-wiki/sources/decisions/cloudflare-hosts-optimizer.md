# Cloudflare Hosts Optimizer

## Scope

QNAP-only control-plane feature for a small user-managed set of hostnames that should resolve to measured Cloudflare IPv4 addresses.

## Data path

1. Sample the official Cloudflare IPv4 ranges.
2. Run a bounded shared TCP/443 coarse pass from the OpenSurge container.
3. Bind probe sockets to the configured physical QNAP interface and source IPv4.
4. Fail closed if `ip route get` does not resolve through that physical interface or indicates TUN traversal.
5. Reuse the coarse candidate pool for every enabled hostname.
6. Validate candidates with the target hostname as TLS SNI / HTTP Host.
7. Run time-bounded download tests only for the best few candidates using Cloudflare's official `speed.cloudflare.com/__down` endpoint.
8. Persist the selected addresses in `/data/control/cloudflare-optimizer-state.json`.
9. During profile reconciliation, inject selected addresses into the effective top-level Mihomo `hosts` mapping and ensure each target uses real-IP handling.
10. If selected addresses changed while the gateway is running, perform one transactional full reload for the entire batch.

The imported profile and global Profile Overlay remain user-owned. Optimizer entries are injected only into the materialized effective profile, so deleting a target restores the underlying user configuration on the next reconciliation.

## Continuous monitoring and scheduling

The optimizer now separates cheap current-IP monitoring from expensive full optimization:

- current selected addresses are checked through the same physical-interface-bound direct path;
- the default health interval is 30 minutes;
- the default health thresholds are 100 ms latency and 0% TCP loss;
- an unhealthy current IP triggers a bounded full optimization;
- a periodic full optimization remains available even while current IPs stay healthy.

Full optimization still supports two schedule forms:

- `interval`: every N days at a local HH:MM time. New configurations default to every 1 day at 04:00;
- `cron`: standard five-field minute/hour/day/month/weekday syntax.

Existing user-selected schedules are preserved during migration.

## Scan budget

Full optimization uses bounded presets instead of an unbounded attempt-to-fill loop:

- fast: 30 seconds / 256 candidates;
- standard: 75 seconds / 1024 candidates;
- full: 120 seconds / 1536 candidates;
- deep: 180 seconds / 2048 candidates.

Candidates first pass TCP/443 sampling, then hard latency/loss filters, target-domain TLS SNI / HTTP Host validation, and finally bounded download testing. The standard preset uses 128 TCP workers, zero-loss filtering and a 30-candidate HTTPS working window. That window is no longer a one-shot top-N cut: when too few candidates validate for a target, the optimizer continues through later latency-sorted candidates in additional bounded windows until it has enough validated edges, the coarse pool is exhausted, or the scan budget expires. The scan latency ceiling is user-selectable in the UI (100–1000 ms).

An optional minimum download-throughput threshold can be set in Mbps. With `0`, the normal bounded first set is measured and HTTPS-verified candidates remain a fallback when download measurement is unavailable. With a value above `0`, OpenSurge follows the useful `-sl` behavior from XIU2/CloudflareSpeedTest: it keeps walking later HTTPS-verified candidates instead of failing after a fixed first set. Multi-domain probing rotates by candidate rank so one target cannot consume the whole queue. The search stops once every target with verified candidates has a measured IP at or above the floor, the HTTPS candidate cap is exhausted, or the outer context expires. The throughput floor remains strict: a domain with no qualifying measured candidate is rejected and does not retain a stale optimizer result from a previous run.

Download throughput uses Cloudflare's official `speed.cloudflare.com/__down` endpoint in 200,000,000-byte chunks. The optimizer binds the HTTPS connection directly to the candidate IP while keeping SNI `speed.cloudflare.com`, starts timing only while response bodies are being read, and accumulates active body time until the preset's `download_seconds` window is reached. If a fast link finishes a 200 MB chunk early, another chunk is requested on the same transport and the measurement continues. TCP setup, TLS negotiation, response-header time and TTFB are therefore kept outside the Mbps denominator. Configurations carrying the older 4–16 MiB hidden chunk size are migrated automatically to 200 MB.

HTTPS validation treats the Cloudflare edge identity as authoritative rather than requiring an origin-success status. After TLS/SNI succeeds, Cloudflare-backed 2xx–4xx responses with `CF-Ray` or `Server: cloudflare` are accepted because authentication, WAF, rate-limit and not-found responses still prove that the candidate reached the intended Cloudflare edge. HTTP 403 and 405 receive a bounded `GET` second opinion so body-only edge-IP errors can be detected; a different WAF status on that second request does not invalidate an already-valid `HEAD`. Explicit Cloudflare Edge IP Restricted / error 1034 responses and 5xx responses remain hard failures.

This flow adopts the useful operational ideas from Lyxot/CloudflareSpeedTestDNS and XIU2/CloudflareSpeedTest while keeping the OpenSurge-native engine, physical-egress enforcement, persistence, scheduler and Mihomo reconciliation. No second optimizer container, DDNS subsystem, TOML/CLI configuration layer or separate long-running process is embedded.

## DNS / Fake-IP ownership

The optimizer does not append blindly to user configuration. At final-profile compilation it inspects the effective `fake-ip-filter-mode` and existing `fake-ip-filter` entries, performs semantic coverage checks, and only adds missing real-IP rules. Existing wildcard coverage is reused. Unsafe rule-mode or whitelist conflicts fail closed rather than silently producing a rule that cannot take effect.

## Persistence

- `/data/control/cloudflare-optimizer.json`: user settings and targets.
- `/data/control/cloudflare-optimizer-state.json`: last/next run and selected addresses.

Both use atomic 0600 writes through the Control service store boundary.
