#!/usr/bin/env bash
set -euo pipefail

CLIENT_NS="opensurge-lab-client"
GATEWAY_NS="opensurge-lab-gateway"
UPSTREAM_NS="opensurge-lab-upstream"
TEST_BIN="${OPEN_SURGE_LAB_TEST_BIN:-/tmp/opensurge-linux-network.test}"

log() { printf '[labnetns] %s\n' "$*"; }

cleanup() {
  set +e
  sudo ip netns del "$CLIENT_NS" 2>/dev/null
  sudo ip netns del "$GATEWAY_NS" 2>/dev/null
  sudo ip netns del "$UPSTREAM_NS" 2>/dev/null
  sudo ip link del os-cl-host 2>/dev/null
  sudo ip link del os-gw-host 2>/dev/null
  set -e
}
trap cleanup EXIT INT TERM
cleanup

for tool in ip nft ping; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

if sudo nft list table inet opensurge >/dev/null 2>&1; then
  echo "host already contains table inet opensurge; refusing a lab run that cannot prove ownership" >&2
  exit 1
fi

if [[ ! -x "$TEST_BIN" ]]; then
  log "building Linux network integration test binary"
  go test -c -o "$TEST_BIN" ./internal/platform/linux
fi

log "creating isolated client -> gateway -> upstream topology"
sudo ip netns add "$CLIENT_NS"
sudo ip netns add "$GATEWAY_NS"
sudo ip netns add "$UPSTREAM_NS"

sudo ip link add os-client type veth peer name os-gw-lan
sudo ip link set os-client netns "$CLIENT_NS"
sudo ip link set os-gw-lan netns "$GATEWAY_NS"

sudo ip link add os-gw-wan type veth peer name os-upstream
sudo ip link set os-gw-wan netns "$GATEWAY_NS"
sudo ip link set os-upstream netns "$UPSTREAM_NS"

sudo ip -n "$CLIENT_NS" link set lo up
sudo ip -n "$GATEWAY_NS" link set lo up
sudo ip -n "$UPSTREAM_NS" link set lo up

sudo ip -n "$CLIENT_NS" addr add 10.77.1.2/24 dev os-client
sudo ip -n "$GATEWAY_NS" addr add 10.77.1.1/24 dev os-gw-lan
sudo ip -n "$GATEWAY_NS" addr add 10.77.2.1/24 dev os-gw-wan
sudo ip -n "$UPSTREAM_NS" addr add 10.77.2.2/24 dev os-upstream
sudo ip -n "$UPSTREAM_NS" addr add 203.0.113.1/32 dev lo

sudo ip -n "$CLIENT_NS" link set os-client up
sudo ip -n "$GATEWAY_NS" link set os-gw-lan up
sudo ip -n "$GATEWAY_NS" link set os-gw-wan up
sudo ip -n "$UPSTREAM_NS" link set os-upstream up

sudo ip -n "$CLIENT_NS" route add default via 10.77.1.1
# The synthetic Internet address lives on upstream's loopback. The gateway
# therefore needs an explicit next hop for it; merely connecting the
# 10.77.2.0/24 WAN segment does not create a route to 203.0.113.1/32.
sudo ip -n "$GATEWAY_NS" route add 203.0.113.1/32 via 10.77.2.2 dev os-gw-wan
sudo ip -n "$UPSTREAM_NS" route add 10.77.1.0/24 via 10.77.2.1
sudo ip netns exec "$GATEWAY_NS" sysctl -q -w net.ipv4.ip_forward=1
sudo ip netns exec "$GATEWAY_NS" sysctl -q -w net.ipv4.conf.all.rp_filter=0
sudo ip netns exec "$GATEWAY_NS" sysctl -q -w net.ipv4.conf.default.rp_filter=0

log "proving each isolated hop before the routed baseline"
sudo ip netns exec "$CLIENT_NS" ping -c 1 -W 2 10.77.1.1 >/dev/null
sudo ip netns exec "$GATEWAY_NS" ping -c 1 -W 2 10.77.2.2 >/dev/null

log "proving the isolated baseline route"
if ! sudo ip netns exec "$CLIENT_NS" ping -c 2 -W 2 203.0.113.1; then
  echo "baseline routed ping failed; dumping namespace routes" >&2
  sudo ip -n "$CLIENT_NS" route show >&2 || true
  sudo ip -n "$GATEWAY_NS" route show >&2 || true
  sudo ip -n "$UPSTREAM_NS" route show >&2 || true
  exit 1
fi

log "running OpenSurge nftables/policy-routing integration tests inside gateway namespace"
sudo ip netns exec "$GATEWAY_NS" env OPEN_SURGE_NETWORK_TESTS=1 \
  "$TEST_BIN" -test.v -test.run '^TestNetwork'

log "proving the lab namespace still routes after the integration suite"
# The integration suite intentionally exercises failure/restore paths. Remove
# any test-scoped residue inside the disposable namespace before the topology
# continuity check; destroying the namespace is the ultimate isolation boundary.
sudo ip netns exec "$GATEWAY_NS" nft delete table inet opensurge 2>/dev/null || true
sudo ip netns exec "$GATEWAY_NS" ip rule del fwmark 0x29 table 20241 2>/dev/null || true
sudo ip netns exec "$GATEWAY_NS" ip route flush table 20241 2>/dev/null || true
sudo ip netns exec "$CLIENT_NS" ping -c 2 -W 2 203.0.113.1 >/dev/null

log "checking that the host namespace was never touched"
if sudo nft list table inet opensurge >/dev/null 2>&1; then
  echo "FAIL: OpenSurge nftables table leaked into the host namespace" >&2
  exit 1
fi
if ip -j rule show | grep -q '20241'; then
  echo "FAIL: OpenSurge policy rule leaked into the host namespace" >&2
  exit 1
fi

log "PASS: isolated Linux network namespace lab completed without host contamination"
