#!/bin/sh
# Executable CLI stubs exercise the real entry points without an Engine/network.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
mkdir -p "$TMP/bin" "$TMP/nas/scripts" "$TMP/srv/container/auriga-test"
cp "$ROOT/scripts/lib.sh" "$TMP/nas/scripts/lib.sh"
cp "$ROOT/scripts/preflight.sh" "$TMP/nas/scripts/preflight.sh"
cp "$ROOT/scripts/start.sh" "$TMP/nas/scripts/start.sh"
for script in network-preflight render mount-access renew-panel; do
  printf '#!/bin/sh\nexit 0\n' >"$TMP/nas/scripts/$script.sh"
done
printf '#!/bin/sh\nexit "${NAS_READY_MOUNT_EXIT:-0}"\n' >"$TMP/nas/scripts/mount-access.sh"
export NAS_ROOT="$TMP/nas" NAS_ALLOW_NONROOT=1
export AURIGA_DATA_ROOT="$TMP/srv/container/auriga-test"
export AURIGA_TEST_MIN_MEMORY_KB=0 AURIGA_TEST_MIN_DISK_KB=0
export NAS_READY_LOG="$TMP/docker.log"
litellm_image='ghcr.io/berriai/litellm:v1.106.6@sha256:37e2dafb088a7723beb998590682957386dfc83ffea1848486f8e0e5cfac0b06'
touch "$AURIGA_DATA_ROOT/.auriga-initialized"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$NAS_READY_LOG"
case "$1 ${2:-}" in
  'image inspect')
    for ref do :; done
    [ "$ref" != "${NAS_READY_MISSING:-}" ] || exit 61
    if [ "$ref" = "${NAS_READY_BAD_REF:-}" ]; then
      printf '%s\n' "$NAS_READY_OUTPUT"
    else printf 'linux/amd64\n'; fi
    ;;
  'compose version'|'info '|'ps -q') exit 0 ;;
  'compose -f')
    case " $* " in *' up '*) exit "${NAS_READY_UP_EXIT:-0}" ;; esac
    ;;
  *) echo 'unexpected docker operation' >&2; exit 90 ;;
esac
EOF
printf '#!/bin/sh\nprintf "x86_64\\n"\n' >"$TMP/bin/uname"
printf '#!/bin/sh\nexit 0\n' >"$TMP/bin/ss"
# Git Bash exposes /proc/meminfo without Linux's MemAvailable field.
real_awk=$(command -v awk)
printf '#!/bin/sh\n' >"$TMP/bin/awk"
printf 'real_awk="%s"\n' "$real_awk" >>"$TMP/bin/awk"
cat >>"$TMP/bin/awk" <<'EOF'
for arg do
  if [ "$arg" = /proc/meminfo ]; then printf '99999999\n'; exit 0; fi
done
exec "$real_awk" "$@"
EOF
chmod 0755 "$TMP/bin/"*
export PATH="$TMP/bin:$PATH"
write_env() {
  cat >"$AURIGA_DATA_ROOT/stack.env" <<EOF
AURIGA_DATA_ROOT=$AURIGA_DATA_ROOT
AURIGA_ACCESS_FILE=$TMP/access.json
AURIGA_HOST_PUBLISH_NODECONTROL=17443
AURIGA_HOST_PUBLISH_REGISTRY=15000
AURIGA_HOST_PUBLISH_PLATFORM_HTTP=18081
EOF
  i=0
  for fixture_service in PLATFORM EDGE AGENT POSTGRES NATS REGISTRY; do
    i=$((i + 1))
    hash=$(printf '%064d' 0 | tr 0 "$i")
    if [ "${NAS_READY_DIGEST_APPS:-0}" = 1 ] || [ "$i" -ge 4 ]; then
      ref="example.com/$fixture_service@sha256:$hash"
    else ref="sha256:$hash"; fi
    printf 'AURIGA_IMAGE_%s=%s\n' "$fixture_service" "$ref" >>"$AURIGA_DATA_ROOT/stack.env"
  done
}
fail=0
pass() { printf 'PASS %s\n' "$1"; }
bad() {
  printf 'FAIL %s\n' "$1"
  cat "$TMP/output"
  fail=1
}
check_preflight() {
  : >"$NAS_READY_LOG"
  if sh "$NAS_ROOT/scripts/preflight.sh" >"$TMP/output" 2>&1; then result=0; else result=$?; fi
  if [ "$2" = pass ]; then
    if [ "$result" = 0 ] && [ "$(grep -c '^image inspect ' "$NAS_READY_LOG")" = 7 ]; then pass "$1"; else bad "$1"; fi
  elif [ "$result" != 0 ] && grep -q "$3" "$TMP/output"; then pass "$1"; else bad "$1"; fi
  if grep -Eq '(^| )pull( |$)| up ' "$NAS_READY_LOG"; then bad preflight_must_be_read_only; fi
}
write_env
check_preflight seven_local_images pass ''
for service in PLATFORM EDGE AGENT POSTGRES NATS REGISTRY; do
  NAS_READY_MISSING=$(sed -n "s/^AURIGA_IMAGE_$service=//p" "$AURIGA_DATA_ROOT/stack.env")
  export NAS_READY_MISSING
  check_preflight "missing_$service" fail 'image not available locally'
done
unset NAS_READY_MISSING
NAS_READY_MISSING=$litellm_image
export NAS_READY_MISSING
check_preflight missing_LITELLM fail 'image not available locally'
unset NAS_READY_MISSING
NAS_READY_DIGEST_APPS=1 write_env
check_preflight seven_digest_images pass ''
export NAS_READY_MISSING
NAS_READY_MISSING=$(sed -n 's/^AURIGA_IMAGE_PLATFORM=//p' "$AURIGA_DATA_ROOT/stack.env")
check_preflight missing_application_digest fail 'image not available locally'
unset NAS_READY_MISSING
for service in PLATFORM EDGE AGENT POSTGRES NATS REGISTRY; do
  export NAS_READY_BAD_REF NAS_READY_OUTPUT=linux/arm64
  NAS_READY_BAD_REF=$(sed -n "s/^AURIGA_IMAGE_$service=//p" "$AURIGA_DATA_ROOT/stack.env")
  check_preflight "wrong_arch_$service" fail 'expected linux/amd64'
done
NAS_READY_BAD_REF=$litellm_image
export NAS_READY_BAD_REF NAS_READY_OUTPUT=linux/arm64
check_preflight wrong_arch_LITELLM fail 'expected linux/amd64'
for NAS_READY_OUTPUT in windows/amd64 '' garbage 'linux/amd64 extra' 'linux/amd64
linux/amd64'; do
  export NAS_READY_OUTPUT
  check_preflight invalid_inspect_output fail 'expected linux/amd64'
done
unset NAS_READY_BAD_REF NAS_READY_OUTPUT
for service in PLATFORM EDGE AGENT POSTGRES NATS REGISTRY; do
  write_env
  sed "s|^AURIGA_IMAGE_$service=.*|AURIGA_IMAGE_$service=sha256:$(printf '%064d' 0)|" "$AURIGA_DATA_ROOT/stack.env" >"$TMP/zero.env"
  cp "$TMP/zero.env" "$AURIGA_DATA_ROOT/stack.env"
  check_preflight "placeholder_$service" fail 'placeholder image'
done
write_env
NAS_READY_MOUNT_EXIT=73; export NAS_READY_MOUNT_EXIT
: >"$NAS_READY_LOG"
if sh "$NAS_ROOT/scripts/start.sh" >"$TMP/start.output" 2>&1; then result=0; else result=$?; fi
if [ "$result" = 73 ] && ! grep -q ' up ' "$NAS_READY_LOG"; then pass start_mount_access_failure_blocks_up
else bad start_mount_access_failure_blocks_up; fi
unset NAS_READY_MOUNT_EXIT
for NAS_READY_UP_EXIT in 73 0; do
  export NAS_READY_UP_EXIT
  : >"$NAS_READY_LOG"
  if sh "$NAS_ROOT/scripts/start.sh" >"$TMP/start.output" 2>&1; then result=0; else result=$?; fi
  if [ "$result" = "$NAS_READY_UP_EXIT" ] \
    && [ "$(grep -c ' up ' "$NAS_READY_LOG")" = 1 ] \
    && grep -q ' up -d --no-build --pull never$' "$NAS_READY_LOG" \
    && ! grep -Eq '(^| )pull( |$)' "$NAS_READY_LOG"; then
    pass "start_single_up_exit_$NAS_READY_UP_EXIT"
  else bad "start_single_up_exit_$NAS_READY_UP_EXIT"; fi
done
[ "$fail" = 0 ]
