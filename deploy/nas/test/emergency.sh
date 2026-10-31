#!/bin/sh
# CLI/start boundaries with a Docker stub; no daemon or real secrets.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT INT TERM
mkdir -p "$TMP/package/scripts" "$TMP/srv/container/auriga-first/enrollment" "$TMP/bin"
cp "$ROOT/scripts/lib.sh" "$ROOT/scripts/emergency.sh" "$ROOT/scripts/start.sh" "$TMP/package/scripts/"
for script in preflight network-preflight render installer; do
  printf '#!/bin/sh\nexit 0\n' >"$TMP/package/scripts/$script.sh"
done
export AURIGA_DATA_ROOT="$TMP/srv/container/auriga-first" NAS_ROOT="$TMP/package" EMERGENCY_LOG="$TMP/docker.log"
export NAS_ALLOW_NONROOT=1
unset NAS_INSTALLER_ACTIVE
touch "$AURIGA_DATA_ROOT/.auriga-initialized"
printf 'AURIGA_DATA_ROOT=%s\nAURIGA_INSTALL_MODE=host\nAURIGA_IMAGE_LITELLM=sha256:%064d\nAURIGA_ACCESS_FILE=%s/access.json\n' \
  "$AURIGA_DATA_ROOT" 1 "$TMP" >"$AURIGA_DATA_ROOT/stack.env"
chmod 600 "$AURIGA_DATA_ROOT/stack.env"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$EMERGENCY_LOG"
case "$1" in
  info|ps) exit 0 ;;
  image) printf 'linux/amd64\n' ;;
  compose)
    [ "${2:-}" != version ] || exit 0
    exit "${EMERGENCY_UP_EXIT:-0}" ;;
  run)
    case " $* " in *' --log-driver none --tty --network none --user 10001:10001 --cap-drop ALL --security-opt no-new-privileges:true --read-only '*) ;; *) exit 90 ;; esac
    case " $* " in *docker.sock*|*' --privileged '*|*' --publish '*|*' --network host '*|*' --env '*password*) exit 91 ;; esac
    case " $* " in *'dst=/enrollment/emergency-enrollment.json,readonly '*) ;; *) exit 92 ;; esac
    case " $* " in *' /emergency.py --once '*) ;; *' /emergency.py '*) ;; *) exit 93 ;; esac
    exit "${EMERGENCY_HELPER_EXIT:-0}" ;;
  *) exit 94 ;;
esac
EOF
chmod 755 "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH"
: >"$EMERGENCY_LOG"
if sh "$NAS_ROOT/scripts/emergency.sh" show >"$TMP/output" 2>&1; then exit 1; fi
grep -q 'stdout to be a terminal' "$TMP/output"
[ ! -s "$EMERGENCY_LOG" ]
echo 'PASS emergency redirect refused before configuration/Docker'
if sh "$NAS_ROOT/scripts/emergency.sh" rotate >"$TMP/output" 2>&1; then exit 1; fi
grep -qx 'Usage: sh emergency.sh show' "$TMP/output"
[ ! -s "$EMERGENCY_LOG" ]
echo 'PASS emergency unsupported rotate is not a public command'
sh "$NAS_ROOT/scripts/start.sh" >"$TMP/output" 2>&1 || { cat "$TMP/output"; exit 1; }
grep -q 'handoff pending' "$TMP/output"
! grep -q '^run ' "$EMERGENCY_LOG"
echo 'PASS emergency non-TTY startup stays usable without exposing credentials'
EMERGENCY_UP_EXIT=73; export EMERGENCY_UP_EXIT
if sh "$NAS_ROOT/scripts/start.sh" >"$TMP/output" 2>&1; then exit 1; else
  result=$?
  [ "$result" = 73 ] || { cat "$TMP/output"; exit 1; }
fi
! grep -q 'handoff pending' "$TMP/output"
unset EMERGENCY_UP_EXIT
echo 'PASS emergency compose failure preserved before handoff'
# A real pty is required here: there is no production test-only TTY bypass.
if [ -t 1 ]; then
  sh "$NAS_ROOT/scripts/emergency.sh" show
  grep -q '^run .*--log-driver none --tty --network none' "$EMERGENCY_LOG"
  grep -q -- '--env AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=true' "$EMERGENCY_LOG"
  AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=false sh "$NAS_ROOT/scripts/emergency.sh" show
  grep -q -- '--env AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=false' "$EMERGENCY_LOG"
  : >"$EMERGENCY_LOG"
  if AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=invalid sh "$NAS_ROOT/scripts/emergency.sh" show 2>"$TMP/output"; then exit 1; fi
  grep -q 'must be true or false' "$TMP/output"
  [ ! -s "$EMERGENCY_LOG" ]
  sh "$NAS_ROOT/scripts/start.sh"
  grep -q ' /emergency.py --once$' "$EMERGENCY_LOG"
  EMERGENCY_HELPER_EXIT=73; export EMERGENCY_HELPER_EXIT
  if sh "$NAS_ROOT/scripts/emergency.sh" show; then exit 1; else [ "$?" = 73 ]; fi
  sh "$NAS_ROOT/scripts/start.sh"
  echo 'PASS emergency TTY helper boundary and exit propagation'
else
  echo 'SKIP emergency TTY shell branch (run this test in a private TTY)'
fi
! grep -Eq '(^| )pull( |$)' "$EMERGENCY_LOG"
