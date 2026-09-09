#!/bin/sh
set -eu

DATA_DIR="${OPENSURGE_DATA_DIR:-/data}"
CONFIG_PATH="${OPENSURGE_CONFIG:-${DATA_DIR}/config/opensurge.yaml}"
STORE_DIR="${OPENSURGE_STORE:-${DATA_DIR}/control}"
WEB_ADDR="${OPENSURGE_WEB_ADDR:-0.0.0.0:8080}"
CONTROL_ADDR="${OPENSURGE_CONTROL_ADDR:-127.0.0.1:61767}"
ALLOWED_HOSTS="${OPENSURGE_ALLOWED_HOSTS:-}"

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
  cp /usr/share/opensurge/config.qnap-docker.yaml "${CONFIG_PATH}"
  chmod 600 "${CONFIG_PATH}"
  echo "Seeded ${CONFIG_PATH}. Configure the LAN values in the Web UI before starting the gateway." >&2
fi

exec /usr/local/bin/opensurge-container \
  --config "${CONFIG_PATH}" \
  --store "${STORE_DIR}" \
  --control-addr "${CONTROL_ADDR}" \
  --web-addr "${WEB_ADDR}" \
  --allowed-hosts "${ALLOWED_HOSTS}"
