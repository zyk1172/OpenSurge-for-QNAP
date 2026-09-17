# DNS policy control plane

## Decision

OpenSurge treats LAN DNS as a first-class gateway service while keeping the existing data-plane split:

```text
LAN client :53
  -> dnsmasq DNS edge
  -> mihomo resolver on 127.0.0.1:1053
  -> upstream resolvers
```

PR1 introduces only the DNS policy domain model and durable control-plane document. It does **not** change the rendered mihomo configuration, dnsmasq behavior, gateway lifecycle, or QNAP Web UI.

## Ownership modes

The DNS policy document has two explicit modes:

- `inherit_profile`: resolver policy continues to come from the imported mihomo profile and global profile overlay. This is the default and preserves existing installations without behavior changes.
- `managed`: OpenSurge owns resolver policy. The managed payload is retained while inactive so a user can switch back to `inherit_profile` without losing a prepared managed configuration.

The ownership switch applies only to resolver policy. Gateway-facing DNS infrastructure remains owned by the ordinary OpenSurge gateway configuration in every mode:

- LAN-facing dnsmasq listen address and port;
- mihomo loopback DNS listener;
- enhanced/fake-IP mode and fake-IP ranges that are required by the gateway data plane;
- QNAP IPv4-only product boundary until IPv6 takeover is implemented separately.

The managed policy model covers the resolver fields that will be compiled into mihomo in the next phase: default nameservers, normal/direct/proxy-server nameservers, fallback, ordered nameserver policy rules, fallback filters, fake-IP filter, `respect-rules`, cache algorithm, and HTTP/3 preference.

## Persistence

The canonical QNAP location is derived from the gateway config path:

```text
/data/config/opensurge.yaml
/data/config/dns-policy.json
```

`dns-policy.json` stays inside the existing `/data` persistence boundary. Loading a missing file returns the backward-compatible `inherit_profile` default **without creating a file**. A file is created only by an explicit save.

Stored documents are strict JSON:

- schema version is mandatory;
- unknown fields fail closed;
- malformed or unsupported schemas do not silently fall back to defaults;
- normalized content receives a SHA-256 revision;
- writes require the caller's expected revision and reject stale updates;
- writes use a same-directory temporary file plus atomic rename and mode `0600`.

The revision is derived from normalized semantic content and is not stored inside the document, avoiding self-referential revision state.

## Compatibility boundary

PR1 must have zero DNS runtime effect. Nothing in `internal/mihomo`, `internal/dhcp`, gateway start/stop, or the QNAP Web surface reads this policy yet.

That boundary is intentional:

1. PR1 establishes a durable, testable ownership and persistence contract.
2. PR2 compiles `managed` policy into authoritative mihomo DNS resolver fields and defines the interaction with imported profile/overlay DNS fields.
3. PR3 exposes DNS API/status/diagnostics and cache operations.
4. PR4 introduces the dedicated QNAP DNS page and removes the misleading editable internal `dns.upstream` control from ordinary runtime settings.

Do not bypass this model by adding a second DNS policy store to the Web layer or by allowing profile, overlay, and managed DNS to simultaneously own the same resolver fields.
