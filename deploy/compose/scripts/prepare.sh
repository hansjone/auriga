#!/usr/bin/env bash
# Build fake-dsh, push to local registry, generate PKI/template/edge config.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
COMPOSE="$ROOT/deploy/compose"
VAR="$COMPOSE/var"
ACCESS_ARGS=()
if [ "$#" -gt 0 ]; then
  if [ "$#" -ne 2 ] || [ "$1" != --access-file ] || [ -z "$2" ]; then
    echo 'usage: prepare.sh [--access-file /absolute/path/access.json]' >&2; exit 1
  fi
  # Resolve once before changing directory; only the public Go loader parses it.
  ACCESS_FILE="$(cd -- "$(dirname -- "$2")" && pwd)/$(basename -- "$2")"
  ACCESS_ARGS=(-access-file "$ACCESS_FILE")
fi
FIXTURE="${DSH_FIXTURE:-fake}"
case "$FIXTURE" in fake|real) ;; *) echo 'DSH_FIXTURE must be fake or real' >&2; exit 1 ;; esac
(
  cd "$COMPOSE/bootstrap"
  go run . -validate-access -edge-container "${COMPOSE_PROJECT_NAME:-auriga-m2}-edge" "${ACCESS_ARGS[@]}"
)
mkdir -p "$VAR/enrollment" "$VAR/join"

echo "==> start local registry if needed"
docker compose -f "$COMPOSE/docker-compose.yml" up -d registry
sleep 2

case "$FIXTURE" in
  fake)
    echo "==> build and push fake-dsh"
    docker build -t 127.0.0.1:5000/auriga/fake-dsh:e2e "$COMPOSE/fake-dsh"
    docker push 127.0.0.1:5000/auriga/fake-dsh:e2e
    DIGEST="$(docker inspect --format='{{index .RepoDigests 0}}' 127.0.0.1:5000/auriga/fake-dsh:e2e)"
    ;;
  real)
    echo "==> build and push pinned real DSH (S4 inputs)"
    node "$COMPOSE/scripts/build-real-dsh.mjs"
    DIGEST="$(node -p 'JSON.parse(require("node:fs").readFileSync(process.argv[1], "utf8")).image' "$VAR/real-dsh.json")"
    ;;
  *) echo "DSH_FIXTURE must be fake or real" >&2; exit 1 ;;
esac
echo "image digest: $DIGEST"

echo "==> bootstrap materials"
OBSERVER_ARGS=()
if [ "${AURIGA_M2_HEARTBEAT_OBSERVER:-0}" = 1 ]; then
  OBSERVER_ARGS=(-heartbeat-observer)
fi
(
  cd "$COMPOSE/bootstrap"
  go run . -out "$VAR" -image "$DIGEST" -fixture "$FIXTURE" \
    -edge-container "${COMPOSE_PROJECT_NAME:-auriga-m2}-edge" "${OBSERVER_ARGS[@]}" "${ACCESS_ARGS[@]}"
)

# Bootstrap strictly validates and preserves the two private Java keys.
CA_SHA="$(tr -d '\n' < "$VAR/ca-sha256.txt")"

# Write a private new inode and atomically replace the run file; never follow an
# existing compose.env symlink or truncate another file through it.
COMPOSE_ENV_TMP="$(umask 077; mktemp "$VAR/.compose.env.XXXXXX")"
trap 'rm -f -- "$COMPOSE_ENV_TMP"' EXIT
cat "$VAR/iam-installation/compose.keys" > "$COMPOSE_ENV_TMP"
cat >> "$COMPOSE_ENV_TMP" <<EOF
AURIGA_AGENT_CA_SHA256=$CA_SHA
EOF
cat "$VAR/access.env" >> "$COMPOSE_ENV_TMP"
mv -fT -- "$COMPOSE_ENV_TMP" "$VAR/compose.env"
trap - EXIT

# Empty join token until e2e writes it (agent waits).
: > "$VAR/join/token"
# Platform (root) needs CA/workspace private keys; edge runs as distroless nonroot and must
# traverse pki/ and read edge.key + identity-signing-keys.json from the bind mount.
chmod 755 "$VAR/pki" "$VAR/templates" || true
chmod 600 "$VAR/pki/ca.key" "$VAR/pki/workspace.key" || true
# Dedicated IAM HTTP-hop key: owner (runner/health probe) + distroless edge group.
# This changes only this run's generated file, never host trust or global ACLs.
if [ "$(id -u)" -eq 0 ]; then
  chgrp 65532 "$VAR/pki/edge-iam.key"
else
  sudo -n chgrp 65532 "$VAR/pki/edge-iam.key"
fi
chmod 640 "$VAR/pki/edge-iam.key"
chmod 644 "$VAR/pki/"*.crt "$VAR/pki/edge.key" \
  "$VAR/identity-signing-keys.json" "$VAR/identity-keys.json" \
  "$VAR/edge.config.json" || true
chmod 777 "$VAR/enrollment" "$VAR/join" || true

echo "prepare done; env file: $VAR/compose.env"
