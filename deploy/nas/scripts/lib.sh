#!/bin/sh
# Shared helpers for Auriga NAS scripts (POSIX sh, LF).
# shellcheck shell=sh

set -eu

nas_scripts_dir() {
  # $0 may be sourced; prefer explicit NAS_ROOT when set.
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

# Load stack.env from data root or NAS_ROOT without printing values.
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
  # shellcheck disable=SC1090
  set -a
  # shellcheck disable=SC1090
  . "$_file"
  set +a
  [ -n "${AURIGA_DATA_ROOT:-}" ] || die "AURIGA_DATA_ROOT is required"
  [ -n "${COMPOSE_PROJECT_NAME:-}" ] || COMPOSE_PROJECT_NAME=auriga-nas-test
  export COMPOSE_PROJECT_NAME
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
  # Intentionally do not pass --env-file that dumps secrets into process listings beyond stack.env.
  # shellcheck disable=SC2086
  env AURIGA_DATA_ROOT="$AURIGA_DATA_ROOT" \
    COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT_NAME" \
    $_cc -f "$NAS_ROOT/docker-compose.yml" -p "$COMPOSE_PROJECT_NAME" "$@"
}

is_absolute_path() {
  case "$1" in
    /*) return 0 ;;
    *) return 1 ;;
  esac
}

path_under_root() {
  _path=$1
  _root=$2
  case "$_path" in
    "$_root"|"$_root"/*) return 0 ;;
    *) return 1 ;;
  esac
}

# True if TCP port is listening on 127.0.0.1 or * (host). Does not prove a LAN IP is free.
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

rand_alnum() {
  _n=${1:-32}
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 "$_n" | tr -d '\n/+=' | cut -c1-"$_n"
    return 0
  fi
  # BusyBox fallback
  dd if=/dev/urandom bs="$_n" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n' | cut -c1-"$_n"
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
    die "refusing to overwrite existing secret/config: $_file"
  fi
}

marker_path() {
  printf '%s\n' "${AURIGA_DATA_ROOT}/.auriga-initialized"
}

release_marker_path() {
  printf '%s\n' "${AURIGA_DATA_ROOT}/.auriga-release"
}
