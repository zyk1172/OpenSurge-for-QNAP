#!/bin/sh
set -eu

ROLE="${OPENSURGE_ROLE:-gateway}"
[ "$ROLE" = "gateway" ] || {
  echo "OpenSurge entrypoint: only OPENSURGE_ROLE=gateway is supported by the QNAP image" >&2
  exit 1
}

DATA_DIR="${OPENSURGE_DATA_DIR:-/data}"
CONFIG_PATH="${OPENSURGE_CONFIG:-${DATA_DIR}/config/opensurge.yaml}"
STORE_DIR="${OPENSURGE_STORE:-${DATA_DIR}/control}"
AUTH_DIR="${OPENSURGE_AUTH_DIR:-${DATA_DIR}/web-auth}"
WEB_ADDR="${OPENSURGE_WEB_ADDR:-0.0.0.0:8080}"
CONTROL_ADDR="${OPENSURGE_CONTROL_ADDR:-127.0.0.1:61767}"
ALLOWED_HOSTS="${OPENSURGE_ALLOWED_HOSTS:-}"
SECURE_COOKIES="${OPENSURGE_SECURE_COOKIES:-false}"
SEED_LAN_IP="${OPENSURGE_SEED_LAN_IP:-192.168.50.2}"
SEED_LAN_CIDR="${OPENSURGE_SEED_LAN_CIDR:-192.168.50.0/24}"
SEED_UPSTREAM_GATEWAY="${OPENSURGE_SEED_UPSTREAM_GATEWAY:-192.168.50.1}"
CONTAINER_INTERFACE="${OPENSURGE_CONTAINER_INTERFACE:-eth0}"
# QNAP commonly assigns the first normal NAS account UID 1000 and the
# `everyone` group GID 100, but installations differ. These values are fully
# configurable and deploy/qnap/preflight.sh verifies them against the real bind
# mount before the supported deployment is started.
WEB_UID="${OPENSURGE_WEB_UID:-1000}"
WEB_GID="${OPENSURGE_WEB_GID:-100}"

fatal() {
  echo "OpenSurge entrypoint: $*" >&2
  exit 1
}

validate_uint() {
  case "$2" in
    ''|*[!0-9]*) fatal "$1 must be a numeric uid/gid, got: $2" ;;
  esac
  [ "$2" -ge 0 ] && [ "$2" -le 2147483647 ] || fatal "$1 is outside the supported numeric range"
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
  echo "Seeded ${CONFIG_PATH} from Web deployment values. Existing configs are never overwritten." >&2
}

validate_uint OPENSURGE_WEB_UID "$WEB_UID"
validate_uint OPENSURGE_WEB_GID "$WEB_GID"

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
  "${STORE_DIR}" \
  "${AUTH_DIR}"

if [ ! -f "${CONFIG_PATH}" ]; then
  seed_config
fi

# The LAN-facing HTTP process must not retain the gateway's NET_ADMIN/NET_RAW
# capabilities. Its only persistent write surface is the dedicated auth dir.
# Never recursively chown /data: on QNAP that bind mount may carry QTS/QuTS
# ACLs and user ownership that must remain intact. Only the OpenSurge-owned
# web-auth directory is assigned to the configured unprivileged identity.
if ! chown "${WEB_UID}:${WEB_GID}" "${AUTH_DIR}"; then
  fatal "cannot assign ${AUTH_DIR} to ${WEB_UID}:${WEB_GID}; check QNAP shared-folder ACLs and run deploy/qnap/preflight.sh"
fi
if ! chmod 700 "${AUTH_DIR}"; then
  fatal "cannot set secure permissions on ${AUTH_DIR}; check QNAP shared-folder ACLs"
fi
if ! setpriv --reuid="$WEB_UID" --regid="$WEB_GID" --clear-groups --no-new-privs \
  /bin/sh -c 'test -r "$1" && test -w "$1" && test -x "$1"' sh "${AUTH_DIR}"; then
  fatal "${AUTH_DIR} is not usable by Web uid:gid ${WEB_UID}:${WEB_GID}; QNAP ACL/ownership conflicts with OPENSURGE_WEB_UID/GID"
fi

if [ ! -f "${AUTH_DIR}/admin.json" ] && [ -f "${STORE_DIR}/admin.json" ]; then
  cp "${STORE_DIR}/admin.json" "${AUTH_DIR}/admin.json"
  chmod 600 "${AUTH_DIR}/admin.json"
  chown "${WEB_UID}:${WEB_GID}" "${AUTH_DIR}/admin.json"
  echo "Migrated legacy Web administrator credentials to ${AUTH_DIR}." >&2
fi

CONTROL_TOKEN="$(/usr/local/bin/opensurge-container --component token --store "${STORE_DIR}")"
[ -n "$CONTROL_TOKEN" ] || fatal "internal control token is empty"

/usr/local/bin/opensurge-container \
  --component control \
  --config "${CONFIG_PATH}" \
  --store "${STORE_DIR}" \
  --control-addr "${CONTROL_ADDR}" &
CONTROL_PID=$!

set -- /usr/local/bin/opensurge-container \
  --component web \
  --store "${STORE_DIR}" \
  --auth-dir "${AUTH_DIR}" \
  --control-addr "${CONTROL_ADDR}" \
  --control-token "${CONTROL_TOKEN}" \
  --web-addr "${WEB_ADDR}" \
  --allowed-hosts "${ALLOWED_HOSTS}" \
  --require-bootstrap-token \
  --qnap-only
case "$(printf '%s' "$SECURE_COOKIES" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes|on) set -- "$@" --secure-cookies ;;
  0|false|no|off|'') ;;
  *) kill -TERM "$CONTROL_PID" 2>/dev/null || true; fatal "OPENSURGE_SECURE_COOKIES must be a boolean" ;;
esac

setpriv \
  --reuid="$WEB_UID" \
  --regid="$WEB_GID" \
  --clear-groups \
  --bounding-set=-all \
  --inh-caps=-all \
  --ambient-caps=-all \
  --no-new-privs \
  "$@" &
WEB_PID=$!

shutdown_children() {
  kill -TERM "$WEB_PID" "$CONTROL_PID" 2>/dev/null || true
  wait "$WEB_PID" 2>/dev/null || true
  wait "$CONTROL_PID" 2>/dev/null || true
}

trap 'shutdown_children; exit 0' INT TERM HUP

# One Docker container, two privilege domains. If either process exits
# unexpectedly, stop the sibling and let Docker's restart policy recover both.
while kill -0 "$CONTROL_PID" 2>/dev/null && kill -0 "$WEB_PID" 2>/dev/null; do
  sleep 1
done

echo "OpenSurge entrypoint: a supervised component exited; restarting the container" >&2
shutdown_children
exit 1
