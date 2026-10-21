#!/bin/sh
# Shared helpers for Auriga NAS scripts (POSIX sh, LF).
# shellcheck shell=sh

set -eu

# Fixed project name — never take arbitrary COMPOSE_PROJECT_NAME from stack.env (M1).
AURIGA_NAS_PROJECT=auriga-nas-test
export AURIGA_NAS_PROJECT

nas_scripts_dir() {
  if [ -n "${NAS_ROOT:-}" ]; then
    printf '%s\n' "$NAS_ROOT"
    return 0
  fi
  _lib_dir=$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)
  CDPATH= cd -P -- "$_lib_dir/.." && pwd
}

NAS_ROOT=$(nas_scripts_dir)
export NAS_ROOT

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

info() {
  printf '%s\n' "$*"
}

require_cmd() {
  for _c in "$@"; do
    command -v "$_c" >/dev/null 2>&1 || die "missing required command: $_c"
  done
}

require_root_or_capable_chown() {
  if [ "${NAS_ALLOW_NONROOT:-}" = "1" ]; then
    info "NAS_ALLOW_NONROOT=1: skipping root/chown requirement (test only)"
    return 0
  fi
  if [ "$(id -u 2>/dev/null || echo 1)" = "0" ]; then
    return 0
  fi
  die "init/render must run as root so edge/platform materials can be chown'd (L1)"
}

# Soft chown: hard-fail as root; no-op under NAS_ALLOW_NONROOT.
chown_required() {
  _spec=$1
  shift
  if [ "${NAS_ALLOW_NONROOT:-}" = "1" ]; then
    return 0
  fi
  chown "$_spec" "$@" || die "chown $_spec failed"
}

# Parse KEY=VALUE lines only — no shell execution (L3).
load_env_file() {
  _file=$1
  [ ! -L "$_file" ] || die "env file must not be a symlink: $_file"
  [ -f "$_file" ] || die "env file missing: $_file"
  # Prefer owner-restricted files when present on a multi-user NAS.
  if command -v stat >/dev/null 2>&1; then
    _mode=$(stat -c '%a' "$_file" 2>/dev/null || stat -f '%OLp' "$_file" 2>/dev/null || echo "")
    case "$_mode" in
      600|400|640|440|'') ;;
      *) info "warning: $_file mode=$_mode (prefer 0600)" ;;
    esac
  fi
  while IFS= read -r _line || [ -n "$_line" ]; do
    case "$_line" in
      ''|\#*) continue ;;
    esac
    case "$_line" in
      *=*) ;;
      *) die "invalid env line (not KEY=VALUE): $_file" ;;
    esac
    _key=${_line%%=*}
    _val=${_line#*=}
    case "$_key" in
      *[!A-Za-z0-9_]*|'') die "invalid env key in $_file" ;;
    esac
    # Strip optional surrounding single/double quotes.
    case "$_val" in
      \"*\") _val=${_val#\"}; _val=${_val%\"} ;;
      \'*\') _val=${_val#\'}; _val=${_val%\'} ;;
    esac
    # Reject command substitution / expansion markers.
    case "$_val" in
      *'$('*|*'`'*|*'${'*) die "refusing shell expansion in env value: $_key" ;;
    esac
    export "$_key=$_val"
  done <"$_file"
}

load_stack_env() {
  _root=${AURIGA_DATA_ROOT:-}
  _file=""
  if [ -n "$_root" ] && [ -f "$_root/stack.env" ]; then
    _file="$_root/stack.env"
  elif [ -f "$NAS_ROOT/stack.env" ]; then
    _file="$NAS_ROOT/stack.env"
  elif [ -f "$NAS_ROOT/env.example" ] && [ "${NAS_ALLOW_EXAMPLE_ENV:-}" = "1" ]; then
    _file="$NAS_ROOT/env.example"
  else
    die "stack.env not found (set AURIGA_DATA_ROOT or place stack.env under $NAS_ROOT)"
  fi
  load_env_file "$_file"
  [ -n "${AURIGA_DATA_ROOT:-}" ] || die "AURIGA_DATA_ROOT is required"
  validate_data_root "$AURIGA_DATA_ROOT"
  validate_managed_tree
  # Ignore operator COMPOSE_PROJECT_NAME — always fixed (M1).
  COMPOSE_PROJECT_NAME=$AURIGA_NAS_PROJECT
  export COMPOSE_PROJECT_NAME
}

validate_managed_tree() {
  # User workspaces may contain symlinks; only these deployment-owned trees are scanned.
  for _rel in config secrets join results enrollment bin socket-share data data/agent data/postgres data/nats data/registry; do
    _managed="$AURIGA_DATA_ROOT/$_rel"
    [ ! -L "$_managed" ] || die "managed path is a symlink: $_managed"
  done
  for _rel in config secrets join results enrollment bin socket-share; do
    _managed="$AURIGA_DATA_ROOT/$_rel"
    [ -d "$_managed" ] || continue
    _links=$(find "$_managed" -type l -print) || die "cannot inspect managed tree: $_managed"
    [ -z "$_links" ] || die "symlink inside managed tree: $_managed"
  done
}

validate_ingress_ancestors() {
  [ "${NAS_ALLOW_NONROOT:-}" != "1" ] || return 0
  _ancestor="$AURIGA_DATA_ROOT/socket-share"
  while :; do
    [ -d "$_ancestor" ] && [ ! -L "$_ancestor" ] || die "unsafe ingress ancestor: $_ancestor"
    _owner=$(stat -c '%u' "$_ancestor") || die "cannot inspect ingress owner"
    _mode=$(stat -c '%a' "$_ancestor") || die "cannot inspect ingress mode"
    [ "$_owner" = 0 ] || die "ingress ancestor must be root-owned: $_ancestor"
    case "$_mode" in ''|*[!0-7]*) die "invalid ingress ancestor mode" ;; esac
    if [ "$((0$_mode & 022))" -ne 0 ]; then
      # Root-owned sticky /tmp is allowed only above our private parent.
      [ "$_ancestor" != "$AURIGA_DATA_ROOT/socket-share" ] && [ "$((0$_mode & 01000))" -ne 0 ] \
        || die "ingress ancestor is writable by group/others: $_ancestor (choose a private root; do not loosen permissions)"
    fi
    [ "$_ancestor" != / ] || break
    _ancestor=$(dirname -- "$_ancestor")
  done
}

prepare_socket_parent() {
  _socket_parent="$AURIGA_DATA_ROOT/socket-share"
  [ ! -L "$_socket_parent" ] || die "socket-share must not be a symlink"
  mkdir -p "$_socket_parent"
  chown_required 0:65532 "$_socket_parent"
  chmod 0750 "$_socket_parent" || die "chmod socket-share failed"
  validate_ingress_ancestors
  # Never precreate/mount the leaf: the agent publishes ingress/ by atomic rename.
}

# M3: absolute, no .., no //, no /./, depth >= 3, last component auriga-*, no symlinks on path.
validate_data_root() {
  _root=$1
  [ -n "$_root" ] || die "AURIGA_DATA_ROOT empty"
  case "$_root" in
    /*) ;;
    *) die "AURIGA_DATA_ROOT must be absolute" ;;
  esac
  case "$_root" in
    *..*|*"//"*|*"/./"*|*"/."|*"/. ") die "AURIGA_DATA_ROOT rejects .. // /. segments" ;;
  esac
  case "$_root" in
    /|/share|/var|/tmp|/home|/root|/usr|/etc|/opt) die "AURIGA_DATA_ROOT too shallow or system path: $_root" ;;
  esac
  # At least three components: /a/b/auriga-x
  _depth=0
  _rest=$_root
  while [ -n "$_rest" ] && [ "$_rest" != "/" ]; do
    _depth=$((_depth + 1))
    _rest=$(dirname -- "$_rest")
    [ "$_depth" -gt 20 ] && break
  done
  [ "$_depth" -ge 3 ] || die "AURIGA_DATA_ROOT needs >= 3 path components (got $_depth): $_root"
  _base=$(basename -- "$_root")
  case "$_base" in
    auriga-*) ;;
    *) die "AURIGA_DATA_ROOT basename must start with auriga- (got $_base)" ;;
  esac
  # Reject symlinks along the path (existing components only).
  _check=$_root
  while [ -n "$_check" ] && [ "$_check" != "/" ]; do
    if [ -L "$_check" ]; then
      die "AURIGA_DATA_ROOT path component is a symlink: $_check"
    fi
    _check=$(dirname -- "$_check")
  done
}

compose_cmd() {
  if docker compose version >/dev/null 2>&1; then
    printf '%s\n' "docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then
    printf '%s\n' "docker-compose"
  else
    die "docker compose not available"
  fi
}

run_compose() {
  _cc=$(compose_cmd)
  # shellcheck disable=SC2086
  env AURIGA_DATA_ROOT="$AURIGA_DATA_ROOT" \
    COMPOSE_PROJECT_NAME="$AURIGA_NAS_PROJECT" \
    $_cc -f "$NAS_ROOT/docker-compose.yml" -p "$AURIGA_NAS_PROJECT" "$@"
}

# True if this compose project already has running containers (H4).
project_is_running() {
  command -v docker >/dev/null 2>&1 || return 1
  _n=$(docker ps -q --filter "label=com.docker.compose.project=${AURIGA_NAS_PROJECT}" 2>/dev/null | wc -l | tr -d ' ')
  [ "${_n:-0}" -gt 0 ]
}

# Port busy on host, ignoring listeners owned by this compose project (H4).
host_port_busy_foreign() {
  _port=$1
  host_port_busy "$_port" || return 1
  _port_owners=$(docker ps -q --no-trunc --filter "publish=$_port" 2>/dev/null) || return 0
  [ -n "$_port_owners" ] || return 0
  for _port_owner in $_port_owners; do
    _project=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$_port_owner" 2>/dev/null) || return 0
    [ "$_project" = "$AURIGA_NAS_PROJECT" ] || return 0
    _bindings=$(docker inspect -f '{{range .NetworkSettings.Ports}}{{range .}}{{printf "%s %s\n" .HostIp .HostPort}}{{end}}{{end}}' "$_port_owner" 2>/dev/null) || return 0
    printf '%s\n' "$_bindings" | awk -v port="$_port" '$1 == "127.0.0.1" && $2 == port {found=1} END {exit !found}' || return 0
  done
  return 1
}

host_port_busy() {
  _port=$1
  if command -v ss >/dev/null 2>&1; then
    ss -lnt 2>/dev/null | grep -E ":${_port}([[:space:]]|$)" >/dev/null 2>&1
    return $?
  fi
  if command -v netstat >/dev/null 2>&1; then
    netstat -lnt 2>/dev/null | awk -v wanted="$_port" '
      $1 ~ /^tcp/ && $NF == "LISTEN" {
        count = split($4, address, ":")
        if (address[count] == wanted) found = 1
      }
      END { exit !found }
    '
    return $?
  fi
  die "need ss or netstat to check host ports"
}

# Image ref: local id sha256:… or registry@sha256:… (H5). Returns 0/1 (does not exit).
validate_image_ref() {
  _img=$1
  case "$_img" in
    sha256:[a-fA-F0-9][a-fA-F0-9]*) return 0 ;;
    *@sha256:[a-fA-F0-9][a-fA-F0-9]*) return 0 ;;
    *)
      printf 'invalid image ref (need sha256:<id> or name@sha256:<digest>): %s\n' "$_img" >&2
      return 1
      ;;
  esac
}

rand_alnum() {
  _n=${1:-32}
  _out=""
  if command -v openssl >/dev/null 2>&1; then
    _out=$(openssl rand -base64 "$_n" 2>/dev/null | tr -d '\n/+=' | cut -c1-"$_n") || _out=""
  else
    _out=$(dd if=/dev/urandom bs="$_n" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n' | cut -c1-"$_n") || _out=""
  fi
  if [ -z "$_out" ] || [ "${#_out}" -lt "$_n" ]; then
    die "rand_alnum failed to produce $_n characters (openssl/urandom error)"
  fi
  printf '%s' "$_out"
}

chmod_strict() {
  _mode=$1
  shift
  for _f in "$@"; do
    [ -e "$_f" ] || continue
    chmod "$_mode" "$_f" || die "chmod $_mode failed: $_f"
  done
}

refuse_secret_overwrite() {
  _file=$1
  if [ -e "$_file" ]; then
    die "refusing to overwrite existing secret/config: $_file (remove manually only after backup)"
  fi
}

marker_path() {
  printf '%s\n' "${AURIGA_DATA_ROOT}/.auriga-initialized"
}

release_marker_path() {
  # Release marker lives next to the loaded bundle, not inventing a data root (M3b).
  printf '%s\n' "${AURIGA_RELEASE_DIR:-$NAS_ROOT}/.auriga-release"
}

dir_is_empty() {
  _d=$1
  [ -d "$_d" ] || return 0
  # shellcheck disable=SC2010
  _any=$(ls -A "$_d" 2>/dev/null | head -n 1 || true)
  [ -z "$_any" ]
}

# IPv4 dotted-quad in CIDR (L4) — simple /8 /16 /24 /32 check.
ipv4_in_cidr() {
  _ip=$1
  _cidr=$2
  _net=${_cidr%/*}
  _bits=${_cidr#*/}
  case "$_bits" in
    ''|*[!0-9]*) return 1 ;;
  esac
  _ipn=$(ipv4_to_int "$_ip") || return 1
  _netn=$(ipv4_to_int "$_net") || return 1
  _mask=$(( 0xFFFFFFFF << (32 - _bits) & 0xFFFFFFFF ))
  [ $((_ipn & _mask)) -eq $((_netn & _mask)) ]
}

ipv4_to_int() {
  _a=$1
  oldifs=$IFS
  IFS=.
  # shellcheck disable=SC2086
  set -- $_a
  IFS=$oldifs
  [ "$#" -eq 4 ] || return 1
  printf '%s' $(( ($1 << 24) + ($2 << 16) + ($3 << 8) + $4 ))
}
