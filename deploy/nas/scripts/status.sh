#!/bin/sh
# Non-secret status for the Auriga NAS test stack.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd docker

info "project=$AURIGA_NAS_PROJECT"
info "data_root=$AURIGA_DATA_ROOT"
if [ -f "$(marker_path)" ]; then
  info "initialized=yes"
else
  info "initialized=no"
fi

_rel=$(release_marker_path)
if [ -f "$_rel" ]; then
  info "release_marker=$_rel"
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$_rel" <<'PY'
import json, sys
try:
    m = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    print(f"release_parse_error={e}")
    sys.exit(0)
print(f"commit={m.get('commit','')}")
for k, v in (m.get("images") or {}).items():
    print(f"image_{k}_id={v.get('id','')}")
for k, v in (m.get("tools") or {}).items():
    print(f"tool_{k}={v}")
PY
  else
    info "release_marker=present (install python3 to pretty-print JSON)"
  fi
else
  info "release_marker=absent"
fi

run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps || true
if ! run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" exec -T network \
  /auriga-network-probe --certificates /etc/auriga/network; then
  info "network_certificate_status=unavailable_or_invalid (network must be running; see diagnostic above)"
fi

# Run the same request on the host or inside Platform. Only a missing local
# prerequisite permits a container retry; a failed TLS exchange is final.
platform_probe() {
  "$@" <<'PROBE'
port=$1
shift
readable_file() { [ -f "$1" ] && [ -r "$1" ]; }
for pki do
  if readable_file "$pki/ca.crt" && readable_file "$pki/edge-iam.crt" && readable_file "$pki/edge-iam.key"; then
    break
  fi
  pki=
done
if [ -z "$pki" ]; then echo 'unavailable (mTLS files unreadable)'; exit 0; fi
if command -v curl >/dev/null 2>&1; then
  exec curl -q --silent --noproxy '*' --max-time 3 --output /dev/null --write-out '%{http_code}' \
    --cacert "$pki/ca.crt" --cert "$pki/edge-iam.crt" --key "$pki/edge-iam.key" \
    --resolve "platform:$port:127.0.0.1" "https://platform:$port/actuator/health"
fi
if ! command -v openssl >/dev/null 2>&1 || ! command -v timeout >/dev/null 2>&1; then
  echo 'unavailable (need curl or openssl with timeout)'
  exit 0
fi
# s_client versions disagree on EOF without TLS close_notify. Accept only a
# complete HTTP header from the verified stream, never diagnostic text or body.
printf 'GET /actuator/health HTTP/1.0\r\nHost: platform:%s\r\nConnection: close\r\n\r\n' "$port" |
  timeout 3 openssl s_client -quiet -verify_return_error -verify_hostname platform -servername platform \
    -connect "127.0.0.1:$port" -CAfile "$pki/ca.crt" -cert "$pki/edge-iam.crt" -key "$pki/edge-iam.key" 2>/dev/null |
  awk '{ sub(/\r$/, "") }
       NR == 1 && /^HTTP\/1\.[01] [2-5][0-9][0-9]( |$)/ { code = $2 }
       /^$/ { if (code) print code; exit }'
PROBE
}

HP=${AURIGA_HOST_PUBLISH_PLATFORM_HTTP:-}
case "$HP" in
  ''|*[!0-9]*) info 'platform_health_http=unavailable (invalid or missing published port)' ;;
  *)
    source=host
    code=$(platform_probe sh -s -- "$HP" "$AURIGA_DATA_ROOT/config/materials/pki" "$AURIGA_DATA_ROOT/config/pki") || code=error
    case "$code" in
      unavailable*)
        source=container
        code=$(platform_probe run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" exec -T platform \
          sh -s -- 8081 /etc/auriga/pki 2>/dev/null) || code=error
        ;;
    esac
    case "$code" in
      [2-5][0-9][0-9]|'unavailable (mTLS files unreadable)'|'unavailable (need curl or openssl with timeout)') ;;
      *) code="error ($source mTLS probe failed)" ;;
    esac
    info "platform_health_source=$source"
    info "platform_health_http=$code"
    ;;
esac

_managed_origin=${AURIGA_MANAGED_PLATFORM_ORIGIN:-}
_managed_file="$AURIGA_DATA_ROOT/config/collab-runtime/platform/managed-network.json"
if [ -z "$_managed_origin" ]; then
  info "managed_network_state=unconfigured (AURIGA_MANAGED_PLATFORM_ORIGIN empty)"
elif [ ! -f "$_managed_file" ]; then
  info "managed_network_state=unconfigured (collab/managed-network.json absent)"
else
  info "managed_network_state=configured"
fi

if shared_present; then
  info "shared_runtime_state=present"
  if _shared_status=$(shared_action status 2>/dev/null); then
    info "shared_runtime_status=$_shared_status"
  else
    info "shared_runtime_status=unavailable_or_invalid"
  fi
  if shared_replay_action check >/dev/null 2>&1; then
    info "shared_replay_state=ready"
  else
    info "shared_replay_state=not_ready"
  fi
else
  info "shared_runtime_state=absent"
fi

info "status done (secrets/env contents not printed)"
