#!/usr/bin/env bash
# Called only after bootstrap has accepted the sealed installation.
set -euo pipefail
VAR=$1
group_read() {
  if [ "$(id -u)" -eq 0 ]; then chgrp 65532 "$1"; else sudo -n chgrp 65532 "$1"; fi
}
if [ -e "$VAR/edge-https" ] || [ -L "$VAR/edge-https" ]; then
  [ -d "$VAR/edge-https" ] && [ ! -L "$VAR/edge-https" ] && [ "$(stat -c '%u:%g:%a' "$VAR/edge-https")" = "$(id -u):65532:750" ] \
    || { echo 'invalid HTTPS runtime directory' >&2; exit 1; }
else
  (umask 027; mkdir "$VAR/edge-https")
  group_read "$VAR/edge-https"
fi
for path in "$VAR/edge-https"/* "$VAR/edge-https"/.[!.]* "$VAR/edge-https"/..?*; do
  [ -e "$path" ] || [ -L "$path" ] || continue
  [ -f "$VAR/browser-tls/browser.key" ] || { echo 'unexpected HTTPS runtime material' >&2; exit 1; }
  case "${path##*/}" in browser.crt|browser.key) ;; *) echo 'unknown HTTPS runtime leaf' >&2; exit 1 ;; esac
done
if [ -f "$VAR/browser-tls/browser.key" ]; then
  for name in browser.crt browser.key; do
    if [ -e "$VAR/edge-https/$name" ] || [ -L "$VAR/edge-https/$name" ]; then
      [ -f "$VAR/edge-https/$name" ] && [ ! -L "$VAR/edge-https/$name" ] && [ "$(stat -c '%u:%g:%a:%h' "$VAR/edge-https/$name")" = "$(id -u):65532:640:1" ] && cmp -s "$VAR/browser-tls/$name" "$VAR/edge-https/$name" \
        || { echo 'HTTPS runtime copy differs; refusing rotation' >&2; exit 1; }
    else
      (umask 077; cp -- "$VAR/browser-tls/$name" "$VAR/edge-https/$name")
      group_read "$VAR/edge-https/$name"
      chmod 640 "$VAR/edge-https/$name"
    fi
  done
fi
