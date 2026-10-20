#!/bin/sh
# Local static validations for deploy/nas (no Docker required for most checks).
# shellcheck shell=sh
set -eu

ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
export NAS_ROOT="$ROOT"
export NAS_ALLOW_EXAMPLE_ENV=1
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
fail=0

pass() { printf 'PASS %s\n' "$1"; }
fail_msg() { printf 'FAIL %s: %s\n' "$1" "$2"; fail=1; }

# --- path rejection ---
AURIGA_DATA_ROOT="/tmp/not-used"
# shellcheck disable=SC1091
. "$ROOT/scripts/lib.sh"
if is_absolute_path "/srv/auriga-test"; then pass path_absolute; else fail_msg path_absolute "expected absolute"; fi
if is_absolute_path "relative/path"; then fail_msg path_relative "should reject"; else pass path_relative; fi
if path_under_root "/srv/auriga-test/data" "/srv/auriga-test"; then pass path_under; else fail_msg path_under "expected under"; fi
if path_under_root "/other" "/srv/auriga-test"; then fail_msg path_escape "should reject"; else pass path_escape; fi

# --- init idempotency + credential no-overwrite ---
DATA="$TMP/data"
mkdir -p "$DATA"
cat >"$DATA/stack.env" <<EOF
COMPOSE_PROJECT_NAME=auriga-nas-test
AURIGA_DATA_ROOT=$DATA
AURIGA_LAN_NETWORK=lan-example
AURIGA_LAN_SUBNET=192.0.2.0/24
AURIGA_EDGE_LAN_IP=192.0.2.10
AURIGA_PORTAL_HOST=portal.example.com
AURIGA_ADMIN_HOST=admin.example.com
AURIGA_INSTANCE_PARENT_DOMAIN=i.example.com
AURIGA_ISSUER=https://portal.example.com
AURIGA_TEAM_ORIGIN=https://portal.example.com
AURIGA_TEAM_RP_ID=portal.example.com
AURIGA_SECURE_COOKIES=true
AURIGA_TRUSTED_PROXIES=192.0.2.1/32
AURIGA_IMAGE_PLATFORM=auriga/platform@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
AURIGA_IMAGE_EDGE=auriga/edge@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
AURIGA_IMAGE_AGENT=auriga/agent@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
AURIGA_IMAGE_POSTGRES=postgres:17@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
AURIGA_IMAGE_NATS=nats:2.10-alpine@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
AURIGA_IMAGE_REGISTRY=registry:2@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
AURIGA_HOST_PUBLISH_NODECONTROL=17443
AURIGA_HOST_PUBLISH_REGISTRY=15000
AURIGA_HOST_PUBLISH_PLATFORM_HTTP=18081
AURIGA_PLATFORM_SEED=true
AURIGA_AGENT_CAPACITY_MAX_INSTANCES=1
EOF

if command -v go >/dev/null 2>&1 && command -v openssl >/dev/null 2>&1; then
  export AURIGA_DATA_ROOT="$DATA"
  if sh "$ROOT/scripts/init.sh"; then
    pass init_first
  else
    fail_msg init_first "init failed"
  fi
  KEY1=$(cat "$DATA/secrets/iam_encryption_key")
  if sh "$ROOT/scripts/init.sh"; then
    KEY2=$(cat "$DATA/secrets/iam_encryption_key")
    if [ "$KEY1" = "$KEY2" ]; then
      pass init_idempotent
    else
      fail_msg init_idempotent "key rotated on re-init"
    fi
  else
    fail_msg init_idempotent "re-init failed"
  fi
  # Credential no-overwrite: remove marker but keep secrets → init must refuse or no-op without rewrite
  rm -f "$DATA/.auriga-initialized"
  if sh "$ROOT/scripts/init.sh" 2>"$TMP/init.err"; then
    # If it succeeded, secrets must be unchanged
    KEY3=$(cat "$DATA/secrets/iam_encryption_key")
    if [ "$KEY1" = "$KEY3" ]; then
      pass creds_no_overwrite
    else
      fail_msg creds_no_overwrite "secrets changed"
    fi
  else
    # Refusal is also acceptable when data+secrets exist without marker
    pass creds_no_overwrite_refuse
  fi
  # Missing secrets with existing postgres data → refuse
  rm -rf "$DATA/secrets" "$DATA/.auriga-initialized" "$DATA/config"
  mkdir -p "$DATA/data/postgres"
  if sh "$ROOT/scripts/init.sh" 2>"$TMP/init2.err"; then
    fail_msg missing_secrets_refuse "should have refused"
  else
    pass missing_secrets_refuse
  fi
else
  printf 'SKIP init_tests (need go+openssl)\n'
fi

# --- stop scope: executable lines must not wipe volumes ---
if grep -v '^[[:space:]]*#' "$ROOT/scripts/stop.sh" | grep -E 'prune|down -v|down --volumes' >/dev/null; then
  fail_msg stop_scope "dangerous docker wipe found"
else
  pass stop_scope
fi
if grep -v '^[[:space:]]*#' "$ROOT/scripts/stop.sh" | grep -q 'down --remove-orphans'; then
  pass stop_keeps_volumes
else
  fail_msg stop_keeps_volumes "expected down --remove-orphans without volume wipe"
fi

# --- nats template: anonymous auth off; edge has no subject wildcard ---
if grep -v '^[[:space:]]*#' "$ROOT/nats/nats.conf.tmpl" | grep -q 'no_auth_user'; then
  fail_msg nats_no_auth "anonymous auth must stay disabled"
else
  pass nats_no_auth
fi
if grep -A20 'user: edge' "$ROOT/nats/nats.conf.tmpl" | grep -v '^[[:space:]]*#' | grep -q 'allow: \[">"\]'; then
  fail_msg nats_edge_wildcard "edge must not have publish/subscribe >"
else
  pass nats_edge_minperms
fi
if grep -q 'http: "127.0.0.1:8222"' "$ROOT/nats/nats.conf.tmpl"; then
  pass nats_monitor_loopback
else
  fail_msg nats_monitor_loopback "monitoring must bind loopback"
fi

# --- public placeholders only (exclude this test file's patterns) ---
if grep -RE 'avelo\.top|10\.0\.0\.|avelo' "$ROOT" \
  --include='*.md' --include='*.yml' --include='*.example' --include='*.tmpl' --include='*.sh' \
  --exclude='run-tests.sh' >/dev/null 2>&1; then
  fail_msg no_private_addrs "private/site values leaked into public pack"
else
  pass no_private_addrs
fi

# --- LF scripts (no CR) ---
cr=0
for f in "$ROOT"/scripts/*.sh "$ROOT"/docker/agent-entrypoint.sh "$ROOT"/test/run-tests.sh; do
  [ -f "$f" ] || continue
  if od -An -tx1 "$f" 2>/dev/null | grep -q ' 0d'; then
    fail_msg "lf_$f" "CR found"
    cr=1
  fi
done
[ "$cr" -eq 0 ] && pass scripts_lf

if [ "$fail" -ne 0 ]; then
  printf 'TESTS FAILED\n'
  exit 1
fi
printf 'ALL TESTS PASSED\n'
exit 0
