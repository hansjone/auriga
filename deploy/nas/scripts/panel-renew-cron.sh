#!/bin/sh
# Host-only scheduler helpers. Private keys remain in the short-lived signer.
set -eu
action=$1; shift
case "$action" in
  install)
    runner=$1; out=$2; data_root=${3:-}
    for value in "$runner" "$out" "$data_root" "$PATH"; do
      case "$value" in *"'"*|*'%'*|*'
'*) echo 'unsupported character in panel scheduler path' >&2; exit 1 ;; esac
    done
    command -v crontab >/dev/null || { echo 'panel renewal requires host crontab' >&2; exit 1; }
    command -v flock >/dev/null || { echo 'panel renewal requires host flock' >&2; exit 1; }
    pgrep -x cron >/dev/null || pgrep -x crond >/dev/null || { echo 'panel renewal requires a running cron daemon' >&2; exit 1; }
    marker="auriga-panel-$(printf '%s' "$runner:$data_root" | cksum | awk '{print $1}')"
    # Keep long host PATHs out of cron's limited command line. The launcher is
    # owner-only and contains no credentials or private key material.
    job="$(dirname "$out")/.panel-renew-job.sh"
    launcher=$(umask 077; mktemp "$(dirname "$out")/.panel-job.XXXXXX")
    printf "#!/bin/sh\nPATH='%s'\nAURIGA_DATA_ROOT='%s'\nexport PATH AURIGA_DATA_ROOT\nexec /bin/sh '%s' --scheduled\n" \
      "$PATH" "$data_root" "$runner" >"$launcher"
    mv -f "$launcher" "$job"
    temp=$(umask 077; mktemp "$(dirname "$out")/.panel-cron.XXXXXX")
    trap 'rm -f -- "$temp" "$temp.old"' EXIT HUP INT TERM
    crontab -l >"$temp.old" 2>/dev/null || :
    awk -v marker="$marker" '$NF != marker' "$temp.old" >"$temp"
    printf "17 * * * * /bin/sh '%s' >> '%s/panel-renew.log' 2>&1 # %s\n" \
      "$job" "$(dirname "$out")" "$marker" >>"$temp"
    crontab "$temp"
    echo "panel renewal hourly cron installed: $marker"
    ;;
  run)
    out=$1; shift
    # Serialize host lifecycle calls too, without changing an existing crontab.
    exec 9>"$(dirname "$out")/.panel-renew.lock"
    flock -n 9 || exit 0
    if "$@"; then exit 0; else result=$?; fi
    printf 'panel renewal failed (exit %s); inspect panel-renew.log and panel-prepare logs\n' "$result" >&2
    temp=$(mktemp "$out/.renew-failed.XXXXXX")
    printf 'panel renewal command failed\n' >"$temp"
    chmod 644 "$temp"
    mv -f "$temp" "$out/.panel-renew-failed"
    exit "$result"
    ;;
  *) exit 2 ;;
esac
