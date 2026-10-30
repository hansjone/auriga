#!/bin/sh
# Isolated scheduler tests: never reads or writes the host crontab.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir -p "$temp/bin" "$temp/var/panel"
cat >"$temp/bin/crontab" <<'EOF'
#!/bin/sh
if [ "$1" = -l ]; then
  case "${TEST_CRON_READ:-ok}" in
    denied) echo 'permission denied' >&2; exit 1 ;;
    empty_error) exit 2 ;;
    none) echo "no crontab for $(id -un)" >&2; exit 1 ;;
  esac
  cat "$TEST_CRONTAB"
else cp "$1" "$TEST_CRONTAB"; fi
EOF
printf '#!/bin/sh\nexit 0\n' >"$temp/bin/pgrep"
chmod +x "$temp/bin/"*
export TEST_CRONTAB="$temp/crontab" PATH="$temp/bin:$PATH"
printf '0 1 * * * existing-job\n' >"$TEST_CRONTAB"
helper="$root/scripts/panel-renew-cron.sh"
for iteration in 1 2; do sh "$helper" install "$temp/runner.sh" "$temp/var/panel" ""; done
state=$(find "$temp" -maxdepth 1 -name '.auriga-panel-*' -type d)
[ "$(wc -l <"$TEST_CRONTAB")" -eq 2 ]
grep -q 'existing-job' "$TEST_CRONTAB"
grep -q -- '--scheduled' "$state/job.sh"
awk 'length($0)>1024 {exit 1}' "$TEST_CRONTAB"
printf '#!/bin/sh\n[ "$1" = --scheduled ] || exit 1\necho invoked\n' >"$temp/runner.sh"
sh "$state/job.sh"
grep -q invoked "$state/renew.log"
dd if=/dev/zero of="$state/renew.log" bs=1048577 count=1 2>/dev/null
sh "$state/job.sh"
[ -f "$state/renew.log.1" ]
grep -q invoked "$state/renew.log"
cp "$TEST_CRONTAB" "$temp/saved"
for TEST_CRON_READ in denied empty_error; do
  export TEST_CRON_READ
  if sh "$helper" install "$temp/runner.sh" "$temp/var/panel" ""; then exit 1; fi
  cmp "$temp/saved" "$TEST_CRONTAB"
  if sh "$helper" remove "$temp/runner.sh" "$temp/var/panel" ""; then exit 1; fi
  cmp "$temp/saved" "$TEST_CRONTAB"
done
unset TEST_CRON_READ
# A minimal NAS may have cron but no flock. Its default ACL can also override
# umask for mkdir without an explicit mode. Exercise both public entry points.
mkdir "$temp/noflock"
for tool in sh dirname cksum awk readlink stat id mkdir rmdir mktemp rm cat cp mv date wc; do
  ln -s "$(command -v "$tool")" "$temp/noflock/$tool"
done
cp "$temp/bin/crontab" "$temp/bin/pgrep" "$temp/noflock/"
real_mkdir=$(command -v mkdir)
rm "$temp/noflock/mkdir"
cat >"$temp/noflock/mkdir" <<EOF
#!/bin/sh
'$real_mkdir' "\$@" || exit \$?
case " \$* " in *' -m 0700 '*) ;; *) chmod 0770 "\$@" ;; esac
EOF
chmod 755 "$temp/noflock/mkdir"
# chmod is used by the fixture, never by the scheduler to repair old state.
ln -s "$(command -v chmod)" "$temp/noflock/chmod"
minimal_runner="$temp/minimal-runner.sh"
minimal_marker="auriga-panel-$(printf '%s' "$minimal_runner:" | cksum | awk '{print $1}')"
minimal_state="$temp/.$minimal_marker"
PATH="$temp/noflock" sh "$helper" install "$minimal_runner" "$temp/var/panel" ""
[ "$(stat -c '%a' "$minimal_state")" = 700 ]
PATH="$temp/noflock" sh "$helper" run "$minimal_runner" "$temp/var/panel" "" true
[ -f "$minimal_state/checked" ]
[ ! -e "$minimal_state/run-lock" ]
if PATH="$temp/noflock" sh "$helper" run "$minimal_runner" "$temp/var/panel" "" sh -c 'exit 23'; then exit 1; else [ "$?" -eq 23 ]; fi
[ -f "$minimal_state/failed" ]
[ ! -e "$minimal_state/run-lock" ]
mkdir -m 0700 "$minimal_state/run-lock"
PATH="$temp/noflock" sh "$helper" run "$minimal_runner" "$temp/var/panel" "" sh -c 'touch "$1"' sh "$temp/unexpected-minimal"
[ ! -e "$temp/unexpected-minimal" ]
rmdir "$minimal_state/run-lock"
ln -s "$temp/var" "$minimal_state/run-lock"
if PATH="$temp/noflock" sh "$helper" run "$minimal_runner" "$temp/var/panel" "" true; then exit 1; fi
rm "$minimal_state/run-lock"
PATH="$temp/noflock" sh "$helper" remove "$minimal_runner" "$temp/var/panel" ""
echo 'PASS minimal NAS: explicit private mode, cron without flock, success/failure cleanup, contention, symlink rejection'
for invalid in "bad'path" 'bad%path' 'bad
path'; do
  if sh "$helper" install "$temp/$invalid" "$temp/var/panel" ""; then exit 1; fi
done
printf '#!/bin/sh\nexit 1\n' >"$temp/bin/pgrep"
if sh "$helper" install "$temp/runner.sh" "$temp/var/panel" ""; then exit 1; fi
printf 'private\n' >"$temp/target"; chmod 600 "$temp/target"
ln -s "$temp/target" "$temp/var/panel/.panel-renew-failed"
if sh "$helper" run "$temp/runner.sh" "$temp/var/panel" "" sh -c 'exit 23'; then exit 1; else [ "$?" -eq 23 ]; fi
[ -f "$state/failed" ]
[ "$(stat -c '%a' "$temp/target")" = 600 ]
[ "$(cat "$temp/target")" = private ]
sh "$helper" run "$temp/runner.sh" "$temp/var/panel" "" true
[ ! -e "$state/failed" ]
(
  flock 9
  sh "$helper" run "$temp/runner.sh" "$temp/var/panel" "" sh -c 'touch "$1"' sh "$temp/unexpected"
  :
) 9>"$state/lock"
[ ! -e "$temp/unexpected" ]
sh "$helper" remove "$temp/runner.sh" "$temp/var/panel" ""
[ "$(cat "$TEST_CRONTAB")" = '0 1 * * * existing-job' ]
[ ! -e "$state/job.sh" ]
TEST_CRON_READ=none; export TEST_CRON_READ
printf '#!/bin/sh\nexit 0\n' >"$temp/bin/pgrep"
sh "$helper" install "$temp/runner.sh" "$temp/var/panel" ""
[ "$(wc -l <"$TEST_CRONTAB")" -eq 1 ]
echo 'PASS panel cron R2: idempotent, read errors preserve table, empty table, injection, private status, symlink, contention, remove'
