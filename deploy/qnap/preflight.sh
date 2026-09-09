#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$SCRIPT_DIR/.env"
STATIC_ONLY=0
LIST_INTERFACES=0

usage() {
  cat <<'EOF'
Usage: sh ./preflight.sh [--env-file PATH] [--static] [--list-interfaces]

Checks the QNAP Docker deployment before `docker compose up`.

  --env-file PATH      Read deployment values from PATH instead of deploy/qnap/.env
  --static             Run only non-host-specific checks. Intended for CI.
  --list-interfaces    Show QNAP host NIC/bridge candidates and exit. Use this
                       before setting OPENSURGE_PARENT_INTERFACE on a dual-NIC NAS.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --env-file)
      [ "$#" -ge 2 ] || { echo "--env-file requires a path" >&2; exit 2; }
      ENV_FILE=$2
      shift 2
      ;;
    --static)
      STATIC_ONLY=1
      shift
      ;;
    --list-interfaces)
      LIST_INTERFACES=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  echo "[FAIL] $*" >&2
  exit 1
}

ok() {
  echo "[ OK ] $*"
}

warn() {
  echo "[WARN] $*" >&2
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

list_interfaces() {
  echo "QNAP host network interfaces / QNET parent candidates"
  echo "----------------------------------------------------"
  if command_exists ip; then
    echo
    echo "Links:"
    ip -o link show 2>/dev/null || ip link show || true
    echo
    echo "IPv4 addresses:"
    ip -o -4 addr show 2>/dev/null || ip -4 addr show || true
    echo
    echo "Default routes:"
    ip -4 route show default 2>/dev/null || true
    echo
    echo "Choose the host NIC/bridge connected to the LAN that should carry OpenSurge traffic."
    echo "Examples on QNAP may include eth0, eth1, bond0 or br0."
    return 0
  fi

  if command_exists ifconfig; then
    ifconfig -a
    echo
    echo "Choose the adapter/bridge connected to the target LAN and use its exact name."
    return 0
  fi

  fail "neither ip nor ifconfig is available; inspect QNAP Network & Virtual Switch instead"
}

if [ "$LIST_INTERFACES" -eq 1 ]; then
  list_interfaces
  exit 0
fi

read_env() {
  key=$1
  default=${2-}

  current=$(printenv "$key" 2>/dev/null || true)
  if [ -n "$current" ]; then
    printf '%s\n' "$current"
    return 0
  fi

  if [ -f "$ENV_FILE" ]; then
    value=$(awk -F= -v wanted="$key" '
      $0 ~ /^[[:space:]]*#/ { next }
      {
        k=$1
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", k)
        if (k == wanted) {
          sub(/^[^=]*=/, "", $0)
          sub(/\r$/, "", $0)
          print $0
          exit
        }
      }
    ' "$ENV_FILE")
    if [ -n "$value" ]; then
      printf '%s\n' "$value"
      return 0
    fi
  fi

  printf '%s\n' "$default"
}

is_uint() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
    *) return 0 ;;
  esac
}

ipv4_to_int() {
  ip=$1
  old_ifs=$IFS
  IFS=.
  set -- $ip
  IFS=$old_ifs
  [ "$#" -eq 4 ] || return 1

  value=0
  for octet in "$@"; do
    is_uint "$octet" || return 1
    [ "$octet" -ge 0 ] && [ "$octet" -le 255 ] || return 1
    value=$((value * 256 + octet))
  done
  printf '%s\n' "$value"
}

validate_network() {
  ip=$1
  cidr=$2
  gateway=$3

  case "$cidr" in
    */*) subnet_ip=${cidr%/*}; prefix=${cidr#*/} ;;
    *) fail "OPENSURGE_SUBNET must be IPv4 CIDR, got: $cidr" ;;
  esac

  is_uint "$prefix" || fail "invalid CIDR prefix: $prefix"
  [ "$prefix" -ge 0 ] && [ "$prefix" -le 32 ] || fail "CIDR prefix must be 0..32"

  ip_i=$(ipv4_to_int "$ip") || fail "invalid OPENSURGE_IP: $ip"
  subnet_i=$(ipv4_to_int "$subnet_ip") || fail "invalid OPENSURGE_SUBNET address: $subnet_ip"
  gateway_i=$(ipv4_to_int "$gateway") || fail "invalid OPENSURGE_GATEWAY: $gateway"

  if [ "$prefix" -eq 0 ]; then
    mask=0
  else
    mask=$(( (4294967295 << (32 - prefix)) & 4294967295 ))
  fi

  network=$((subnet_i & mask))
  [ $((ip_i & mask)) -eq "$network" ] || fail "OPENSURGE_IP $ip is outside $cidr"
  [ $((gateway_i & mask)) -eq "$network" ] || fail "OPENSURGE_GATEWAY $gateway is outside $cidr"
  [ "$ip_i" -ne "$gateway_i" ] || fail "OPENSURGE_IP must not equal OPENSURGE_GATEWAY"

  if [ "$prefix" -le 30 ]; then
    broadcast=$((network | (4294967295 ^ mask)))
    [ "$ip_i" -ne "$network" ] || fail "OPENSURGE_IP must not be the subnet network address"
    [ "$ip_i" -ne "$broadcast" ] || fail "OPENSURGE_IP must not be the subnet broadcast address"
  fi

  ok "IPv4 topology is internally consistent: $ip in $cidr via $gateway"
}

validate_interface_name() {
  value=$1
  case "$value" in
    ''|*[!A-Za-z0-9_.:@-]*) fail "invalid interface name: $value" ;;
  esac
}

validate_data_path() {
  value=$1
  case "$value" in
    /*) ;;
    *) fail "OPENSURGE_DATA_PATH must be an absolute QNAP path, e.g. /share/Container/opensurge" ;;
  esac
  case "$value" in
    /|/share|/share/Container)
      fail "OPENSURGE_DATA_PATH must be a dedicated subdirectory, not $value"
      ;;
  esac
}

[ -f "$ENV_FILE" ] || fail "environment file not found: $ENV_FILE (copy .env.example to .env first)"
command_exists printenv || fail "printenv command not found"
command_exists awk || fail "awk command not found"

OPENSURGE_IP=$(read_env OPENSURGE_IP)
OPENSURGE_SUBNET=$(read_env OPENSURGE_SUBNET)
OPENSURGE_GATEWAY=$(read_env OPENSURGE_GATEWAY)
OPENSURGE_PARENT_INTERFACE=$(read_env OPENSURGE_PARENT_INTERFACE)
OPENSURGE_CONTAINER_INTERFACE=$(read_env OPENSURGE_CONTAINER_INTERFACE eth0)
OPENSURGE_DATA_PATH=$(read_env OPENSURGE_DATA_PATH)

[ -n "$OPENSURGE_IP" ] || fail "OPENSURGE_IP is required"
[ -n "$OPENSURGE_SUBNET" ] || fail "OPENSURGE_SUBNET is required"
[ -n "$OPENSURGE_GATEWAY" ] || fail "OPENSURGE_GATEWAY is required"
[ -n "$OPENSURGE_PARENT_INTERFACE" ] || fail "OPENSURGE_PARENT_INTERFACE is required"
[ -n "$OPENSURGE_DATA_PATH" ] || fail "OPENSURGE_DATA_PATH is required"

validate_network "$OPENSURGE_IP" "$OPENSURGE_SUBNET" "$OPENSURGE_GATEWAY"
validate_interface_name "$OPENSURGE_PARENT_INTERFACE"
validate_interface_name "$OPENSURGE_CONTAINER_INTERFACE"
validate_data_path "$OPENSURGE_DATA_PATH"
ok "QNET parent interface selected: $OPENSURGE_PARENT_INTERFACE"
ok "Container-side LAN interface: $OPENSURGE_CONTAINER_INTERFACE"
ok "Persistent /data bind mount: $OPENSURGE_DATA_PATH"

command_exists docker || fail "docker command not found"
docker compose version >/dev/null 2>&1 || fail "Docker Compose plugin is unavailable"

docker compose --env-file "$ENV_FILE" -f "$SCRIPT_DIR/docker-compose.yml" config --quiet \
  || fail "docker-compose.yml does not resolve with $ENV_FILE"
ok "Compose configuration resolves successfully"

if [ "$STATIC_ONLY" -eq 1 ]; then
  ok "Static preflight complete"
  exit 0
fi

docker info >/dev/null 2>&1 || fail "Docker daemon is unavailable"
ok "Docker daemon is reachable"

network_plugins=$(docker info --format '{{json .Plugins.Network}}' 2>/dev/null || true)
case "$network_plugins" in
  *qnet*) ok "QNAP qnet Docker network driver is reported by Docker" ;;
  *) warn "Docker did not advertise qnet in its network-plugin list. Some QNAP builds do not expose third-party drivers consistently here; Compose creation remains the authoritative qnet check." ;;
esac

[ -c /dev/net/tun ] || fail "/dev/net/tun is missing or is not a character device"
ok "/dev/net/tun is available"

if command_exists ip; then
  ip link show dev "$OPENSURGE_PARENT_INTERFACE" >/dev/null 2>&1 \
    || fail "QNET parent interface does not exist: $OPENSURGE_PARENT_INTERFACE"
  ok "QNET parent interface exists: $OPENSURGE_PARENT_INTERFACE"
  selected_addr=$(ip -o -4 addr show dev "$OPENSURGE_PARENT_INTERFACE" 2>/dev/null || true)
  if [ -n "$selected_addr" ]; then
    echo "[INFO] Selected-interface IPv4: $selected_addr"
  else
    warn "No host IPv4 was reported directly on $OPENSURGE_PARENT_INTERFACE. This can be normal when QNAP uses a bridge/Virtual Switch, but verify the QNET parent in Network & Virtual Switch."
  fi
  gateway_route=$(ip -4 route get "$OPENSURGE_GATEWAY" 2>/dev/null | head -n 1 || true)
  if [ -n "$gateway_route" ]; then
    echo "[INFO] Host route to gateway: $gateway_route"
    case " $gateway_route " in
      *" dev $OPENSURGE_PARENT_INTERFACE "*) ok "Host route to the gateway uses the selected parent interface" ;;
      *) warn "The host route to $OPENSURGE_GATEWAY does not name $OPENSURGE_PARENT_INTERFACE. QNAP bridge/Virtual Switch naming may explain this; verify the selected adapter before deployment." ;;
    esac
  fi
elif command_exists ifconfig; then
  ifconfig "$OPENSURGE_PARENT_INTERFACE" >/dev/null 2>&1 \
    || fail "QNET parent interface does not exist: $OPENSURGE_PARENT_INTERFACE"
  ok "QNET parent interface exists: $OPENSURGE_PARENT_INTERFACE"
  ifconfig "$OPENSURGE_PARENT_INTERFACE" || true
else
  warn "Neither ip nor ifconfig is available; parent-interface existence was not verified"
fi

data_path=$OPENSURGE_DATA_PATH
if [ -e "$data_path" ]; then
  [ -d "$data_path" ] || fail "OPENSURGE_DATA_PATH exists but is not a directory: $data_path"
  [ -w "$data_path" ] || fail "OPENSURGE_DATA_PATH is not writable: $data_path"
  ok "Persistent data directory is writable: $data_path"
else
  parent=$(dirname -- "$data_path")
  [ -d "$parent" ] || fail "parent of OPENSURGE_DATA_PATH does not exist: $parent"
  [ -w "$parent" ] || fail "cannot create OPENSURGE_DATA_PATH under: $parent"
  warn "Persistent data directory does not exist yet. Create it explicitly before deployment: mkdir -p '$data_path'"
fi

if command_exists ping; then
  if ping -c 1 -W 1 "$OPENSURGE_IP" >/dev/null 2>&1; then
    fail "OPENSURGE_IP $OPENSURGE_IP already responds on the LAN; choose another address or verify ownership"
  fi
  warn "No ping reply from $OPENSURGE_IP. This reduces obvious conflicts but does not prove the address is free."
else
  warn "ping command is unavailable; static IP conflict was not probed"
fi

ok "QNAP deployment preflight complete"
