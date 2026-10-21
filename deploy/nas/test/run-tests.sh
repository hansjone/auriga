#!/bin/sh
# Behavior tests for deploy/nas (no real Docker daemon required for most).
# shellcheck shell=sh
set -eu

ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
export NAS_ROOT="$ROOT"
export NAS_ALLOW_EXAMPLE_ENV=1
export NAS_ALLOW_NONROOT=${NAS_ALLOW_NONROOT:-1}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
fail=0

pass() { printf 'PASS %s\n' "$1"; }
fail_msg() { printf 'FAIL %s: %s\n' "$1" "$2"; fail=1; }

# shellcheck disable=SC1091
. "$ROOT/scripts/lib.sh"

try_validate() {
  # validate_data_root calls die on failure — isolate in subshell
  sh -c 'NAS_ROOT="$1"; . "$1/scripts/lib.sh"; validate_data_root "$2"' sh "$ROOT" "$1" 2>/dev/null
}

# --- path validation (M3) ---
if try_validate "/srv/container/auriga-test"; then pass path_ok; else fail_msg path_ok "should accept"; fi
if try_validate "/srv/auriga-test"; then fail_msg path_shallow "should reject depth<3"; else pass path_shallow; fi
if try_validate "/"; then fail_msg path_root "should reject /"; else pass path_root; fi
if try_validate "/share"; then fail_msg path_share "should reject /share"; else pass path_share; fi
if try_validate "/tmp/foo/bar"; then fail_msg path_name "should reject non-auriga-* basename"; else pass path_name; fi
if try_validate "/srv/container/auriga-test/../x"; then fail_msg path_dotdot "should reject .."; else pass path_dotdot; fi

mkdir -p "$TMP/real/container"
ln -s "$TMP/real/container" "$TMP/linkroot" 2>/dev/null || true
if [ -L "$TMP/linkroot" ]; then
  if try_validate "$TMP/linkroot/auriga-test"; then
    fail_msg path_symlink "should reject symlink component"
  else
    pass path_symlink
  fi
else
  printf 'SKIP path_symlink (ln not available)\n'
fi

# --- rand_alnum / empty secret (M4) ---
_v=$(rand_alnum 32) || { fail_msg rand_alnum "failed"; _v=""; }
if [ "${#_v}" -eq 32 ]; then pass rand_alnum; else fail_msg rand_alnum "len=${#_v}"; fi

# --- project name lock (M1) ---
if [ "$AURIGA_NAS_PROJECT" = "auriga-nas-test" ]; then pass project_fixed; else fail_msg project_fixed "unexpected"; fi

# --- ipv4_in_cidr (L4) ---
if ipv4_in_cidr "192.0.2.10" "192.0.2.0/24"; then pass cidr_in; else fail_msg cidr_in "expected in"; fi
if ipv4_in_cidr "198.51.100.10" "192.0.2.0/24"; then fail_msg cidr_out "expected out"; else pass cidr_out; fi

try_img() {
  sh -c 'NAS_ROOT="$1"; . "$1/scripts/lib.sh"; validate_image_ref "$2"' sh "$ROOT" "$1" 2>/dev/null
}

# --- stop scope: no volume wipe, no remove-orphans ---
if grep -v '^[[:space:]]*#' "$ROOT/scripts/stop.sh" | grep -E 'prune|down -v|down --volumes|remove-orphans' >/dev/null; then
  fail_msg stop_scope "dangerous stop flags present"
else
  pass stop_scope
fi

# --- nats: no anonymous auth; no JS.API.> wildcards; jetstream caps ---
if grep -v '^[[:space:]]*#' "$ROOT/nats/nats.conf.tmpl" | grep -q 'no_auth_user'; then
  fail_msg nats_no_auth "anonymous auth must stay disabled"
else
  pass nats_no_auth
fi
if grep -v '^[[:space:]]*#' "$ROOT/nats/nats.conf.tmpl" | grep -qE '\$JS\.API\.>"'; then
  fail_msg nats_js_wild "JS.API.> must not appear"
else
  pass nats_js_narrow
fi
if grep -q 'max_file_store' "$ROOT/nats/nats.conf.tmpl" && grep -q 'max_memory_store' "$ROOT/nats/nats.conf.tmpl"; then
  pass nats_js_caps
else
  fail_msg nats_js_caps "missing jetstream store caps"
fi

# --- compose topology markers (H1/H2/M6/M8) ---
grep -q 'auriga-nas-test_publish' "$ROOT/docker-compose.yml" || { fail_msg topo_publish "missing publish network"; }
grep -q 'internal: true' "$ROOT/docker-compose.yml" || { fail_msg topo_internal "missing internal:true"; }
grep -q 'pki-edge' "$ROOT/docker-compose.yml" || { fail_msg topo_pki_edge "missing pki-edge mount"; }
grep -q '/socket-share:' "$ROOT/docker-compose.yml" || { fail_msg topo_ingress "missing socket parent mount"; }
# edge service block must not mount data/agent (ignore comments)
if awk '
  /^  edge:/ {in_edge=1; next}
  /^  [a-z]/ && in_edge {in_edge=0}
  in_edge && $0 !~ /^[[:space:]]*#/ && /data\/agent/ {found=1}
  END { exit found ? 0 : 1 }
' "$ROOT/docker-compose.yml"; then
  fail_msg edge_agent_mount "edge must not mount data/agent"
else
  pass edge_no_agent_mount
fi
grep -q 'cap_drop: \[ALL\]' "$ROOT/docker-compose.yml" && pass cap_drop || fail_msg cap_drop "missing"

# --- no private site values ---
if grep -RE 'avelo\.top|10\.0\.0\.|avelo' "$ROOT" \
  --include='*.md' --include='*.yml' --include='*.example' --include='*.tmpl' --include='*.sh' \
  --exclude='run-tests.sh' >/dev/null 2>&1; then
  fail_msg no_private_addrs "private/site values leaked"
else
  pass no_private_addrs
fi

# --- init behavior with fake go/openssl ---
DATA="$TMP/srv/container/auriga-test"
mkdir -p "$DATA"
cat >"$DATA/stack.env" <<EOF
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
AURIGA_IMAGE_PLATFORM=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
AURIGA_IMAGE_EDGE=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
AURIGA_IMAGE_AGENT=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
AURIGA_IMAGE_POSTGRES=postgres:17@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
AURIGA_IMAGE_NATS=nats:2.10-alpine@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
AURIGA_IMAGE_REGISTRY=registry:2@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
AURIGA_HOST_PUBLISH_NODECONTROL=17443
AURIGA_HOST_PUBLISH_REGISTRY=15000
AURIGA_HOST_PUBLISH_PLATFORM_HTTP=18081
AURIGA_PLATFORM_SEED=true
AURIGA_AGENT_CAPACITY_MAX_INSTANCES=1
EOF

if { [ -x "$ROOT/bin/auriga-nas-bootstrap" ] || command -v go >/dev/null 2>&1; } && command -v openssl >/dev/null 2>&1; then
  export AURIGA_DATA_ROOT="$DATA"
  if sh "$ROOT/scripts/init.sh"; then
    pass init_first
  else
    fail_msg init_first "init failed"
  fi
  if [ "$NAS_ALLOW_NONROOT" != 1 ]; then
    if [ "$(stat -c '%u:%a' "$DATA/config/postgres/init.sql")" = 999:600 ] \
      && [ "$(stat -c '%u:%g:%a' "$DATA/socket-share")" = 0:65532:750 ]; then
      pass linux_owner_modes
    else
      fail_msg linux_owner_modes "postgres SQL or shared parent ownership incorrect"
    fi
  fi
  KEY1=$(cat "$DATA/secrets/iam_encryption_key")
  if sh "$ROOT/scripts/init.sh"; then
    KEY2=$(cat "$DATA/secrets/iam_encryption_key")
    if [ "$KEY1" = "$KEY2" ]; then pass init_idempotent; else fail_msg init_idempotent "key rotated"; fi
  else
    fail_msg init_idempotent "re-init failed"
  fi
  if sh "$ROOT/test/review-regressions.sh" "$DATA"; then
    pass review_regressions
  else
    fail_msg review_regressions "review regression failed"
  fi
  # Non-empty foreign root without marker
  OTHER="$TMP/srv/container/auriga-other"
  mkdir -p "$OTHER/noise"
  echo x >"$OTHER/noise/f"
  cat >"$OTHER/stack.env" <<EOF
AURIGA_DATA_ROOT=$OTHER
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
AURIGA_IMAGE_PLATFORM=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
AURIGA_IMAGE_EDGE=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
AURIGA_IMAGE_AGENT=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
AURIGA_IMAGE_POSTGRES=postgres:17@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
AURIGA_IMAGE_NATS=nats:2.10-alpine@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
AURIGA_IMAGE_REGISTRY=registry:2@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
AURIGA_HOST_PUBLISH_NODECONTROL=17443
AURIGA_HOST_PUBLISH_REGISTRY=15000
AURIGA_HOST_PUBLISH_PLATFORM_HTTP=18081
AURIGA_PLATFORM_SEED=false
AURIGA_AGENT_CAPACITY_MAX_INSTANCES=1
EOF
  export AURIGA_DATA_ROOT="$OTHER"
  if sh "$ROOT/scripts/init.sh" 2>"$TMP/init-foreign.err"; then
    fail_msg foreign_root "should refuse non-empty unmarked root"
  else
    pass foreign_root
  fi
  # Missing secrets with postgres data
  export AURIGA_DATA_ROOT="$DATA"
  rm -rf "$DATA/secrets" "$DATA/.auriga-initialized" "$DATA/config"
  mkdir -p "$DATA/data/postgres"
  echo leftover >"$DATA/data/postgres/x"
  if sh "$ROOT/scripts/init.sh" 2>"$TMP/init2.err"; then
    fail_msg missing_secrets_refuse "should have refused"
  else
    pass missing_secrets_refuse
  fi
else
  printf 'SKIP init_tests (need go+openssl)\n'
fi

# --- H4: project_is_running helper with fake docker ---
BIN="$TMP/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'EOF'
#!/bin/sh
# Emulate: docker ps -q --filter label=com.docker.compose.project=...
if [ "$1" = "ps" ]; then
  if [ "${FAKE_RUNNING:-}" = "1" ]; then
    printf '%s\n' 'deadbeefcafebabe'
  fi
  exit 0
fi
exit 0
EOF
# Ensure LF executable for Git Bash on Windows
chmod +x "$BIN/docker"
PATH="$BIN:$PATH"
export PATH
export FAKE_RUNNING=1
_out=$(docker ps -q --filter "label=com.docker.compose.project=auriga-nas-test" || true)
if [ -n "$_out" ] && project_is_running; then
  pass h4_running_detect
else
  fail_msg h4_running_detect "fake docker out='$_out' which=$(command -v docker)"
fi
export FAKE_RUNNING=0
if project_is_running; then fail_msg h4_stopped_detect "expected stopped"; else pass h4_stopped_detect; fi

# --- image ref validator (H5) ---
if validate_image_ref "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" 2>/dev/null; then
  pass img_local_id
else
  fail_msg img_local_id "sha256:id should pass"
fi
if validate_image_ref "auriga/platform:nas" 2>/dev/null; then
  fail_msg img_tag "bare tag should fail"
else
  pass img_tag_reject
fi

# --- smoke partial exit code documented ---
if grep -q 'AURIGA_SMOKE_ALLOW_PARTIAL' "$ROOT/scripts/smoke.sh" && grep -q 'exit 2' "$ROOT/scripts/smoke.sh"; then
  pass smoke_partial
else
  fail_msg smoke_partial "missing PARTIAL exit 2"
fi

# --- LF ---
cr=0
for f in "$ROOT"/scripts/*.sh "$ROOT"/docker/agent-entrypoint.sh "$ROOT"/test/run-tests.sh; do
  [ -f "$f" ] || continue
  if od -An -tx1 "$f" 2>/dev/null | grep -q ' 0d'; then
    fail_msg "lf_$(basename "$f")" "CR found"
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
