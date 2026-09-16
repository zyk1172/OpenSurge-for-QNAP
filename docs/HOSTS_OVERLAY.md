# Hosts support in the global profile overlay

OpenSurge exposes Mihomo Hosts behavior from **Advanced · Global Profile Overlay** without handing gateway-owned DNS fields to imported profiles.

## Guided controls

The guided editor exposes two native Mihomo DNS switches:

- **Use configured Hosts** → `dns.use-hosts`
- **Read system Hosts** → `dns.use-system-hosts`

On QNAP, Mihomo runs inside the OpenSurge container. Therefore `use-system-hosts` reads the container's `/etc/hosts`, not an arbitrary file from the QNAP host filesystem.

Mihomo defaults both switches to `true` when they are absent. The guided editor presents that default when the overlay has not explicitly overridden a switch; changing a switch writes an explicit overlay value that takes precedence over an imported profile.

## Native Mihomo Hosts with wildcards

The guided editor now also exposes a **Native Mihomo Hosts** YAML field. This field writes the native top-level `hosts:` mapping rather than the conventional `/etc/hosts` text format, so Mihomo domain wildcards can be used directly.

The editor accepts either a mapping body:

```yaml
'*.example.com': 192.0.2.10
'+.example.net': 192.0.2.11
'.sub.example.org': 192.0.2.12
redirect.example: target.example
multi.example:
  - 192.0.2.20
  - 192.0.2.21
```

or a complete pasted block:

```yaml
hosts:
  '*.example.com': 192.0.2.10
  '+.example.net': target.example
```

Wildcard keys should be quoted. Mihomo wildcard semantics apply: `*.example.com` matches one subdomain level, `+.example.com` is a suffix-style match that also covers the root domain, and `.example.com` matches subdomains without the root domain.

Values may be a single non-empty string or a non-empty string array. A string can be an IP address or another hostname supported by Mihomo Hosts resolution.

## Standard Hosts file import

The existing standard Hosts editor remains available for large conventional files:

```text
# comments and blank lines are allowed
0.0.0.0 ads.example.com tracker.example.com
192.168.2.10 nas.home
2001:db8::10 nas-v6.home
```

OpenSurge parses the file before saving/applying it and converts it to Mihomo's native top-level `hosts:` mapping. The parser supports:

- IPv4 and IPv6 addresses;
- one or more hostnames after an address;
- blank lines and `#` comments;
- duplicate host/IP pair removal;
- multiple addresses for one hostname, rendered as a Mihomo array.

Invalid lines or invalid IP addresses fail overlay validation before the candidate Mihomo configuration is applied.

For backward compatibility, the raw standard Hosts text and the optional native YAML block are persisted in the existing OpenSurge-only `dns.merge.hosts-file` overlay field. OpenSurge consumes this internal representation during composition; the project-only field and its persistence markers are **never** emitted as Mihomo DNS keys.

## Merge order

Hosts data is merged in this order:

1. `hosts:` already present in an imported Mihomo profile;
2. entries parsed from the traditional Hosts file editor;
3. entries from the native Mihomo Hosts YAML editor.

A later exact key replaces an earlier exact key. This lets the native editor intentionally override a traditional or imported entry while preserving unrelated entries.

The final generated Mihomo configuration contains one native top-level section such as:

```yaml
hosts:
  ads.example.com: 0.0.0.0
  '*.example.net': 192.0.2.10
  nas.home: 192.168.2.10

dns:
  use-hosts: true
  use-system-hosts: false
```

## AI / automation control API

Agents do not need to rewrite the whole profile overlay just to manage Hosts. The authenticated control API exposes a focused Hosts view on the existing profile-overlay endpoint:

```text
GET /api/v1/profile-overlay?view=hosts
PUT /api/v1/profile-overlay?view=hosts
```

`GET` returns:

```json
{
  "schema_version": 1,
  "revision": "...",
  "enabled": true,
  "use_hosts": true,
  "use_system_hosts": false,
  "standard_hosts": "0.0.0.0 ads.example\n",
  "native_hosts_yaml": "'*.example.com': 192.0.2.10\n",
  "desired": false,
  "applied": false
}
```

`PUT` is a partial update. Omitted fields are preserved. It uses the same optimistic-concurrency protection as the full overlay endpoint, so the caller must send the current revision in `If-Match`.

Example request body:

```json
{
  "enabled": true,
  "use_hosts": true,
  "native_hosts_yaml": "'*.example.com': 192.0.2.10\n'+.example.net': target.example\n"
}
```

Supported update fields are `enabled`, `use_hosts`, `use_system_hosts`, `standard_hosts`, and `native_hosts_yaml`. Invalid traditional Hosts text or invalid native Hosts YAML is rejected before the overlay is saved.

Saving the overlay changes the persistent desired configuration; it does not bypass the normal OpenSurge apply/start/reload lifecycle. The resulting candidate is still composed and validated before it becomes the running Mihomo configuration.

This feature does not turn arbitrary remote Hosts subscriptions into Mihomo rule providers. A local/pasted Hosts file or native Hosts mapping is compiled into Mihomo's native `hosts:` configuration.
