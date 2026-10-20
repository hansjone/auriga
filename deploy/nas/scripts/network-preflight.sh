#!/bin/sh
# Verify external LAN Docker network exists and target IP is compatible / unused.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

case "${1:-}" in
  --help)
    echo 'Usage: sh network-preflight.sh'
    exit 0 ;;
  '') ;;
  *) die "unknown argument" ;;
esac

load_stack_env
require_cmd docker awk

NET=${AURIGA_LAN_NETWORK:?}
SUBNET=${AURIGA_LAN_SUBNET:?}
IP=${AURIGA_EDGE_LAN_IP:?}

docker network inspect "$NET" >/dev/null 2>&1 || die "external network not found: $NET (will not create it)"

driver=$(docker network inspect -f '{{.Driver}}' "$NET")
info "network=$NET driver=$driver"

cfg_subnets=$(docker network inspect -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' "$NET")
info "network_subnets=$cfg_subnets expected=$SUBNET"
case " $cfg_subnets " in
  *" $SUBNET "*) ;;
  *)
    info "WARNING: configured subnet $SUBNET not listed in network IPAM ($cfg_subnets)"
    info "cannot confirm IPAM compatibility — exit 4"
    exit 4
    ;;
esac

if ! ipv4_in_cidr "$IP" "$SUBNET"; then
  die "AURIGA_EDGE_LAN_IP=$IP is not inside AURIGA_LAN_SUBNET=$SUBNET"
fi

# Reject network / broadcast for /24 and similar (simple check).
_bits=${SUBNET#*/}
_net=${SUBNET%/*}
_ipn=$(ipv4_to_int "$IP")
_netn=$(ipv4_to_int "$_net")
_hosts=$(( (1 << (32 - _bits)) - 1 ))
_bcast=$(( _netn | _hosts ))
if [ "$_ipn" -eq "$_netn" ] || [ "$_ipn" -eq "$_bcast" ]; then
  die "AURIGA_EDGE_LAN_IP is network or broadcast address"
fi

# Gateway / auxiliary addresses from IPAM.
gw=$(docker network inspect -f '{{range .IPAM.Config}}{{.Gateway}} {{end}}' "$NET")
for g in $gw; do
  [ -n "$g" ] || continue
  if [ "$g" = "$IP" ]; then
    die "AURIGA_EDGE_LAN_IP equals network gateway $g"
  fi
done

occupied=$(docker network inspect -f '{{range $id, $e := .Containers}}{{printf "%s %s\n" $e.Name $e.IPv4Address}}{{end}}' "$NET" 2>/dev/null || true)
info "network_members:"
printf '%s\n' "$occupied" | while IFS= read -r _line || [ -n "$_line" ]; do
  [ -n "$_line" ] || continue
  info "  $_line"
done

# If our project edge already holds this IP, allow (H4 idempotent start).
own_edge=""
if project_is_running; then
  own_edge=$(docker ps --filter "label=com.docker.compose.project=${AURIGA_NAS_PROJECT}" \
    --filter "label=com.docker.compose.service=edge" --format '{{.ID}}' 2>/dev/null | head -n 1 || true)
fi

case "$occupied" in
  *" $IP/"*|*"$IP/"*)
    if [ -n "$own_edge" ]; then
      info "LAN IP $IP already held by this project's edge — ok for re-start"
    else
      die "LAN IP already assigned on network: $IP"
    fi
    ;;
esac

if command -v ip >/dev/null 2>&1; then
  if [ -z "$own_edge" ] && ip neigh show "$IP" 2>/dev/null | grep -qi 'REACHABLE\|DELAY\|STALE\|PERMANENT'; then
    info "neighbor_table mentions $IP — treat as occupied until operator clears ARP/DHCP reservation"
    die "LAN IP appears in neighbor table: $IP"
  fi
fi

info "NETWORK-PRECHECK-PASSED: name/driver/IPAM ok; IP not seen on foreign members"
info "NOTE: absence of ping/TCP response does not prove $IP is unused on the wire"
