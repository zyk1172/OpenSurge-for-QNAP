#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ENV_FILE="$SCRIPT_DIR/.env"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
STATIC_ONLY=0
LIST_INTERFACES=0

usage() {
  cat <<'EOF'
Usage: sh ./preflight.sh [--env-file PATH] [--compose-file PATH] [--static] [--list-interfaces]

Checks the NAS Docker deployment before `docker compose up`.

  --env-file PATH      Read deployment values from PATH instead of deploy/qnap/.env
  --static             Run only non-host-specific checks. Intended for CI.
  --list-interfaces    Show QNAP host NIC/bridge candidates and exit. Use this
                       before setting OPENSURGE_PARENT_INTERFACE on a dual-NIC NAS.

The host-specific run also performs a real bind-mount permission probe with the
OpenSurge image. It verifies root persistence semantics and then verifies that
the configured unprivileged Web UID/GID can create, rename and remove files in
/data/web-auth. This catches QTS/QuTS ACL problems that a host-side `[ -w ]`
check cannot detect.
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
    --compose-file)
      [ "$#" -ge 2 ] || { echo "--compose-file requires a path" >&2; exit 2; }
      COMPOSE_FILE=$2
      shift 2
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

info() {
  echo "[INFO] $*"
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

list_interfaces() {
  echo "NAS host network interfaces / container LAN parent candidates"
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

  fail "neither ip nor ifconfig is available; inspect the NAS network settings instead"
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

validate_numeric_identity() {
  name=$1
  value=$2
  is_uint "$value" || fail "$name must be a numeric uid/gid, got: $value"
  [ "$value" -le 2147483647 ] || fail "$name is outside the supported numeric range"
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
    case "$octet" in 0|[1-9]|[1-9][0-9]|[1-9][0-9][0-9]) ;; *) return 1 ;; esac
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
  case "$prefix" in 0|[1-9]|[1-9][0-9]) ;; *) fail "invalid CIDR prefix: $prefix" ;; esac
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
  [ "$subnet_i" -eq "$network" ] || fail "OPENSURGE_SUBNET must use the canonical network address"
  [ "$prefix" -le 30 ] || fail "same-LAN NAS deployment requires a subnet prefix of 0..30"

  if [ "$prefix" -le 30 ]; then
    broadcast=$((network | (4294967295 ^ mask)))
    [ "$ip_i" -ne "$network" ] || fail "OPENSURGE_IP must not be the subnet network address"
    [ "$ip_i" -ne "$broadcast" ] || fail "OPENSURGE_IP must not be the subnet broadcast address"
    [ "$gateway_i" -ne "$network" ] && [ "$gateway_i" -ne "$broadcast" ] || fail "OPENSURGE_GATEWAY must be a usable host address"
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
  if [ "$NAS_PLATFORM" != qnap ]; then
    case "$value" in
      /*/*/*) ;;
      *) fail "OPENSURGE_DATA_PATH must be an absolute dedicated NAS subdirectory" ;;
    esac
    case "$value" in
      */../*|*/./*|*/..|*/.|*/|*:*|*','*|*' '*|/volume[0-9]/docker|/srv/opensurge)
        fail "OPENSURGE_DATA_PATH must be a dedicated directory without ambiguous mount syntax: $value" ;;
    esac
    return
  fi
  case "$value" in
    /share/*) ;;
    /*) warn "OPENSURGE_DATA_PATH is absolute but not under /share; verify this is intentional on QNAP: $value" ;;
    *) fail "OPENSURGE_DATA_PATH must be an absolute QNAP path, e.g. /share/Container/opensurge" ;;
  esac
  case "$value" in
    /|/share|/share/Container)
      fail "OPENSURGE_DATA_PATH must be a dedicated subdirectory, not $value"
      ;;
  esac
}

numeric_owner() {
  target=$1
  if stat -c '%u:%g' "$target" >/dev/null 2>&1; then
    stat -c '%u:%g' "$target"
    return 0
  fi
  if stat -f '%u:%g' "$target" >/dev/null 2>&1; then
    stat -f '%u:%g' "$target"
    return 0
  fi
  return 1
}

permission_probe() {
  image=$1
  data_path=$2
  web_uid=$3
  web_gid=$4

  docker image inspect "$image" >/dev/null 2>&1 \
    || fail "OpenSurge image is not loaded locally: $image (pull or docker load the selected image first)"
  ok "Permission probe image exists locally: $image"

  # Root/control-plane persistence semantics. This deliberately exercises the
  # bind mount from inside a container rather than trusting the SSH account's
  # host-side write bit.
  if ! docker run --rm \
      --entrypoint /bin/sh \
      -v "$data_path:/data" \
      "$image" -c '
        set -eu
        probe="/data/.opensurge-root-probe-$$"
        umask 077
        mkdir "$probe"
        printf "%s\n" root-probe > "$probe/value.tmp"
        sync "$probe/value.tmp" 2>/dev/null || sync
        mv "$probe/value.tmp" "$probe/value"
        test -s "$probe/value"
        rm -rf "$probe"
      '; then
    fail "container root cannot safely create/rename/delete files in $data_path; check NAS shared-folder permissions/ACLs and storage health"
  fi
  ok "NAS bind mount supports container root persistence semantics"

  # Prepare only the OpenSurge-owned Web credential directory. Never chown the
  # whole /data tree: QNAP shared folders may carry QTS/QuTS ACL ownership that
  # other services rely on.
  if ! docker run --rm \
      --entrypoint /bin/sh \
      -v "$data_path:/data" \
      "$image" -c "
        set -eu
        mkdir -p /data/web-auth
        chown ${web_uid}:${web_gid} /data/web-auth
        chmod 700 /data/web-auth
      "; then
    fail "cannot assign /data/web-auth to ${web_uid}:${web_gid}; NAS ACLs may prohibit the requested ownership"
  fi

  if ! docker run --rm \
      --entrypoint /bin/sh \
      --user "${web_uid}:${web_gid}" \
      -v "$data_path:/data" \
      "$image" -c '
        set -eu
        probe="/data/web-auth/.opensurge-web-probe-$$"
        umask 077
        printf "%s\n" web-probe > "$probe.tmp"
        mv "$probe.tmp" "$probe"
        test -s "$probe"
        rm -f "$probe"
      '; then
    fail "Web uid:gid ${web_uid}:${web_gid} cannot use /data/web-auth. Run 'id <NAS-user>' on the NAS, set OPENSURGE_WEB_UID/GID to those numeric values, and review NAS shared-folder ACLs"
  fi
  ok "Web uid:gid ${web_uid}:${web_gid} can persist credentials through the real NAS bind mount"
}

[ -f "$ENV_FILE" ] || fail "environment file not found: $ENV_FILE (copy .env.example to .env first)"
command_exists printenv || fail "printenv command not found"
command_exists awk || fail "awk command not found"

NAS_PLATFORM=$(read_env OPENSURGE_NAS_PLATFORM qnap)
case "$NAS_PLATFORM" in
  qnap) NETWORK_DRIVER=qnet ;;
  synology|fnos|generic) NETWORK_DRIVER=macvlan ;;
  *) fail "OPENSURGE_NAS_PLATFORM must be qnap, synology, fnos or generic" ;;
esac
OPENSURGE_IP=$(read_env OPENSURGE_IP)
OPENSURGE_SUBNET=$(read_env OPENSURGE_SUBNET)
OPENSURGE_GATEWAY=$(read_env OPENSURGE_GATEWAY)
OPENSURGE_PARENT_INTERFACE=$(read_env OPENSURGE_PARENT_INTERFACE)
OPENSURGE_CONTAINER_INTERFACE=$(read_env OPENSURGE_CONTAINER_INTERFACE eth0)
OPENSURGE_DATA_PATH=$(read_env OPENSURGE_DATA_PATH)
OPENSURGE_WEB_UID=$(read_env OPENSURGE_WEB_UID 1000)
OPENSURGE_WEB_GID=$(read_env OPENSURGE_WEB_GID 100)

[ -n "$OPENSURGE_IP" ] || fail "OPENSURGE_IP is required"
[ -n "$OPENSURGE_SUBNET" ] || fail "OPENSURGE_SUBNET is required"
[ -n "$OPENSURGE_GATEWAY" ] || fail "OPENSURGE_GATEWAY is required"
[ -n "$OPENSURGE_PARENT_INTERFACE" ] || fail "OPENSURGE_PARENT_INTERFACE is required"
[ -n "$OPENSURGE_DATA_PATH" ] || fail "OPENSURGE_DATA_PATH is required"

validate_network "$OPENSURGE_IP" "$OPENSURGE_SUBNET" "$OPENSURGE_GATEWAY"
validate_interface_name "$OPENSURGE_PARENT_INTERFACE"
validate_interface_name "$OPENSURGE_CONTAINER_INTERFACE"
validate_data_path "$OPENSURGE_DATA_PATH"
validate_numeric_identity OPENSURGE_WEB_UID "$OPENSURGE_WEB_UID"
validate_numeric_identity OPENSURGE_WEB_GID "$OPENSURGE_WEB_GID"
[ "$OPENSURGE_WEB_UID" -gt 0 ] || fail "OPENSURGE_WEB_UID must not be root"
[ "$OPENSURGE_CONTAINER_INTERFACE" = eth0 ] || fail "single-network NAS Compose requires OPENSURGE_CONTAINER_INTERFACE=eth0"
ok "$NETWORK_DRIVER parent interface selected: $OPENSURGE_PARENT_INTERFACE"
ok "Container-side LAN interface: $OPENSURGE_CONTAINER_INTERFACE"
ok "Persistent /data bind mount: $OPENSURGE_DATA_PATH"
ok "Unprivileged Web identity: ${OPENSURGE_WEB_UID}:${OPENSURGE_WEB_GID}"

command_exists docker || fail "docker command not found"
compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose "$@"
  elif command_exists docker-compose; then
    docker-compose "$@"
  else
    fail "Docker Compose is unavailable (docker compose or docker-compose required)"
  fi
}

compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" config --quiet \
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
  *"$NETWORK_DRIVER"*) ok "$NETWORK_DRIVER Docker network driver is reported by Docker" ;;
  *)
    [ "$NETWORK_DRIVER" = qnet ] || fail "Docker does not advertise the macvlan network driver"
    warn "Docker did not advertise qnet; Compose creation remains the authoritative QNAP check." ;;
esac
if [ "$NETWORK_DRIVER" = macvlan ]; then
  docker_security=$(docker info --format '{{json .SecurityOptions}}')
  case "$docker_security" in *rootless*) fail "macvlan requires rootful Docker" ;; esac
  warn "macvlan isolates the NAS host from the container. Test Web and Gateway/DNS from another LAN device; NAS Host Takeover is not supported."
fi

[ -c /dev/net/tun ] || fail "/dev/net/tun is missing or is not a character device"
ok "/dev/net/tun is available"

if command_exists ip; then
  ip link show dev "$OPENSURGE_PARENT_INTERFACE" >/dev/null 2>&1 \
    || fail "$NETWORK_DRIVER parent interface does not exist: $OPENSURGE_PARENT_INTERFACE"
  ok "$NETWORK_DRIVER parent interface exists: $OPENSURGE_PARENT_INTERFACE"
  host_addresses=$(ip -o -4 addr show 2>/dev/null | awk '{split($4,a,"/"); print a[1]}')
  if printf '%s\n' "$host_addresses" | awk -v wanted="$OPENSURGE_IP" '$0 == wanted {found=1} END {exit !found}'; then
    fail "OPENSURGE_IP must not equal a NAS host address"
  fi
  selected_addr=$(ip -o -4 addr show dev "$OPENSURGE_PARENT_INTERFACE" 2>/dev/null || true)
  if [ -n "$selected_addr" ]; then
    info "Selected-interface IPv4: $selected_addr"
  else
    warn "No host IPv4 was reported directly on $OPENSURGE_PARENT_INTERFACE. This can be normal when QNAP uses a bridge/Virtual Switch, but verify the QNET parent in Network & Virtual Switch."
  fi
  gateway_route=$(ip -4 route get "$OPENSURGE_GATEWAY" 2>/dev/null | head -n 1 || true)
  if [ -n "$gateway_route" ]; then
    info "Host route to gateway: $gateway_route"
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
else
  parent=$(dirname -- "$data_path")
  [ -d "$parent" ] || fail "parent of OPENSURGE_DATA_PATH does not exist: $parent"
  mkdir -p "$data_path" 2>/dev/null \
    || fail "cannot create OPENSURGE_DATA_PATH under $parent; create a dedicated NAS shared-folder subdirectory with read/write permission first"
  ok "Created persistent data directory: $data_path"
fi

owner=$(numeric_owner "$data_path" 2>/dev/null || true)
if [ -n "$owner" ]; then
  info "NAS host numeric owner for data path: $owner"
  case "$owner" in
    "${OPENSURGE_WEB_UID}:${OPENSURGE_WEB_GID}") ;;
    *) warn "Data-root owner $owner differs from Web ${OPENSURGE_WEB_UID}:${OPENSURGE_WEB_GID}; this is allowed because only /data/web-auth is Web-writable, but the real container probe below must pass." ;;
  esac
fi

probe_image=$(compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" config --images 2>/dev/null | head -n 1 || true)
[ -n "$probe_image" ] || probe_image=opensurge-for-qnap:test
permission_probe "$probe_image" "$data_path" "$OPENSURGE_WEB_UID" "$OPENSURGE_WEB_GID"
if [ "$NETWORK_DRIVER" = macvlan ]; then
  reported_platform=$(docker run --rm --network none \
    -e "OPENSURGE_NAS_PLATFORM=$NAS_PLATFORM" \
    --entrypoint /usr/local/bin/opensurge-container "$probe_image" --component platform) \
    || fail "selected image lacks multi-NAS support; load an image built from this revision or a newer release"
  [ "$reported_platform" = "$NAS_PLATFORM" ] || fail "image does not honor OPENSURGE_NAS_PLATFORM"
  # All kernel probes run in a disposable container namespace, never on NAS routing.
  docker run --rm --network none --cap-add NET_ADMIN --cap-add NET_RAW \
    -e OPENSURGE_DISPOSABLE_PROBE=1 \
    --device /dev/net/tun:/dev/net/tun --security-opt no-new-privileges:true \
    --sysctl net.ipv4.ip_forward=1 --sysctl net.ipv4.conf.all.rp_filter=0 \
    --sysctl net.ipv4.conf.default.rp_filter=0 \
    --sysctl net.ipv6.conf.all.disable_ipv6=1 \
    --entrypoint /bin/sh "$probe_image" /usr/share/opensurge/nas-kernel-probe.sh \
    || fail "NAS kernel cannot provide macvlan / TUN / ingress-interface policy routing"
  ok "Disposable namespace TUN and policy-routing probe passed"
fi

if command_exists ping; then
  if ping -c 1 -W 1 "$OPENSURGE_IP" >/dev/null 2>&1; then
    fail "OPENSURGE_IP $OPENSURGE_IP already responds on the LAN; choose another address or verify ownership"
  fi
  warn "No ping reply from $OPENSURGE_IP. This reduces obvious conflicts but does not prove the address is free."
else
  warn "ping command is unavailable; static IP conflict was not probed"
fi

ok "$NAS_PLATFORM deployment preflight complete"
