#!/usr/bin/env bash
# Build fake-dsh, push to local registry, generate PKI/template/edge config.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
COMPOSE="$ROOT/deploy/compose"
VAR="$COMPOSE/var"
ACCESS_ARGS=()
while [ "$#" -gt 0 ]; do
  if [ "$1" = --litellm-ui-origin ]; then
    [ "$#" -ge 2 ] && [ -n "$2" ] || { echo 'missing LiteLLM UI origin' >&2; exit 1; }
    ACCESS_ARGS+=("-litellm-ui-origin" "$2")
    shift 2
    continue
  fi
  case "$1" in --access-file|--https-cert-file|--https-key-file|--network-collab-file) ;; *) echo 'unsupported prepare input' >&2; exit 1 ;; esac
  [ "$#" -ge 2 ] && [ -n "$2" ] || { echo 'missing prepare input' >&2; exit 1; }
  # Resolve once before changing directory; only the public Go loader parses it.
  ACCESS_DIR="$(cd -- "$(dirname -- "$2")" 2>/dev/null && pwd)" || { echo 'invalid prepare input' >&2; exit 1; }
  ACCESS_FILE="$ACCESS_DIR/$(basename -- "$2")"
  ACCESS_ARGS+=("-${1#--}" "$ACCESS_FILE")
  shift 2
done
FIXTURE="${DSH_FIXTURE:-fake}"
case "$FIXTURE" in fake|real) ;; *) echo 'DSH_FIXTURE must be fake or real' >&2; exit 1 ;; esac
(
  cd "$COMPOSE/bootstrap"
  go run . -validate-access -edge-container "${COMPOSE_PROJECT_NAME:-auriga-m2}-edge" "${ACCESS_ARGS[@]}"
)
mkdir -p "$VAR/enrollment" "$VAR/join"

# Keep build metadata and public raw proofs outside the installation until the
# bootstrap receipt has accepted its original materials and exact inputs.
PREPARE_TMP="$(mktemp -d)"
PROOF_TMP="$PREPARE_TMP/proofs"
trap 'rm -rf -- "$PREPARE_TMP"' EXIT

echo "==> start local registry if needed"
bash "$COMPOSE/scripts/compose.sh" bootstrap-registry
sleep 2

case "$FIXTURE" in
  fake)
    echo "==> build and push fake-dsh"
    docker build --platform=linux/amd64 --provenance=false --sbom=false -t 127.0.0.1:5000/auriga/fake-dsh:e2e "$COMPOSE/fake-dsh"
    docker push 127.0.0.1:5000/auriga/fake-dsh:e2e
    DIGEST="$(docker inspect --format='{{index .RepoDigests 0}}' 127.0.0.1:5000/auriga/fake-dsh:e2e)"
    ;;
  real)
    echo "==> build and push pinned real DSH (S4 inputs)"
    node "$COMPOSE/scripts/build-real-dsh.mjs" --metadata-out "$PREPARE_TMP/real-dsh.json"
    DIGEST="$(node -p 'JSON.parse(require("node:fs").readFileSync(process.argv[1], "utf8")).image' "$PREPARE_TMP/real-dsh.json")"
    ;;
  *) echo "DSH_FIXTURE must be fake or real" >&2; exit 1 ;;
esac
echo "image digest: $DIGEST"

# Public raw registry bytes are collected outside the installation. Existing
# installations are validated by bootstrap before any material can be changed.
node "$COMPOSE/scripts/collect-runtime-proofs.mjs" "$DIGEST" "$PROOF_TMP"
echo "==> bootstrap materials"
OBSERVER_ARGS=()
if [ "${AURIGA_M2_HEARTBEAT_OBSERVER:-0}" = 1 ]; then
  OBSERVER_ARGS=(-heartbeat-observer)
fi
(
  cd "$COMPOSE/bootstrap"
  go run . -out "$VAR" -image "$DIGEST" -fixture "$FIXTURE" -runtime-proofs "$PROOF_TMP" \
    -edge-container "${COMPOSE_PROJECT_NAME:-auriga-m2}-edge" "${OBSERVER_ARGS[@]}" "${ACCESS_ARGS[@]}"
)

if [ "$FIXTURE" = real ]; then
  mv -fT -- "$PREPARE_TMP/real-dsh.json" "$VAR/real-dsh.json"
fi
rm -rf -- "$PREPARE_TMP"
trap - EXIT

# Bootstrap strictly validates and preserves the two private Java keys.
CA_SHA="$(tr -d '\n' < "$VAR/ca-sha256.txt")"

# Write a private new inode and atomically replace the run file; never follow an
# existing compose.env symlink or truncate another file through it.
COMPOSE_ENV_TMP="$(umask 077; mktemp "$VAR/.compose.env.XXXXXX")"
trap 'rm -f -- "$COMPOSE_ENV_TMP"' EXIT
cat "$VAR/iam-installation/compose.keys" > "$COMPOSE_ENV_TMP"
cat >> "$COMPOSE_ENV_TMP" <<EOF
AURIGA_AGENT_CA_SHA256=$CA_SHA
AURIGA_PANEL_UID=$(stat -c '%u' "$VAR/panel-signing.key")
AURIGA_PANEL_GID=$(stat -c '%g' "$VAR/panel-signing.key")
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
# uid10001 runtime reads its own material via gid10001, and the live issuer
# token via supplementary gid65533. Installation credentials stay owner-only.
sh "$COMPOSE/scripts/network-permissions.sh" "$VAR"
# Edge receives only its submit client identity and the shared public network
# server CA (submit mTLS and broker WSS); no broker identity or private key.
# The broker passwords, server keys and gateway installation credential remain private.
if [ -e "$VAR/network-edge" ] || [ -L "$VAR/network-edge" ]; then
  [ -d "$VAR/network-edge" ] && [ ! -L "$VAR/network-edge" ] \
    || { echo 'invalid network edge material directory' >&2; exit 1; }
else
  mkdir "$VAR/network-edge"
fi
chmod 750 "$VAR/network-edge"
for name in edge.pem edge.key server-ca.pem; do
  if [ -e "$VAR/network-edge/$name" ] || [ -L "$VAR/network-edge/$name" ]; then
    [ -f "$VAR/network-edge/$name" ] && [ ! -L "$VAR/network-edge/$name" ] && [ "$(stat -c '%h' "$VAR/network-edge/$name")" = 1 ] && cmp -s "$VAR/network/$name" "$VAR/network-edge/$name" \
      || { echo 'existing network edge material differs; refusing rotation' >&2; exit 1; }
  else
    (umask 077; cp -- "$VAR/network/$name" "$VAR/network-edge/$name")
  fi
  chmod 640 "$VAR/network-edge/$name"
done
if [ "$(id -u)" -eq 0 ]; then
  chgrp 65532 "$VAR/network-edge" "$VAR/network-edge/edge.pem" "$VAR/network-edge/edge.key" "$VAR/network-edge/server-ca.pem"
else
  sudo -n chgrp 65532 "$VAR/network-edge" "$VAR/network-edge/edge.pem" "$VAR/network-edge/edge.key" "$VAR/network-edge/server-ca.pem"
fi
bash "$COMPOSE/scripts/https-materials.sh" "$VAR"
if [ "$(id -u)" -eq 0 ]; then
  sh "$COMPOSE/scripts/collab-materials.sh" "$VAR"
else
  sudo -n sh "$COMPOSE/scripts/collab-materials.sh" "$VAR"
fi
chmod 644 "$VAR/pki/"*.crt "$VAR/pki/edge.key" \
  "$VAR/identity-signing-keys.json" "$VAR/identity-keys.json" \
  "$VAR/edge.config.json" || true
chmod 777 "$VAR/enrollment" "$VAR/join" || true

if [ -e "$VAR/panel" ] || [ -L "$VAR/panel" ]; then
  [ -d "$VAR/panel" ] && [ ! -L "$VAR/panel" ] || { echo 'invalid panel output directory' >&2; exit 1; }
else
  mkdir -m 755 "$VAR/panel"
fi

# The signer runs as the existing key owner; no extra DAC capability is needed.
PANEL_OWNER="$(stat -c '%u:%g' "$VAR/panel-signing.key")"
if [ "$(id -u)" -eq 0 ]; then
  chown "$PANEL_OWNER" "$VAR/panel"
else
  sudo -n chown "$PANEL_OWNER" "$VAR/panel"
fi
sh "$COMPOSE/scripts/renew-panel.sh" --install || echo "warning: panel automatic renewal unavailable; configure host cron or renew manually" >&2
sh "$COMPOSE/scripts/renew-panel.sh" || echo "warning: panel startup check failed; stack startup remains available" >&2

# File-backed secrets: only the intended container identity can read each key.
if [ "$(id -u)" -eq 0 ]; then
  chgrp 999 "$VAR/litellm/pg_password"
  chgrp 10001 "$VAR/litellm/team"
else
  sudo -n chgrp 999 "$VAR/litellm/pg_password"
  sudo -n chgrp 10001 "$VAR/litellm/team"
fi
chmod 640 "$VAR/litellm/pg_password" "$VAR/litellm/team"

echo "prepare done; env file: $VAR/compose.env"
