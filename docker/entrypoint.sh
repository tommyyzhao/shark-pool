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

CFG_COUNT=$(find "${VPN_CONFIG_DIR:-/vpn/configs}" -name '*.ovpn' 2>/dev/null | wc -l | tr -d ' ')
echo "shark-pool country pool '${EXIT_NAME:-exit}' starting (${CFG_COUNT} locations)"
echo "  http base: :${HTTP_PORT:-8888}   socks base: :${SOCKS_PORT:-1080}   api: :${API_PORT:-8000}"
exec shark-pool exit
