#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT INT TERM
mkdir -p "$TMP/package/scripts" "$TMP/srv/container/auriga-join/join" "$TMP/bin"
cp "$ROOT/scripts/lib.sh" "$ROOT/scripts/join-node.sh" "$TMP/package/scripts/"
export AURIGA_DATA_ROOT="$TMP/srv/container/auriga-join" NAS_ROOT="$TMP/package" TEST_JOIN="$TMP/srv/container/auriga-join/join" TEST_INPUT="$TMP/input"
touch "$AURIGA_DATA_ROOT/.auriga-initialized" "$TEST_JOIN/token"
chmod 750 "$TEST_JOIN"
chmod 600 "$TEST_JOIN/token"
printf 'AURIGA_DATA_ROOT=%s\nAURIGA_ACCESS_FILE=%s/access.json\nAURIGA_INSTALL_MODE=container\nAURIGA_INSTALLER_IMAGE=sha256:%064d\n' "$AURIGA_DATA_ROOT" "$TMP" 1 >"$AURIGA_DATA_ROOT/stack.env"
chmod 600 "$AURIGA_DATA_ROOT/stack.env"
printf '%s.%s\n' '019a0000-0000-7000-8000-000000000001' "$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')" >"$TEST_INPUT"
chmod 600 "$TEST_INPUT"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
case "$1" in
 image) printf 'linux/amd64\n';;
 run)
  case " $* " in *' --network none --user 0:0 --cap-drop ALL --cap-add DAC_OVERRIDE --security-opt no-new-privileges:true --read-only '*) ;; *) exit 90;; esac
  case " $* " in *docker.sock*|*' --privileged '*|*' --publish '*) exit 91;; esac
  while [ "$1" != --entrypoint ]; do shift; done
  shift 3
  [ "$1" = -eu ] && [ "$2" = -c ]
  exec sh -eu -c "$3" join-helper "$TEST_JOIN/token" "$TEST_INPUT"
  ;;
 *) exit 92;;
esac
EOF
chmod 755 "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH"
if [ "$(id -u)" = 0 ]; then
 sh "$NAS_ROOT/scripts/join-node.sh" "$TEST_INPUT"
 cmp "$TEST_INPUT" "$TEST_JOIN/token"
 if sh "$NAS_ROOT/scripts/join-node.sh" "$TEST_INPUT"; then exit 1; fi
 : >"$TEST_JOIN/token"; chmod 644 "$TEST_JOIN/token"
 if sh "$NAS_ROOT/scripts/join-node.sh" "$TEST_INPUT"; then exit 1; fi
 chmod 600 "$TEST_JOIN/token"
else printf 'SKIP join_node_root_target (requires root fixture)\n'; fi
chmod 644 "$TEST_INPUT"
if sh "$NAS_ROOT/scripts/join-node.sh" "$TEST_INPUT"; then exit 1; fi
chmod 600 "$TEST_INPUT"
ln -s "$TEST_INPUT" "$TMP/link"
if sh "$NAS_ROOT/scripts/join-node.sh" "$TMP/link"; then exit 1; fi
printf 'invalid\n' >"$TEST_INPUT"
if sh "$NAS_ROOT/scripts/join-node.sh" "$TEST_INPUT"; then exit 1; fi
printf 'PASS join_node_private_input_and_fixed_target\n'
