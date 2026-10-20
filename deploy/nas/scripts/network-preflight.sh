#!/bin/sh
# Verify external LAN Docker network exists and target IP is compatible / unused.
# Does NOT create QNAP parent networks. Ping/TCP silence does not prove an IP is free.
# Exit: 0=ok to proceed; 1=conflict; 2=args; 4=cannot confirm (operator must verify).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

case "${1:-}" in
  --help)
    echo 'Usage: sh network-preflight.sh'
    echo 'Requires stack.env: AURIGA_LAN_NETWORK, AURIGA_LAN_SUBNET, AURIGA_EDGE_LAN_IP'
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

# Driver / IPAM summary (no secrets).
driver=$(docker network inspect -f '{{.Driver}}' "$NET")
info "network=$NET driver=$driver"

# Collect configured subnets.
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

# Existing containers on this network and their IPv4 addresses.
occupied=$(docker network inspect -f '{{range $id, $e := .Containers}}{{$e.IPv4Address}} {{end}}' "$NET" 2>/dev/null || true)
info "network_members_ipv4=$occupied"

case "$occupied" in
  *"$IP/"*|*"$IP "*) die "LAN IP already assigned on network: $IP" ;;
esac

# ARP/neigh if available — soft signal only.
if command -v ip >/dev/null 2>&1; then
  if ip neigh show "$IP" 2>/dev/null | grep -qi 'REACHABLE\|DELAY\|STALE\|PERMANENT'; then
    info "neighbor_table mentions $IP — treat as occupied until operator clears ARP/DHCP reservation"
    die "LAN IP appears in neighbor table: $IP"
  fi
fi

info "NETWORK-PRECHECK-PASSED: name/driver/IPAM ok; IP not seen on network members"
info "NOTE: absence of ping/TCP response does not prove $IP is unused on the wire"
