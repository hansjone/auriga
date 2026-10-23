#!/bin/sh
# Host + config preflight for Auriga NAS test stack (POSIX sh).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

case "${1:-}" in
  --help)
    echo 'Usage: AURIGA_DATA_ROOT=/srv/auriga-test sh preflight.sh'
    echo 'Checks Docker/Compose, memory, disk, host loopback publish ports, images, paths.'
    exit 0 ;;
  '') ;;
  *) die "unknown argument (use --help)" ;;
esac

load_stack_env
require_cmd docker awk df uname

MIN_MEMORY_KB=${AURIGA_TEST_MIN_MEMORY_KB:-4194304}
MIN_DISK_KB=${AURIGA_TEST_MIN_DISK_KB:-20971520}

case "$(uname -m)" in
  x86_64|amd64) ;;
  *) die "expected Linux amd64; select matching images before deploying" ;;
esac

docker info >/dev/null 2>&1 || die "docker not usable"
docker compose version >/dev/null 2>&1 || die "docker compose plugin required (2.29+)"

if [ -f /proc/meminfo ]; then
  available=$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)
  case "$available" in ''|*[!0-9]*) die "cannot read MemAvailable" ;; esac
  info "available_memory_kb=$available minimum=$MIN_MEMORY_KB"
  [ "$available" -ge "$MIN_MEMORY_KB" ] || die "insufficient available memory"
fi

_parent=$(dirname -- "$AURIGA_DATA_ROOT")
if [ -d "$_parent" ]; then
  free_kb=$(df -Pk "$_parent" | awk 'NR == 2 {print $4}')
  case "$free_kb" in ''|*[!0-9]*) die "cannot read free disk" ;; esac
  info "available_disk_kb=$free_kb minimum=$MIN_DISK_KB"
  [ "$free_kb" -ge "$MIN_DISK_KB" ] || die "insufficient free disk"
fi

busy=0
  for port in \
    "${AURIGA_HOST_PUBLISH_NODECONTROL:?}" \
    "${AURIGA_HOST_PUBLISH_REGISTRY:?}" \
    "${AURIGA_HOST_PUBLISH_PLATFORM_HTTP:?}"
  do
    case "$port" in ''|*[!0-9]*) die "invalid host publish port" ;; esac
    [ "$port" -ge 1024 ] && [ "$port" -le 65535 ] || die "host publish port out of range: $port"
    if host_port_busy_foreign "$port"; then
      info "host_port=$port OCCUPIED (foreign)"
      busy=1
    else
      info "host_port=$port free_at_check_time"
    fi
  done
[ "$busy" -eq 0 ] || die "host publish ports conflict; revise stack.env"
if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  sh "$NAS_ROOT/scripts/installer.sh" check
elif [ -d "$AURIGA_DATA_ROOT/socket-share" ]; then
  validate_ingress_ancestors
fi

for img in \
  "${AURIGA_IMAGE_PLATFORM:?}" \
  "${AURIGA_IMAGE_EDGE:?}" \
  "${AURIGA_IMAGE_AGENT:?}" \
  "${AURIGA_IMAGE_POSTGRES:?}" \
  "${AURIGA_IMAGE_NATS:?}" \
  "${AURIGA_IMAGE_REGISTRY:?}"
do
  validate_image_ref "$img" || die "bad image ref"
  case "${img##*sha256:}" in
    *[!0]*) ;;
    *) die "placeholder image must be replaced: $img" ;;
  esac
  # Inspect the exact configured reference, including RepoDigests. Never pull.
  image_platform=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$img" 2>/dev/null) \
    || die "image not available locally: $img (explicitly prepare images before start)"
  [ "$image_platform" = linux/amd64 ] \
    || die "expected linux/amd64 image and valid inspect output: $img"
done

if [ -e "$AURIGA_DATA_ROOT" ] || [ -L "$AURIGA_DATA_ROOT" ]; then
  if [ ! -f "$(marker_path)" ]; then
    die "data root exists without .auriga-initialized; run init.sh or choose a fresh auriga-* path"
  fi
  info "data_root_present=yes initialized=yes"
else
  info "data_root_present=no (init.sh will create)"
fi

if [ -f "$(marker_path)" ]; then
  run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" config --quiet \
    || die "docker compose config failed"
  info "compose_config=ok"
fi

info "HOST-PREREQUISITES-PASSED"
info "LAN IP allocation NOT verified here — run network-preflight.sh"
