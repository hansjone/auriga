#!/bin/sh
set -eu
DATA_ROOT="${AURIGA_AGENT_DATA_ROOT:-/var/lib/auriga}"
AGENT_DIR="${DATA_ROOT}/agent"
TOKEN_FILE="${AURIGA_AGENT_JOIN_TOKEN_FILE:-/run/auriga-join/token}"
mkdir -p "$AGENT_DIR" "$DATA_ROOT/instances" "$DATA_ROOT/artifacts" "$DATA_ROOT/backups"

if [ ! -f "$AGENT_DIR/node.crt" ]; then
  echo "waiting for join token at $TOKEN_FILE ..."
  i=0
  while [ ! -s "$TOKEN_FILE" ]; do
    i=$((i + 1))
    if [ "$i" -gt 600 ]; then
      echo "join token not provided" >&2
      exit 1
    fi
    sleep 1
  done
  SERVER="${AURIGA_AGENT_SERVER:?AURIGA_AGENT_SERVER required}"
  CA_SHA="${AURIGA_AGENT_CA_SHA256:?AURIGA_AGENT_CA_SHA256 required}"
  set -- join --server "$SERVER" --token-file "$TOKEN_FILE" --ca-sha256 "$CA_SHA" --data-root "$DATA_ROOT"
  if [ "${AURIGA_AGENT_ALLOW_INSECURE:-}" = "true" ]; then
    set -- "$@" --allow-insecure
  fi
  auriga-agent "$@"
fi

if [ -f /etc/auriga/identity-keys.json ]; then
  cp /etc/auriga/identity-keys.json "$AGENT_DIR/identity-keys.json"
fi

set -- run --data-root "$DATA_ROOT"
if [ "${AURIGA_AGENT_ALLOW_INSECURE:-}" = "true" ]; then
  set -- "$@" --allow-insecure
fi
if [ -n "${AURIGA_AGENT_INGRESS_LISTEN:-}" ]; then
  set -- "$@" --ingress-listen "$AURIGA_AGENT_INGRESS_LISTEN"
fi
if [ -n "${AURIGA_AGENT_INGRESS_LOCAL:-}" ]; then
  set -- "$@" --ingress-local "$AURIGA_AGENT_INGRESS_LOCAL"
fi
exec auriga-agent "$@"
