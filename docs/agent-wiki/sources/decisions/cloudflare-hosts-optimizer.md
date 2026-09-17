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
7. Run short download tests only for the best few candidates.
8. Persist the selected addresses in `/data/control/cloudflare-optimizer-state.json`.
9. During profile reconciliation, inject selected addresses into the effective top-level Mihomo `hosts` mapping and ensure each target uses real-IP handling.
10. If selected addresses changed while the gateway is running, perform one transactional full reload for the entire batch.

The imported profile and global Profile Overlay remain user-owned. Optimizer entries are injected only into the materialized effective profile, so deleting a target restores the underlying user configuration on the next reconciliation.

## Scheduling

Two schedule forms are supported:

- `interval`: every N days at a local HH:MM time. This is the default and avoids the calendar-reset semantics of `*/7` cron day-of-month expressions.
- `cron`: standard five-field minute/hour/day/month/weekday syntax.

Default schedule: every 7 days at 04:00.

## Scan budget

Default full-run budget is 60 seconds. The UI exposes 30/60/90/120 second presets. The probe engine shares its TCP coarse scan across all target domains and stops extending work when the global deadline expires.

## DNS / Fake-IP ownership

The optimizer does not append blindly to user configuration. At final-profile compilation it inspects the effective `fake-ip-filter-mode` and existing `fake-ip-filter` entries, performs semantic coverage checks, and only adds missing real-IP rules. Existing wildcard coverage is reused. Unsafe rule-mode or whitelist conflicts fail closed rather than silently producing a rule that cannot take effect.

## Persistence

- `/data/control/cloudflare-optimizer.json`: user settings and targets.
- `/data/control/cloudflare-optimizer-state.json`: last/next run and selected addresses.

Both use atomic 0600 writes through the Control service store boundary.
