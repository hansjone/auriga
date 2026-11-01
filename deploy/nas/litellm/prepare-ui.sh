#!/bin/sh
# Image-independent preparation; publish the credential pair in one rename.
set -eu
[ "$#" -eq 1 ] || exit 2
die() { printf '%s\n' "$1" >&2; exit 1; }
root=$1
[ ! -L "$root" ] || die 'invalid UI secret directory'
[ ! -e "$root" ] || [ -d "$root" ] || die 'invalid UI secret directory'
parent=$(CDPATH='' cd -- "$(dirname -- "$root")" && pwd -P)
root="$parent/$(basename -- "$root")"
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
if [ "$(id -u)" -ne 0 ]; then
  if [ -n "${AURIGA_INSTALLER_IMAGE:-}" ]; then
    # Standalone Docker-only operators use their existing approved installer,
    # never the LiteLLM service image. Normal NAS render is already inside it.
    image=$AURIGA_INSTALLER_IMAGE
    printf '%s\n' "$image" | grep -Eq '^(sha256:|[^[:space:]]+@sha256:)[a-f0-9]{64}$' \
      || die 'UI preparation requires an immutable local installer image'
    [ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64 ] \
      || die 'UI preparation installer image must already exist locally as linux/amd64'
    exec docker run --rm --pull never --network none --cap-drop ALL \
      --cap-add DAC_OVERRIDE --cap-add CHOWN --cap-add FOWNER \
      --security-opt no-new-privileges:true --user 0:0 \
      -v "$parent:/parent" -v "$here/prepare-ui.sh:/prepare-ui.sh:ro" \
      --entrypoint /bin/sh "$image" /prepare-ui.sh "/parent/$(basename -- "$root")"
  fi
  exec sudo -n sh "$here/prepare-ui.sh" "$root"
fi
for tool in openssl stat mktemp chown chmod mv rmdir grep; do
  command -v "$tool" >/dev/null 2>&1 || die 'UI preparation tool missing'
done
if [ -d "$root" ]; then
  count=0
  for entry in "$root"/* "$root"/.[!.]* "$root"/..?*; do
    [ -e "$entry" ] || [ -L "$entry" ] || continue
    case "$entry" in "$root/password"|"$root/reader") ;; *) die 'incomplete UI secrets' ;; esac
    count=$((count+1))
  done
  if [ "$count" -ne 0 ]; then
    [ "$count" -eq 2 ] && [ "$(stat -c '%u:%g:%a' "$root")" = 0:0:700 ] \
      || die 'incomplete UI secrets or directory metadata drift'
    for name in password reader; do
      leaf="$root/$name"
      [ -f "$leaf" ] && [ ! -L "$leaf" ] && [ "$(stat -c '%u:%g:%a:%h' "$leaf")" = 10001:10004:640:1 ] \
        || die 'UI secret metadata drift'
      case "$name" in password) size=64; pattern='^[0-9a-f]{64}$' ;; reader) size=67; pattern='^sk-[0-9a-f]{64}$' ;; esac
      [ "$(stat -c '%s' "$leaf")" -eq "$size" ] && LC_ALL=C grep -Eq "$pattern" "$leaf" \
        || die 'invalid UI credential'
    done
    exit 0
  fi
fi
umask 077
stage=$(mktemp -d "$parent/.litellm-ui.XXXXXX")
cleanup() { rm -f -- "$stage/password" "$stage/reader"; rmdir -- "$stage"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
chown 0:0 "$stage"
chmod 0700 "$stage"
for name in password reader; do
  value=$(openssl rand -hex 32) || die 'UI credential random generation failed'
  [ "${#value}" -eq 64 ] || die 'UI credential random generation failed'
  case "$value" in *[!0-9a-f]*) die 'UI credential random generation failed' ;; esac
  [ "$name" != reader ] || value="sk-$value"
  printf '%s' "$value" >"$stage/$name"
  unset value
  chown 10001:10004 "$stage/$name"
  chmod 0640 "$stage/$name"
done
# Only a previously empty target is removable; never replace existing secrets.
[ ! -d "$root" ] || rmdir -- "$root"
mv -T -- "$stage" "$root"
trap - EXIT INT TERM HUP
