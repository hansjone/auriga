#!/bin/sh
# Internal read-only probe, executed in the configured local platform image.
set -eu
service=$1; identity=$2; group=$3; shift 3
failure() {
  printf 'mount-access FAILED service=%s uid:gid=%s supplementary=%s path-category=%s operation=%s\n' \
    "$service" "$identity" "$group" "$category" "$1" >&2
  exit 1
}
directory() { [ -d "$1" ] && [ ! -L "$1" ] && [ -x "$1" ] || failure traverse; }
public() {
  [ -f "$1" ] && [ ! -L "$1" ] || failure public-file
  cat "$1" >/dev/null 2>&1 || failure public-read
}
secret() {
  [ -f "$1" ] && [ ! -L "$1" ] || failure secret-file
  # Only open; never consume or print secret bytes, even on failure.
  ( : <"$1" ) 2>/dev/null || failure secret-open
}
tree() (
  directory "$1"
  [ -r "$1" ] || failure public-list
  for leaf in "$1"/* "$1"/.[!.]* "$1"/..?*; do
    [ -e "$leaf" ] || [ -L "$leaf" ] || continue
    if [ -d "$leaf" ] && [ ! -L "$leaf" ]; then tree "$leaf"; else public "$leaf"; fi
  done
)
postgres_data_phase() (
  # Root may only classify an empty first-start directory, never certify uid999
  # access. If it cannot list/traverse, the runtime identity must still check it.
  phase=runtime
  if [ -d "$1" ] && [ ! -L "$1" ] && [ -r "$1" ] && [ -x "$1" ]; then
    phase=entrypoint-empty
    for leaf in "$1"/* "$1"/.[!.]* "$1"/..?*; do
      [ -e "$leaf" ] || [ -L "$leaf" ] || continue
      phase=runtime
      break
    done
  fi
  printf 'mount-access PGDATA phase=%s\n' "$phase"
)
command -v cat >/dev/null 2>&1 || { category=probe-runtime; failure missing-cat; }
for spec; do
  category=${spec%%:*}; rest=${spec#*:}; kind=${rest%%:*}; path=/probe/$category
  case "$kind" in
    dir) directory "$path" ;;
    postgres-data-phase) postgres_data_phase "$path" ;;
    public) public "$path" ;;
    secret) secret "$path" ;;
    tree) tree "$path" ;;
    platform-pki)
      directory "$path"
      for leaf in ca.crt iam.crt workspace.crt; do public "$path/$leaf"; done
      for leaf in ca.key iam.key workspace.key; do secret "$path/$leaf"; done ;;
    edge-pki)
      directory "$path"
      for leaf in ca.crt edge.crt edge-iam.crt proxy-ca.crt proxy-edge.crt; do public "$path/$leaf"; done
      for leaf in edge.key edge-iam.key proxy-edge.key; do secret "$path/$leaf"; done ;;
    installation)
      directory "$path"
      for leaf in internal.properties edge.secret; do secret "$path/$leaf"; done ;;
    join) directory "$path"; secret "$path/token" ;;
    tokens)
      directory "$path"
      # JWT is first published at platform startup; do not require it on init.
      if [ -e "$path/edge" ] || [ -L "$path/edge" ]; then
        directory "$path/edge"
        if [ -e "$path/edge/service.jwt" ] || [ -L "$path/edge/service.jwt" ]; then secret "$path/edge/service.jwt"; fi
      fi ;;
    *) failure unsupported-kind ;;
  esac
done
printf 'mount-access OK service=%s uid:gid=%s supplementary=%s\n' "$service" "$identity" "$group"
