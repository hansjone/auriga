#!/bin/sh
# Root sets up a non-root operator with root-owned data/config, as container init does.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
[ "$(id -u)" = 0 ] && command -v setpriv >/dev/null || { echo 'SKIP nonroot panel startup: root and setpriv required'; exit 0; }
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
chmod 755 "$temp"
mkdir -p "$temp/operator/auriga-data/config/panel" "$temp/package/scripts" "$temp/bin" "$temp/export"
cp "$root/scripts/"*.sh "$temp/package/scripts/"
find "$temp/package" -type d -exec chmod 755 {} +
chmod 644 "$temp/package/scripts/"*.sh
chown 65534:65534 "$temp/operator"
chmod 755 "$temp/operator" "$temp/operator/auriga-data" "$temp/operator/auriga-data/config"
chown 10001:10001 "$temp/operator/auriga-data/config/panel"
chmod 755 "$temp/operator/auriga-data/config/panel"
touch "$temp/operator/auriga-data/.auriga-initialized" "$temp/crontab" "$temp/docker.log"
chown 65534:65534 "$temp/crontab" "$temp/docker.log"
cat >"$temp/operator/auriga-data/stack.env" <<EOF
AURIGA_DATA_ROOT=$temp/operator/auriga-data
AURIGA_INSTALL_MODE=container
AURIGA_ACCESS_FILE=$temp/access.json
EOF
printf '{}\n' >"$temp/access.json"
for leaf in platform edge agent; do printf 'FIXTURE_KEY=value\n' >"$temp/export/$leaf.env"; done
tar -C "$temp/export" -cf "$temp/export.tar" platform.env edge.env agent.env
for script in preflight network-preflight render; do printf '#!/bin/sh\nexit 0\n' >"$temp/package/scripts/$script.sh"; done
cat >"$temp/package/scripts/installer.sh" <<'EOF'
#!/bin/sh
if [ "$1" = export-env ]; then cat "$TEST_EXPORT"; fi
EOF
cat >"$temp/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_DOCKER_LOG"
case " $* " in *' panel-prepare '*) exit 23 ;; esac
exit 0
EOF
cat >"$temp/bin/crontab" <<'EOF'
#!/bin/sh
echo 'NAS must never call host crontab' >>"$TEST_CRONTAB"
exit 99
EOF
printf '#!/bin/sh\nexit 0\n' >"$temp/bin/pgrep"
chmod 755 "$temp/bin/"*
operator() {
  setpriv --reuid=65534 --regid=65534 --clear-groups env -u NAS_ALLOW_NONROOT -u NAS_INSTALLER_ACTIVE NAS_ROOT="$temp/package" \
    AURIGA_DATA_ROOT="$temp/operator/auriga-data" TEST_EXPORT="$temp/export.tar" \
    TEST_DOCKER_LOG="$temp/docker.log" TEST_CRONTAB="$temp/crontab" PATH="$temp/bin:$PATH" "$@"
}
if [ -n "${PANEL_R1_HELPER:-}" ]; then
  if operator sh "$PANEL_R1_HELPER" install "$temp/package/scripts/renew-panel.sh" "$temp/operator/auriga-data/config/panel" "$temp/operator/auriga-data" >"$temp/r1.log" 2>&1; then echo 'baseline unexpectedly passed' >&2; exit 1; fi
  grep -q 'Permission denied' "$temp/r1.log"
  echo 'PASS reproduced R1 M1: non-root cannot create job in root-owned config'
fi
if ! operator sh "$temp/package/scripts/start.sh" >"$temp/start.log" 2>&1; then cat "$temp/start.log"; exit 1; fi
# Host scheduling is absent; a failed explicit signer does not gate full start.
if operator sh "$temp/package/scripts/renew-panel.sh" >"$temp/manual.log" 2>&1; then exit 1; else [ "$?" = 23 ]; fi
grep -q 'up -d --no-build --pull never' "$temp/docker.log"
[ "$(stat -c '%u' "$temp/operator/auriga-data/config")" = 0 ]
[ "$(stat -c '%u' "$temp/operator/auriga-data/config/panel")" = 10001 ]
[ -z "$(find "$temp/operator" -maxdepth 1 -name '.auriga-panel-*')" ]
if ! operator sh "$temp/package/scripts/stop.sh" >"$temp/stop.log" 2>&1; then cat "$temp/stop.log"; exit 1; fi
[ ! -s "$temp/crontab" ]
printf '#!/bin/sh\nexit 1\n' >"$temp/bin/pgrep"
: >"$temp/docker.log"
if ! operator sh "$temp/package/scripts/start.sh" >"$temp/start.log" 2>&1; then cat "$temp/start.log"; exit 1; fi
grep -q 'up -d --no-build --pull never' "$temp/docker.log"
[ ! -s "$temp/crontab" ]
echo 'PASS non-root NAS start/stop without host cron or scheduler writes; manual signer failure isolated'
