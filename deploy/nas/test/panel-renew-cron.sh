#!/bin/sh
# Isolated host scheduler regression; never touches the real crontab or Docker.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir "$temp/bin" "$temp/panel"
cat >"$temp/bin/crontab" <<'EOF'
#!/bin/sh
if [ "$1" = -l ]; then cat "$TEST_CRONTAB"; else cp "$1" "$TEST_CRONTAB"; fi
EOF
cat >"$temp/bin/pgrep" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$temp/bin/"*
export TEST_CRONTAB="$temp/crontab" PATH="$temp/bin:$PATH"
printf '0 1 * * * existing-job\n' >"$TEST_CRONTAB"
helper="$root/scripts/panel-renew-cron.sh"
for iteration in 1 2; do sh "$helper" install "$temp/runner.sh" "$temp/panel"; done
[ "$(wc -l <"$TEST_CRONTAB")" -eq 2 ]
grep -q 'existing-job' "$TEST_CRONTAB"
grep -q '^17 \* \* \* \* /bin/sh ' "$TEST_CRONTAB"
grep -q -- '--scheduled' "$temp/.panel-renew-job.sh"
awk 'length($0)>1024 {exit 1}' "$TEST_CRONTAB"
cat >"$temp/bin/pgrep" <<'EOF'
#!/bin/sh
exit 1
EOF
if sh "$helper" install "$temp/runner.sh" "$temp/panel"; then echo 'missing cron daemon accepted' >&2; exit 1; fi
if sh "$helper" run "$temp/panel" sh -c 'exit 23'; then echo 'failed renewal accepted' >&2; exit 1; else [ "$?" -eq 23 ]; fi
[ -f "$temp/panel/.panel-renew-failed" ]
sh "$helper" run "$temp/panel" true
# Simultaneous cron/manual invocations do not launch another signer.
(
  flock 9
  sh "$helper" run "$temp/panel" sh -c 'touch "$1"' sh "$temp/unexpected"
  : # Keep this shell alive as the lock holder (avoid tail-command exec).
) 9>"$temp/.panel-renew.lock"
[ ! -e "$temp/unexpected" ]
echo 'panel renewal cron regression passed (preserve/idempotent/hourly/failure/lock)'
