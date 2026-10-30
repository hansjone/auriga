#!/bin/sh
# Host state is operator-owned and never mounted into the signer.
set -eu
action=$1; runner=$2; out=$3; data_root=${4:-}
shift 4
for value in "$runner" "$out" "$data_root" "$PATH"; do
  case "$value" in *"'"*|*'%'*|*'
'*) echo 'unsupported character in panel scheduler path' >&2; exit 1 ;; esac
done
marker="auriga-panel-$(printf '%s' "$runner:$data_root" | cksum | awk '{print $1}')"
if [ -n "$data_root" ]; then parent=$(dirname -- "$data_root"); else parent=$(dirname -- "$(dirname -- "$out")"); fi
parent=$(CDPATH= cd -P -- "$parent" && pwd) || exit 1
state="$parent/.$marker"
private_state() {
  [ ! -L "$parent" ] && [ "$(readlink -f -- "$parent")" = "$parent" ] || return 1
  owner=$(stat -c '%u' "$parent"); mode=$(stat -c '%a' "$parent")
  [ "$owner" = 0 ] || [ "$owner" = "$(id -u)" ] || return 1
  [ "$((0$mode & 022))" = 0 ] || return 1
  if [ ! -e "$state" ] && [ ! -L "$state" ]; then mkdir -m 0700 "$state" || return 1; fi
  [ -d "$state" ] && [ ! -L "$state" ] && [ "$(stat -c '%u:%a' "$state")" = "$(id -u):700" ]
}
read_crontab() {
  if LC_ALL=C crontab -l >"$temp/table" 2>"$temp/error"; then return 0; else result=$?; fi
  if [ "$result" -eq 1 ] && [ ! -s "$temp/table" ] && [ "$(cat "$temp/error")" = "no crontab for $(id -un)" ]; then return 0; fi
  echo 'cannot read existing crontab; refusing to replace it' >&2
  cat "$temp/error" >&2
  return 1
}
case "$action" in
  install|remove)
    command -v crontab >/dev/null || { echo 'panel auto-renew unavailable: host crontab missing' >&2; exit 1; }
    if [ "$action" = install ]; then
      pgrep -x cron >/dev/null || pgrep -x crond >/dev/null || { echo 'panel auto-renew unavailable: cron daemon stopped' >&2; exit 1; }
    fi
    private_state || { echo 'panel scheduler needs an operator-writable private directory beside the data root' >&2; exit 1; }
    temp=$(umask 077; mktemp -d "$state/.update.XXXXXX")
    trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
    read_crontab || exit 1
    awk -v marker="$marker" '$NF != marker' "$temp/table" >"$temp/next"
    if [ "$action" = install ]; then
      printf "#!/bin/sh\nPATH='%s'\nAURIGA_DATA_ROOT='%s'\nexport PATH AURIGA_DATA_ROOT\n" "$PATH" "$data_root" >"$temp/job"
      printf "if [ -f '%s/renew.log' ] && [ \"\$(wc -c < '%s/renew.log')\" -gt 1048576 ]; then mv -f '%s/renew.log' '%s/renew.log.1'; fi\n" "$state" "$state" "$state" "$state" >>"$temp/job"
      printf "exec /bin/sh '%s' --scheduled >> '%s/renew.log' 2>&1\n" "$runner" "$state" >>"$temp/job"
      mv -f "$temp/job" "$state/job.sh"
      printf "17 * * * * /bin/sh '%s/job.sh' # %s\n" "$state" "$marker" >>"$temp/next"
    fi
    LC_ALL=C crontab "$temp/next"
    if [ "$action" = remove ]; then rm -f -- "$state/job.sh" "$state/failed" "$state/checked"; fi
    echo "panel renewal cron $action: $marker (state: $state)"
    ;;
  run)
    # No writes, chmod or renames in the signer-owned public output directory.
    if ! private_state; then
      echo 'panel host status unavailable; running startup check without host lock' >&2
      exec "$@"
    fi
    if command -v flock >/dev/null; then
      exec 9>"$state/lock"
      if flock -n -E 75 9; then :; else
        result=$?
        if [ "$result" -eq 75 ]; then echo 'panel renewal already running; skipped' >&2; exit 0; fi
        echo 'host lock unavailable; signer OS lock still serializes publication' >&2
        exec "$@"
      fi
    else
      # Atomic private-directory lock for minimal NAS hosts. Never steal a lock
      # based on a PID (which can be reused), or repair unknown existing state.
      if mkdir -m 0700 "$state/run-lock" 2>/dev/null; then
        trap 'rmdir -- "$state/run-lock" 2>/dev/null || :' EXIT
        trap 'exit 130' INT
        trap 'exit 143' HUP TERM
      elif [ -d "$state/run-lock" ] && [ ! -L "$state/run-lock" ] &&
        [ "$(stat -c '%u:%a' "$state/run-lock")" = "$(id -u):700" ]; then
        echo 'panel renewal already running; skipped' >&2
        exit 0
      else
        echo 'invalid panel renewal directory lock; refusing repair' >&2
        exit 1
      fi
    fi
    if "$@"; then
      rm -f -- "$state/failed"
      date -u >"$state/checked"
    else
      result=$?
      printf 'panel renewal command failed (exit %s)\n' "$result" >"$state/failed"
      cat "$state/failed" >&2
      exit "$result"
    fi
    ;;
  *) exit 2 ;;
esac
