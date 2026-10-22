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
case "$driver" in
  qnet|macvlan|ipvlan|bridge) ;;
  *) info "unverified external network driver — exit 4"; exit 4 ;;
esac

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

reserved=$(docker network inspect -f '{{range .IPAM.Config}}{{range index . "AuxiliaryAddresses"}}{{println .}}{{end}}{{end}}' "$NET")
for reserved_ip in $reserved; do
  [ "$reserved_ip" != "$IP" ] || die "AURIGA_EDGE_LAN_IP is an IPAM auxiliary reservation"
done
dynamic_ranges=$(docker network inspect -f '{{range .IPAM.Config}}{{if index . "IPRange"}}{{println (index . "IPRange")}}{{end}}{{end}}' "$NET")
for dynamic_range in $dynamic_ranges; do
  if ipv4_in_cidr "$IP" "$dynamic_range"; then
    die "AURIGA_EDGE_LAN_IP overlaps the dynamic IPAM allocation range"
  fi
done

occupied=$(docker network inspect -f '{{range $id, $e := .Containers}}{{printf "%s %s\n" $id $e.IPv4Address}}{{end}}' "$NET")
info "network_members:"
printf '%s\n' "$occupied" | while IFS= read -r _line || [ -n "$_line" ]; do
  [ -n "$_line" ] || continue
  info "  $_line"
done

# Exempt only the actual member holding the exact target address on this network.
own_edge=""
while read -r member address; do
  [ "${address%/*}" = "$IP" ] || continue
  member_project=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$member") || die "cannot inspect LAN member"
  member_service=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.service"}}' "$member") || die "cannot inspect LAN member"
  [ "$member_project" = "$AURIGA_NAS_PROJECT" ] && [ "$member_service" = edge ] \
    || die "LAN IP already assigned on foreign network member: $IP"
  own_edge=$member
done <<EOF
$occupied
EOF
if [ -n "$own_edge" ]; then
  info "LAN IP $IP held by verified project edge on $NET — ok for re-start"
fi

if command -v ip >/dev/null 2>&1; then
  if [ -z "$own_edge" ] && ip neigh show "$IP" 2>/dev/null | grep -qi 'REACHABLE\|DELAY\|STALE\|PERMANENT'; then
    info "neighbor_table mentions $IP — treat as occupied until operator clears ARP/DHCP reservation"
    die "LAN IP appears in neighbor table: $IP"
  fi
fi

info "NETWORK-PRECHECK-PASSED: name/driver/IPAM ok; IP not seen on foreign members"
info "NOTE: absence of ping/TCP response does not prove $IP is unused on the wire"
