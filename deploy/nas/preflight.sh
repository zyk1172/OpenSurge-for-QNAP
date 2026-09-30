#!/bin/sh
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec sh "$SCRIPT_DIR/../qnap/preflight.sh" \
  --env-file "$SCRIPT_DIR/.env" \
  --compose-file "$SCRIPT_DIR/docker-compose.yml" "$@"
