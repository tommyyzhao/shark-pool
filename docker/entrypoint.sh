#!/bin/sh
set -eu

MODE="${SHARK_POOL_MODE:-exit}"

if [ "$MODE" = "registry" ] || [ "$MODE" = "pool" ]; then
  echo "shark-pool registry starting on :${API_PORT:-8100}"
  exec shark-pool registry
fi

if [ -z "${SURFSHARK_USERNAME:-}" ] || [ -z "${SURFSHARK_PASSWORD:-}" ]; then
  echo "ERROR: set SURFSHARK_USERNAME and SURFSHARK_PASSWORD (Surfshark manual-setup service credentials)"
  exit 1
fi

echo "shark-pool exit '${EXIT_NAME:-exit}' starting"
echo "  config: ${VPN_CONFIG:-auto from ${VPN_CONFIG_DIR:-/vpn/configs}}"
echo "  http:   :${HTTP_PORT:-8888}   socks: :${SOCKS_PORT:-1080}   api: :${API_PORT:-8000}"
exec shark-pool exit
