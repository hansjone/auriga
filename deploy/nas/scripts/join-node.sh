#!/bin/sh
# Import an administrator-issued one-use token without changing join ownership.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 1 ] || die 'Usage: sh join-node.sh /canonical/private/join-token'
load_stack_env
require_cmd docker readlink stat awk
[ -f "$(marker_path)" ] || die 'not initialized'
[ -d "$AURIGA_DATA_ROOT/join" ] && [ ! -L "$AURIGA_DATA_ROOT/join" ] \
 && [ "$(readlink -f -- "$AURIGA_DATA_ROOT/join")" = "$AURIGA_DATA_ROOT/join" ] || die 'join directory must be canonical'
input=$1
case "$input" in /*) ;; *) die 'join token input must be absolute';; esac
[ -f "$input" ] && [ ! -L "$input" ] && [ "$(readlink -f -- "$input")" = "$input" ] || die 'join token input must be canonical and regular'
case "$input" in *','*|*':'*|*'\'*|*'
'*) die 'unsupported token mount path';; esac
[ "$(stat -c '%u:%a:%h' "$input")" = "$(id -u):600:1" ] || die 'token input must be caller-owned mode0600 with one link'
LC_ALL=C awk 'NR != 1 {exit 1} length($0)!=80 || substr($0,37,1)!="." {exit 1} {u=substr($0,1,36); s=substr($0,38); if(u !~ /^[0-9a-f-]+$/ || substr(u,9,1)!="-" || substr(u,14,1)!="-" || substr(u,19,1)!="-" || substr(u,24,1)!="-" || s !~ /^[A-Za-z0-9_-]+$/) exit 1; valid=1} END {if(!valid || NR!=1) exit 1}' "$input" || die 'invalid join token format'
image=${AURIGA_INSTALLER_IMAGE:-}
validate_image_ref "$image" || die 'approved immutable installer image required'
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64 ] || die 'installer image must already exist locally'
docker run --rm --pull never --network none --user 0:0 --cap-drop ALL --cap-add DAC_OVERRIDE \
 --security-opt no-new-privileges:true --read-only \
 --mount "type=bind,src=$input,dst=/input/token,readonly" \
 --mount "type=bind,src=$AURIGA_DATA_ROOT/join,dst=/join" \
 --entrypoint /bin/sh "$image" -eu -c '
target=$1; input=$2
[ "$(stat -c "%u:%g:%a" "$(dirname -- "$target")")" = 0:0:750 ] || { echo "invalid join directory metadata" >&2; exit 1; }
[ -f "$target" ] && [ ! -L "$target" ] && [ "$(stat -c "%u:%g:%a:%h" "$target")" = 0:0:600:1 ] || { echo "invalid join target metadata" >&2; exit 1; }
[ ! -s "$target" ] || { echo "existing join token is not empty; refusing overwrite" >&2; exit 1; }
cat "$input" >"$target"
printf "JOIN_TOKEN_IMPORTED (content not printed)\n"
' join-helper /join/token /input/token
