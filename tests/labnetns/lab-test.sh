#!/usr/bin/env bash
set -euo pipefail

CLIENT_NS="opensurge-lab-client"
GATEWAY_NS="opensurge-lab-gateway"
UPSTREAM_NS="opensurge-lab-upstream"
TEST_BIN="${OPEN_SURGE_LAB_TEST_BIN:-/tmp/opensurge-linux-network.test}"
GATEWAY_TEST_BIN="${OPEN_SURGE_GATEWAY_TEST_BIN:-/tmp/opensurge-gateway.test}"

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

for tool in ip nft ping dnsmasq dig; do
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
if [[ ! -x "$GATEWAY_TEST_BIN" ]]; then
  log "building gateway fault-injection test binary"
  go test -c -o "$GATEWAY_TEST_BIN" ./internal/gateway
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

log "injecting a real dnsmasq crash and proving safe replacement"
sudo ip netns exec "$GATEWAY_NS" env OPEN_SURGE_DNSMASQ_FAULT_TESTS=1 \
  "$GATEWAY_TEST_BIN" -test.v -test.run '^TestDNSMasqCrashRecoveryLinux$'

log "proving the lab namespace still routes after integration and crash recovery"
# The network integration suite intentionally exercises failure/restore paths.
# Remove only test-scoped residue inside the disposable namespace before the
# continuity check; destroying the namespace remains the final isolation wall.
sudo ip netns exec "$GATEWAY_NS" nft delete table inet opensurge 2>/dev/null || true
sudo ip netns exec "$GATEWAY_NS" ip rule del fwmark 0x29 table 20241 2>/dev/null || true
sudo ip -n "$GATEWAY_NS" rule del pref 20241 iif os-gw-lan table 20241 2>/dev/null || true
sudo ip -n "$GATEWAY_NS" rule del pref 20242 iif os-gw-lan prohibit 2>/dev/null || true
sudo ip netns exec "$GATEWAY_NS" ip route flush table 20241 2>/dev/null || true
sudo ip netns exec "$CLIENT_NS" ping -c 2 -W 2 203.0.113.1 >/dev/null

log "proving NAS-host DNS takeover with L4 policy routing and no firewall NAT"
# CLIENT_NS models the QNAP host. GATEWAY_NS models the QNET OpenSurge
# namespace. A dedicated veth path models tun0 and terminates at a DNS server
# in UPSTREAM_NS. 10.77.1.254 is deliberately in the client's LAN prefix: it
# would be reached directly without the two L4 policy-routing stages.
sudo ip link add os-dns-tun type veth peer name os-dns-sink
sudo ip link set os-dns-tun netns "$GATEWAY_NS"
sudo ip link set os-dns-sink netns "$UPSTREAM_NS"
sudo ip -n "$GATEWAY_NS" addr add 10.77.3.1/30 dev os-dns-tun
sudo ip -n "$UPSTREAM_NS" addr add 10.77.3.2/30 dev os-dns-sink
sudo ip -n "$GATEWAY_NS" link set os-dns-tun up
sudo ip -n "$UPSTREAM_NS" link set os-dns-sink up
sudo ip -n "$UPSTREAM_NS" addr add 10.77.1.254/32 dev lo
sudo ip -n "$UPSTREAM_NS" route replace 10.77.1.2/32 via 10.77.3.1 dev os-dns-sink

sudo ip -n "$GATEWAY_NS" route replace default via 10.77.3.2 dev os-dns-tun table 20243 proto 243
sudo ip -n "$GATEWAY_NS" rule add pref 20239 from 10.77.1.2/32 iif os-gw-lan ipproto udp dport 53 table 20243
sudo ip -n "$GATEWAY_NS" rule add pref 20240 from 10.77.1.2/32 iif os-gw-lan ipproto tcp dport 53 table 20243

sudo ip -n "$CLIENT_NS" route replace default via 10.77.1.1 dev os-client table 20242 proto 242
sudo ip -n "$CLIENT_NS" rule add pref 24090 iif lo ipproto udp dport 53 table 20242
sudo ip -n "$CLIENT_NS" rule add pref 24091 iif lo ipproto tcp dport 53 table 20242
sudo ip -n "$CLIENT_NS" rule add pref 24100 iif lo table main suppress_prefixlength 0
sudo ip -n "$CLIENT_NS" rule add pref 24110 iif lo table 20242

# The gateway-side lookup can be simulated because 10.77.1.2 is a forwarded
# source in GATEWAY_NS. It must choose the DNS-only table, while an ordinary
# same-LAN source must stay on the LAN route.
gateway_dns_route="$(sudo ip -n "$GATEWAY_NS" -4 route get 10.77.1.254 from 10.77.1.2 iif os-gw-lan ipproto udp dport 53)"
grep -q 'via 10.77.3.2' <<<"$gateway_dns_route"
grep -q 'dev os-dns-tun' <<<"$gateway_dns_route"
grep -q 'table 20243' <<<"$gateway_dns_route"

ordinary_client_route="$(sudo ip -n "$GATEWAY_NS" -4 route get 10.77.1.254 from 10.77.1.3 iif os-gw-lan ipproto udp dport 53)"
grep -q 'dev os-gw-lan' <<<"$ordinary_client_route"
if grep -q 'table 20243' <<<"$ordinary_client_route"; then
  echo "NAS DNS policy leaked onto an ordinary same-LAN client" >&2
  exit 1
fi

# Use real UDP and TCP DNS queries rather than `ip route get ... iif lo`: the
# latter models an ingress lookup and Linux rejects a locally-owned source with
# an explicit loopback iif. Real local sockets are exactly what `iif lo` rules
# are intended to select.
DNSMASQ_PIDFILE=/tmp/opensurge-lab-host-dns.pid
rm -f "$DNSMASQ_PIDFILE"
sudo ip netns exec "$UPSTREAM_NS" dnsmasq \
  --conf-file=/dev/null \
  --no-resolv \
  --no-hosts \
  --bind-interfaces \
  --listen-address=10.77.1.254 \
  --port=53 \
  --address=/probe.opensurge.test/203.0.113.77 \
  --pid-file="$DNSMASQ_PIDFILE"

udp_answer="$(sudo ip netns exec "$CLIENT_NS" dig @10.77.1.254 probe.opensurge.test A +short +time=2 +tries=1)"
tcp_answer="$(sudo ip netns exec "$CLIENT_NS" dig @10.77.1.254 probe.opensurge.test A +tcp +short +time=2 +tries=1)"
test "$udp_answer" = "203.0.113.77"
test "$tcp_answer" = "203.0.113.77"

# A non-DNS same-LAN lookup remains direct; the NAS takeover must not detour
# QTS/LAN management traffic through its proxy table.
host_lan_route="$(sudo ip -n "$CLIENT_NS" -4 route get 10.77.1.1 from 10.77.1.2)"
grep -q 'dev os-client' <<<"$host_lan_route"

# Exact teardown mirrors product ownership: dedicated priorities plus route
# protocol only. No global rule/route flush and no nftables mutation.
if [[ -s "$DNSMASQ_PIDFILE" ]]; then
  sudo kill "$(cat "$DNSMASQ_PIDFILE")" 2>/dev/null || true
fi
rm -f "$DNSMASQ_PIDFILE"
sudo ip -n "$CLIENT_NS" rule del pref 24090 iif lo ipproto udp dport 53 table 20242
sudo ip -n "$CLIENT_NS" rule del pref 24091 iif lo ipproto tcp dport 53 table 20242
sudo ip -n "$CLIENT_NS" rule del pref 24110 iif lo table 20242
sudo ip -n "$CLIENT_NS" rule del pref 24100 iif lo table main suppress_prefixlength 0
sudo ip -n "$CLIENT_NS" route flush table 20242 proto 242
sudo ip -n "$GATEWAY_NS" rule del pref 20239 from 10.77.1.2/32 iif os-gw-lan ipproto udp dport 53 table 20243
sudo ip -n "$GATEWAY_NS" rule del pref 20240 from 10.77.1.2/32 iif os-gw-lan ipproto tcp dport 53 table 20243
sudo ip -n "$GATEWAY_NS" route flush table 20243 proto 243
sudo ip -n "$GATEWAY_NS" link del os-dns-tun

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
