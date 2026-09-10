# Hosts support in the global profile overlay

OpenSurge exposes Mihomo Hosts behavior from **Advanced · Global Profile Overlay** without handing gateway-owned DNS fields to imported profiles.

## Guided controls

The guided editor exposes two native Mihomo DNS switches:

- **Use configured Hosts** → `dns.use-hosts`
- **Read system Hosts** → `dns.use-system-hosts`

On QNAP, Mihomo runs inside the OpenSurge container. Therefore `use-system-hosts` reads the container's `/etc/hosts`, not an arbitrary file from the QNAP host filesystem.

Both Mihomo switches default to `true` when they are absent, matching Mihomo's current defaults. The UI shows that effective state and only writes an explicit value after the user changes a switch.

## Standard Hosts file import

The guided editor can paste or import a normal Hosts file using this format:

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

The raw text is persisted inside the overlay document as the OpenSurge-only field `dns.merge.hosts-file`. This field is consumed by OpenSurge during composition and is **never** emitted as a Mihomo DNS key.

## Composition rules

Imported profiles may already contain native top-level `hosts:` entries. OpenSurge preserves them. When a Hosts-file entry uses the same hostname, the global overlay value replaces that hostname while unrelated imported entries remain intact.

The final generated Mihomo configuration contains a native top-level section such as:

```yaml
hosts:
  ads.example.com: 0.0.0.0
  nas.home: 192.168.2.10

dns:
  use-hosts: true
  use-system-hosts: false
```

This feature does not turn arbitrary remote Hosts subscriptions into Mihomo rule providers. A local/pasted Hosts file is parsed by OpenSurge and compiled into Mihomo's native `hosts:` configuration.
