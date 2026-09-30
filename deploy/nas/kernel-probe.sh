#!/bin/sh
# Run only in a disposable container network namespace (preflight uses network=none).
set -eu
[ "${OPENSURGE_DISPOSABLE_PROBE:-}" = 1 ] || {
  echo 'This probe requires an explicitly designated disposable container namespace.' >&2
  exit 1
}

cleanup() {
  ip rule del pref 20241 iif os-macvlan table 20241 2>/dev/null || true
  ip route del table 20241 default dev os-tun 2>/dev/null || true
  ip link del os-tun 2>/dev/null || true
  ip link del os-macvlan 2>/dev/null || true
  ip link del os-ingress 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

ip link add os-ingress type veth peer name os-peer
ip link set os-ingress up
ip link set os-peer up
ip link add link os-ingress name os-macvlan type macvlan mode bridge
ip link set os-macvlan up
ip addr add 192.0.2.1/24 dev os-macvlan
ip tuntap add dev os-tun mode tun
ip link set os-tun up
ip route add table 20241 default dev os-tun
ip rule add pref 20241 iif os-macvlan table 20241
for proto in 6 17; do
  ip -4 route get 203.0.113.1 from 192.0.2.2 iif os-macvlan \
    ipproto "$proto" sport 23456 dport 443 | grep -q 'dev os-tun'
done
echo 'macvlan / TUN / TCP+UDP ingress routing probe passed'
