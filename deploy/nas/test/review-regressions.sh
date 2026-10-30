#!/bin/sh
# Exercise review findings with actual scripts and a recording Docker substitute.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
DATA=$1
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
export NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA" NAS_ALLOW_NONROOT=0
. "$ROOT/scripts/lib.sh"
load_stack_env

grep -qx 'SPRING_PROFILES_ACTIVE=combined,e2e' "$DATA/config/platform.env"
sed 's/AURIGA_PLATFORM_SEED=true/AURIGA_PLATFORM_SEED=false/' "$DATA/stack.env" >"$TMP/env"
cp "$TMP/env" "$DATA/stack.env"
sh "$ROOT/scripts/render.sh" >/dev/null
grep -qx 'SPRING_PROFILES_ACTIVE=combined' "$DATA/config/platform.env"
[ -d "$DATA/socket-share" ] && [ ! -e "$DATA/socket-share/ingress" ]
printf 'PASS seed_profiles_and_socket_parent\n'

# Normalize the actual Compose model: aliases must resolve to the same stable
# trusted edge while all existing network and privilege boundaries stay intact.
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
  && command -v python3 >/dev/null 2>&1; then
  umask 077
  docker compose -f "$ROOT/docker-compose.yml" --env-file "$DATA/stack.env" \
    config --format json >"$TMP/compose.json"
  python3 "$ROOT/test/internal-heartbeat.py" "$DATA" "$TMP/compose.json"
  cp "$AURIGA_ACCESS_FILE" "$TMP/access.saved"
  sed 's/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002/g' "$TMP/access.saved" >"$AURIGA_ACCESS_FILE"
  if sh "$ROOT/scripts/render.sh" >"$TMP/changed.log" 2>&1; then echo 'changed installation input accepted' >&2; exit 1; fi
  cp "$TMP/access.saved" "$AURIGA_ACCESS_FILE"
  sh "$ROOT/scripts/render.sh" >/dev/null
  printf 'PASS access_input_change_refused\n'

else
  printf 'SKIP internal_heartbeat_normalized_config (need Docker Compose and Python 3)\n'
fi

mkdir "$TMP/outside"
if ln -s "$TMP/outside" "$TMP/probe-link" 2>/dev/null && [ -L "$TMP/probe-link" ]; then
  rm "$TMP/probe-link"
  mv "$DATA/config/postgres" "$DATA/config/postgres-saved"
  ln -s "$TMP/outside" "$DATA/config/postgres"
  if sh "$ROOT/scripts/render.sh" >"$TMP/render.log" 2>&1; then
    echo 'render accepted symlink' >&2; exit 1
  fi
  [ ! -e "$TMP/outside/init.sql" ]
  rm "$DATA/config/postgres"
  mv "$DATA/config/postgres-saved" "$DATA/config/postgres"
  printf 'PASS managed_child_symlink_rejected\n'
else
  printf 'SKIP managed_child_symlink_rejected (filesystem lacks symlinks)\n'
fi

mkdir -p "$TMP/bin" "$DATA/data/agent/instances/inst_one/home" "$DATA/results"
export TEST_NAS_DATA="$DATA" TEST_DOCKER_LOG="$TMP/docker.log"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
case "$1" in
  info) exit 0 ;;
  image)
    [ "${2:-}" = inspect ] && [ "${3:-}" = --format ] || exit 1
    case "${4:-}" in
      '{{.Os}}/{{.Architecture}}') printf 'linux/amd64\n' ;;
      '{{.Config.User}}') printf '\n' ;;
      *) exit 1 ;;
    esac
    ;;
  run)
    # Start-chain coverage only; real DAC/ACL checks live in mount-access-real.sh.
    case " $* " in *' --pull never --network none --user '*) ;; *) exit 1 ;; esac
    case " $* " in *' --cap-drop ALL --security-opt no-new-privileges:true --read-only '*) ;; *) exit 1 ;; esac
    case " $* " in *docker.sock*|*' --privileged '*|*' --cap-add '*) exit 1 ;; esac
    printf 'probe\n' >>"$TEST_DOCKER_LOG"
    ;;
  compose)
    case " $* " in *' up '*) printf '%s\n' "$*" >>"$TEST_DOCKER_LOG" ;; esac
    exit 0 ;;
  ps)
    case " $* " in
      *' publish='*) printf '%s\n' "${TEST_PORT_OWNER:-aaaaaaaaaaaa}" ;;
      *' label=auriga.managed=true '*) printf '%s\n' aaaaaaaaaaaa bbbbbbbbbbbb cccccccccccc ;;
      *) printf '%s\n' aaaaaaaaaaaa ;;
    esac
    ;;
  inspect)
    format=$3; cid=$4
    case "$format" in
      *auriga.managed*) [ "$cid" = bbbbbbbbbbbb ] && echo false || echo true ;;
      *auriga.instance*) echo inst_one ;;
      *com.docker.compose.project*) [ "$cid" = bbbbbbbbbbbb ] && echo other-project || echo auriga-nas-test ;;
      *com.docker.compose.service*) echo edge ;;
      *NetworkSettings.Ports*) echo '127.0.0.1 17443' ;;
      *.Mounts*)
        case "$cid" in
          cccccccccccc) printf '/foreign%s/instances/inst_one/home\n' "$TEST_NAS_DATA/data/agent" ;;
          *) printf '%s/data/agent/instances/inst_one/home\n' "$TEST_NAS_DATA" ;;
        esac
        ;;
      *) exit 1 ;;
    esac
    ;;
  network)
    [ "$2" = inspect ] || exit 1
    [ "${3:-}" = -f ] || exit 0
    case "$4" in
      *Driver*) echo bridge ;;
      *Subnet*) echo 192.0.2.0/26 ;;
      *Gateway*) echo 192.0.2.1 ;;
      *AuxiliaryAddresses*|*IPRange*) : ;;
      *Containers*) printf '%s 192.0.2.10/24\n' "${TEST_IP_OWNER:-aaaaaaaaaaaa}" ;;
      *) exit 1 ;;
    esac
    ;;
  stop|rm) printf '%s %s\n' "$1" "$2" >>"$TEST_DOCKER_LOG" ;;
  *) exit 1 ;;
esac
EOF
cat >"$TMP/bin/ss" <<'EOF'
#!/bin/sh
echo 'LISTEN 0 128 127.0.0.1:17443 0.0.0.0:*'
EOF
cat >"$TMP/bin/crontab" <<'EOF'
#!/bin/sh
# Never alter the real host crontab from this lifecycle test.
if [ "$1" = -l ]; then exit 0; fi
grep -q 'auriga-panel-' "$1" || [ ! -s "$1" ]
EOF
cat >"$TMP/bin/pgrep" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$TMP/bin/docker" "$TMP/bin/ss" "$TMP/bin/crontab" "$TMP/bin/pgrep"
export PATH="$TMP/bin:$PATH"
# All registration entries receive the same label AND path checks as discovery.
printf '%s\n' bbbbbbbbbbbb cccccccccccc unrelated-name >"$DATA/results/registered-instances.txt"
sh "$ROOT/scripts/stop.sh" --with-registered-instances >"$TMP/stop.log"
grep -qx 'stop aaaaaaaaaaaa' "$TEST_DOCKER_LOG"
grep -qx 'rm aaaaaaaaaaaa' "$TEST_DOCKER_LOG"
[ "$(wc -l <"$TEST_DOCKER_LOG" | tr -d ' ')" = 2 ]
printf 'PASS stop_labels_and_exact_mount_boundary\n'

TEST_PORT_OWNER=aaaaaaaaaaaa; export TEST_PORT_OWNER
if sh -c '. "$NAS_ROOT/scripts/lib.sh"; host_port_busy_foreign 17443'; then
  echo 'own port rejected' >&2; exit 1
fi
TEST_PORT_OWNER=bbbbbbbbbbbb; export TEST_PORT_OWNER
sh -c '. "$NAS_ROOT/scripts/lib.sh"; host_port_busy_foreign 17443'
printf 'PASS per_port_owner_checked\n'

TEST_IP_OWNER=aaaaaaaaaaaa; export TEST_IP_OWNER
sh "$ROOT/scripts/network-preflight.sh" >"$TMP/network-own.log"
TEST_IP_OWNER=bbbbbbbbbbbb; export TEST_IP_OWNER
if sh "$ROOT/scripts/network-preflight.sh" >"$TMP/network-foreign.log" 2>&1; then
  echo 'foreign target IP accepted while own edge exists' >&2; exit 1
fi
printf 'PASS exact_ip_member_owner_checked\n'

# Follow the real start chain as well as init/re-init. Source archives keep
# scripts at 0644, so child scripts must work without an executable bit.
TEST_PORT_OWNER=aaaaaaaaaaaa; TEST_IP_OWNER=aaaaaaaaaaaa
export TEST_PORT_OWNER TEST_IP_OWNER
AURIGA_TEST_MIN_MEMORY_KB=1 AURIGA_TEST_MIN_DISK_KB=1 \
  sh "$ROOT/scripts/start.sh" >"$TMP/start.log"
grep -q ' up -d --no-build --pull never$' "$TEST_DOCKER_LOG"
printf 'PASS start_shell_chain\n'
