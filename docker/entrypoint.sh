#!/bin/sh
set -eu

DATA_DIR="${OPENSURGE_DATA_DIR:-/data}"
CONFIG_PATH="${OPENSURGE_CONFIG:-${DATA_DIR}/config/opensurge.yaml}"
STORE_DIR="${OPENSURGE_STORE:-${DATA_DIR}/control}"
WEB_ADDR="${OPENSURGE_WEB_ADDR:-0.0.0.0:8080}"
CONTROL_ADDR="${OPENSURGE_CONTROL_ADDR:-127.0.0.1:61767}"
ALLOWED_HOSTS="${OPENSURGE_ALLOWED_HOSTS:-}"
SEED_LAN_IP="${OPENSURGE_SEED_LAN_IP:-192.168.50.2}"
SEED_LAN_CIDR="${OPENSURGE_SEED_LAN_CIDR:-192.168.50.0/24}"
SEED_UPSTREAM_GATEWAY="${OPENSURGE_SEED_UPSTREAM_GATEWAY:-192.168.50.1}"
CONTAINER_INTERFACE="${OPENSURGE_CONTAINER_INTERFACE:-eth0}"

fatal() {
  echo "OpenSurge entrypoint: $*" >&2
  exit 1
}

validate_seed_tokens() {
  case "$SEED_LAN_IP" in
    ''|*[!0-9.]*) fatal "invalid OPENSURGE_SEED_LAN_IP" ;;
  esac
  case "$SEED_UPSTREAM_GATEWAY" in
    ''|*[!0-9.]*) fatal "invalid OPENSURGE_SEED_UPSTREAM_GATEWAY" ;;
  esac
  case "$SEED_LAN_CIDR" in
    ''|*[!0-9./]*) fatal "invalid OPENSURGE_SEED_LAN_CIDR" ;;
  esac
  case "$SEED_LAN_CIDR" in
    */*) SEED_PREFIX=${SEED_LAN_CIDR##*/} ;;
    *) fatal "OPENSURGE_SEED_LAN_CIDR must contain a prefix length" ;;
  esac
  case "$SEED_PREFIX" in
    ''|*[!0-9]*) fatal "invalid prefix length in OPENSURGE_SEED_LAN_CIDR" ;;
  esac
  [ "$SEED_PREFIX" -ge 0 ] && [ "$SEED_PREFIX" -le 32 ] \
    || fatal "OPENSURGE_SEED_LAN_CIDR prefix must be between 0 and 32"
  case "$CONTAINER_INTERFACE" in
    ''|*[!A-Za-z0-9_.:@-]*) fatal "invalid OPENSURGE_CONTAINER_INTERFACE" ;;
  esac
}

seed_config() {
  validate_seed_tokens
  tmp="${CONFIG_PATH}.seed.$$"
  trap 'rm -f "$tmp"' EXIT HUP INT TERM

  sed \
    -e "s|^  interface: \"eth0\"$|  interface: \"${CONTAINER_INTERFACE}\"|" \
    -e "s|^  lan_ip: \"192.168.50.2\"$|  lan_ip: \"${SEED_LAN_IP}\"|" \
    -e "s|^  lan_prefix_len: 24$|  lan_prefix_len: ${SEED_PREFIX}|" \
    -e "s|^  lan_cidr: \"192.168.50.0/24\"$|  lan_cidr: \"${SEED_LAN_CIDR}\"|" \
    -e "s|^  upstream_interface: \"eth0\"$|  upstream_interface: \"${CONTAINER_INTERFACE}\"|" \
    -e "s|^  upstream_gateway: \"192.168.50.1\"$|  upstream_gateway: \"${SEED_UPSTREAM_GATEWAY}\"|" \
    -e "s|^  listen: \"192.168.50.2\"$|  listen: \"${SEED_LAN_IP}\"|" \
    /usr/share/opensurge/config.qnap-docker.yaml > "$tmp"

  grep -Fq "lan_ip: \"${SEED_LAN_IP}\"" "$tmp" \
    || fatal "failed to render seeded LAN IP"
  grep -Fq "lan_cidr: \"${SEED_LAN_CIDR}\"" "$tmp" \
    || fatal "failed to render seeded LAN CIDR"
  grep -Fq "upstream_gateway: \"${SEED_UPSTREAM_GATEWAY}\"" "$tmp" \
    || fatal "failed to render seeded upstream gateway"
  grep -Fq "interface: \"${CONTAINER_INTERFACE}\"" "$tmp" \
    || fatal "failed to render seeded container interface"

  chmod 600 "$tmp"
  mv "$tmp" "$CONFIG_PATH"
  trap - EXIT HUP INT TERM
  echo "Seeded ${CONFIG_PATH} from Docker deployment values. Existing configs are never overwritten." >&2
}

umask 077
mkdir -p \
  "${DATA_DIR}/config" \
  "${DATA_DIR}/profiles" \
  "${DATA_DIR}/providers" \
  "${DATA_DIR}/runtime" \
  "${DATA_DIR}/state" \
  "${DATA_DIR}/logs" \
  "${DATA_DIR}/backups" \
  "${DATA_DIR}/licenses" \
  "${STORE_DIR}"

if [ ! -f "${CONFIG_PATH}" ]; then
  seed_config
fi

exec /usr/local/bin/opensurge-container \
  --config "${CONFIG_PATH}" \
  --store "${STORE_DIR}" \
  --control-addr "${CONTROL_ADDR}" \
  --web-addr "${WEB_ADDR}" \
  --allowed-hosts "${ALLOWED_HOSTS}"
