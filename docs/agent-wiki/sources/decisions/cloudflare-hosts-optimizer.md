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

Candidates first pass TCP/443 sampling, then hard latency/loss filters, target-domain TLS SNI / HTTP Host validation, and finally bounded download testing. The standard preset uses 128 TCP workers, zero-loss filtering, 30 HTTPS candidates and at most 8 download candidates. The scan latency ceiling is user-selectable in the UI (100–1000 ms). An optional minimum download-throughput threshold can be set in Mbps: `0` preserves the HTTPS-verified fallback when download measurement is unavailable, while values above `0` strictly reject unmeasured candidates and candidates below the configured throughput floor. A domain explicitly rejected by this throughput floor does not retain a stale optimizer result from a previous run.

Download throughput uses a large 1,000,000,000-byte Cloudflare response stream so fast links do not finish after only a few MiB. The optimizer binds the HTTPS connection directly to the candidate IP while keeping SNI `speed.cloudflare.com`, starts the throughput timer only after response headers have arrived, and cancels the body transfer after the preset's `download_seconds` window. TCP setup, TLS negotiation and TTFB therefore remain separate quality dimensions instead of depressing the reported Mbps. Configurations carrying the older 4–16 MiB hidden response size are migrated automatically to the large-stream value.

HTTPS validation treats the Cloudflare edge identity as authoritative rather than using a strict 2xx/3xx-only rule. A 2xx/3xx response still passes normally. HTTP 403 and 405 are accepted only after the TLS/SNI request succeeds and the response proves Cloudflare edge handling via `CF-Ray` or `Server: cloudflare`; these statuses are common for PT/WAF-protected sites and for origins that reject `HEAD`. Restricted responses are re-checked with a bounded `GET`. Explicit Cloudflare Edge IP Restricted / error 1034 responses remain hard failures and are never admitted to the candidate queue.

This flow adopts the useful operational ideas from Lyxot/CloudflareSpeedTestDNS and XIU2/CloudflareSpeedTest while keeping the OpenSurge-native engine, physical-egress enforcement, persistence, scheduler and Mihomo reconciliation. No second optimizer container, DDNS subsystem, TOML/CLI configuration layer or separate long-running process is embedded.

## DNS / Fake-IP ownership

The optimizer does not append blindly to user configuration. At final-profile compilation it inspects the effective `fake-ip-filter-mode` and existing `fake-ip-filter` entries, performs semantic coverage checks, and only adds missing real-IP rules. Existing wildcard coverage is reused. Unsafe rule-mode or whitelist conflicts fail closed rather than silently producing a rule that cannot take effect.

## Persistence

- `/data/control/cloudflare-optimizer.json`: user settings and targets.
- `/data/control/cloudflare-optimizer-state.json`: last/next run and selected addresses.

Both use atomic 0600 writes through the Control service store boundary.
