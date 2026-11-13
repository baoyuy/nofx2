#!/usr/bin/env bash
# Local development only: open a SOCKS5 tunnel through the deployment server so
# the local app can reach Binance from a network that blocks it.
#
#   NOFX_TUNNEL_HOST=<server> ./scripts/dev-tunnel.sh
#
# The host is not stored in the repository. Point HTTPS_PROXY at
# socks5h://127.0.0.1:1080 (host) or socks5h://host.docker.internal:1080
# (inside Docker Desktop) to use it.
set -euo pipefail

: "${NOFX_TUNNEL_HOST:?set NOFX_TUNNEL_HOST to the server address}"
HOST="$NOFX_TUNNEL_HOST"
PORT="${NOFX_TUNNEL_PORT:-1080}"
KEY="${NOFX_TUNNEL_KEY:-$HOME/.ssh/nofx_server}"
USER_NAME="${NOFX_TUNNEL_USER:-root}"

BIND="${NOFX_TUNNEL_BIND:-127.0.0.1}"

exec ssh -N -D "$BIND:$PORT" -i "$KEY" \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 \
  -o ServerAliveCountMax=3 \
  -o StrictHostKeyChecking=accept-new \
  "$USER_NAME@$HOST"
