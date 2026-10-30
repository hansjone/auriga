#!/bin/sh
# CLI security boundary only; the Python protocol tests exercise the workflow.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT INT TERM
mkdir -p "$TMP/package/scripts" "$TMP/srv/container/auriga-bootstrap/enrollment" "$TMP/bin"
cp "$ROOT/scripts/lib.sh" "$ROOT/scripts/admin-bootstrap.sh" "$TMP/package/scripts/"
printf '#!/bin/sh\n[ "$1" = check ]\n' >"$TMP/package/scripts/installer.sh"
export AURIGA_DATA_ROOT="$TMP/srv/container/auriga-bootstrap" NAS_ROOT="$TMP/package" BOOTSTRAP_LOG="$TMP/docker.log"
unset NAS_ALLOW_NONROOT NAS_INSTALLER_ACTIVE
touch "$AURIGA_DATA_ROOT/.auriga-initialized" "$AURIGA_DATA_ROOT/enrollment/emergency-enrollment.json" "$TMP/recipient.pem"
printf 'AURIGA_DATA_ROOT=%s\nAURIGA_INSTALL_MODE=container\nAURIGA_IMAGE_LITELLM=sha256:%064d\n' "$AURIGA_DATA_ROOT" 1 >"$AURIGA_DATA_ROOT/stack.env"
printf 'AURIGA_ACCESS_FILE=%s/access.json\n' "$TMP" >>"$AURIGA_DATA_ROOT/stack.env"
chmod 600 "$AURIGA_DATA_ROOT/stack.env"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$BOOTSTRAP_LOG"
case "$1" in
  image) printf 'linux/amd64\n' ;;
  ps) printf '%064d\n' 2 ;;
  run)
    case " $* " in *' --network container:'*' --user 10001:10001 --cap-drop ALL --security-opt no-new-privileges:true --read-only '*) ;; *) exit 90 ;; esac
    case " $* " in *docker.sock*|*' --privileged '*|*' --publish '*|*' --network host '*) exit 91 ;; esac
    exit "${BOOTSTRAP_TEST_EXIT:-0}" ;;
  *) exit 92 ;;
esac
EOF
chmod 755 "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH"
sh "$NAS_ROOT/scripts/admin-bootstrap.sh" "$TMP/recipient.pem"
BOOTSTRAP_TEST_EXIT=73; export BOOTSTRAP_TEST_EXIT
if sh "$NAS_ROOT/scripts/admin-bootstrap.sh" "$TMP/recipient.pem"; then exit 1; else [ "$?" = 73 ]; fi
unset BOOTSTRAP_TEST_EXIT
ln -s "$TMP/recipient.pem" "$TMP/recipient-link.pem"
if sh "$NAS_ROOT/scripts/admin-bootstrap.sh" "$TMP/recipient-link.pem"; then exit 1; fi
if sh "$NAS_ROOT/scripts/admin-bootstrap.sh" recipient.pem; then exit 1; fi
if grep -Eq '(^| )pull( |$)' "$BOOTSTRAP_LOG"; then exit 1; fi
echo 'PASS admin bootstrap: exact edge namespace, restricted helper, exit propagation, canonical public recipient'
