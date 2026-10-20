#!/usr/bin/env bash
# Build fake-dsh, push to local registry, generate PKI/template/edge config.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
COMPOSE="$ROOT/deploy/compose"
VAR="$COMPOSE/var"
mkdir -p "$VAR/enrollment" "$VAR/join"

echo "==> start local registry if needed"
docker compose -f "$COMPOSE/docker-compose.yml" up -d registry
sleep 2

echo "==> build and push fake-dsh"
docker build -t 127.0.0.1:5000/auriga/fake-dsh:e2e "$COMPOSE/fake-dsh"
docker push 127.0.0.1:5000/auriga/fake-dsh:e2e
DIGEST="$(docker inspect --format='{{index .RepoDigests 0}}' 127.0.0.1:5000/auriga/fake-dsh:e2e)"
echo "image digest: $DIGEST"

echo "==> bootstrap materials"
(
  cd "$COMPOSE/bootstrap"
  go mod tidy
  go run . -out "$VAR" -image "$DIGEST"
)

# Keys for Java (32-byte base64)
if [ -z "${AURIGA_IAM_ENCRYPTION_KEY:-}" ]; then
  AURIGA_IAM_ENCRYPTION_KEY="$(openssl rand -base64 32)"
fi
if [ -z "${AURIGA_WORKSPACE_ENCRYPTION_KEY:-}" ]; then
  AURIGA_WORKSPACE_ENCRYPTION_KEY="$(openssl rand -base64 32)"
fi
CA_SHA="$(tr -d '\n' < "$VAR/ca-sha256.txt")"

cat > "$VAR/compose.env" <<EOF
AURIGA_IAM_ENCRYPTION_KEY=$AURIGA_IAM_ENCRYPTION_KEY
AURIGA_WORKSPACE_ENCRYPTION_KEY=$AURIGA_WORKSPACE_ENCRYPTION_KEY
AURIGA_AGENT_CA_SHA256=$CA_SHA
EOF

# Empty join token until e2e writes it (agent waits).
: > "$VAR/join/token"
# Platform (root) needs CA/workspace private keys; edge runs as distroless nonroot and must
# traverse pki/ and read edge.key + identity-signing-keys.json from the bind mount.
chmod 755 "$VAR/pki" "$VAR/templates" || true
chmod 600 "$VAR/pki/ca.key" "$VAR/pki/workspace.key" || true
chmod 644 "$VAR/pki/"*.crt "$VAR/pki/edge.key" \
  "$VAR/identity-signing-keys.json" "$VAR/identity-keys.json" \
  "$VAR/edge.config.json" || true
chmod 777 "$VAR/enrollment" "$VAR/join" || true

echo "prepare done; env file: $VAR/compose.env"
