#!/bin/sh
# Exercise the public status entry point against a real mutual TLS listener.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
for tool in curl openssl timeout python3; do
  command -v "$tool" >/dev/null || { echo "missing mTLS test dependency: $tool" >&2; exit 1; }
done
umask 077
tmp=$(mktemp -d)
server=
cleanup() {
  if [ -n "$server" ]; then kill "$server" 2>/dev/null || :; wait "$server" 2>/dev/null || :; fi
  rm -rf -- "$tmp"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
cd "$tmp"
export AURIGA_DATA_ROOT="$tmp/auriga-status" NAS_ROOT="$root"
pki="$AURIGA_DATA_ROOT/config/materials/pki"
mkdir -p "$pki" bin no-curl
for name in ca foreign; do
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=$name" \
    -keyout "$name.key" -out "$name.crt" >/dev/null 2>&1
done
issue_leaf() {
  openssl req -new -newkey rsa:2048 -nodes -subj "/CN=$1" -keyout "$1.key" -out "$1.csr" >/dev/null 2>&1
  printf '%s\n' "$2" > extensions
  openssl x509 -req -in "$1.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 1 \
    -extfile extensions -out "$1.crt" >/dev/null 2>&1
}
issue_leaf platform 'subjectAltName=DNS:platform'
issue_leaf other 'subjectAltName=DNS:other'
issue_leaf edge-iam 'extendedKeyUsage=clientAuth'
cp ca.crt edge-iam.crt edge-iam.key "$pki/"
echo 200 > response

python3 - <<'PY' &
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import ssl
import time

class Health(BaseHTTPRequestHandler):
    def handle(self):
        try:
            super().handle()
        except (BrokenPipeError, ConnectionResetError):
            pass  # A client may close immediately after rejecting the certificate.

    def do_GET(self):
        if Path("response").read_text().strip() == "stall":
            time.sleep(5)
            return
        status = int(Path("response").read_text()) if self.path == "/actuator/health" else 404
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def log_message(self, *args):
        pass

def server_context(leaf):
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(leaf + ".crt", leaf + ".key")
    tls.load_verify_locations("ca.crt")
    tls.verify_mode = ssl.CERT_REQUIRED
    return tls

def check_sni(sock, name, context):
    if name != "platform":
        return ssl.ALERT_DESCRIPTION_UNRECOGNIZED_NAME
    if Path("wrong-name").exists():
        sock.context = other

other = server_context("other")
tls = server_context("platform")
tls.set_servername_callback(check_sni)
with ThreadingHTTPServer(("127.0.0.1", 0), Health) as server:
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    Path("port.tmp").write_text(str(server.server_port))
    Path("port.tmp").rename("port")
    server.serve_forever()
PY
server=$!
attempt=0
until [ -s port ]; do
  kill -0 "$server" && [ "$attempt" -lt 100 ] || { echo 'TLS fixture failed to start' >&2; exit 1; }
  sleep 0.05
  attempt=$((attempt + 1))
done

FIXTURE_PORT=$(cat port)
export FIXTURE_PORT FIXTURE_CONTAINER_PKI="$tmp/container-pki"
printf 'AURIGA_DATA_ROOT=%s\nAURIGA_ACCESS_FILE=%s/access.json\nAURIGA_HOST_PUBLISH_PLATFORM_HTTP=%s\n' \
  "$AURIGA_DATA_ROOT" "$tmp" "$FIXTURE_PORT" > "$AURIGA_DATA_ROOT/stack.env"

# Only Compose is simulated. Map its fixed container port/mount to the fixture,
# then execute the actual probe from stdin with the chosen set of client tools.
cat > bin/docker <<'SH'
#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = exec ] && [ "$3" = platform ]; then
    shift 3
    [ "$*" = 'sh -s -- 8081 /etc/auriga/pki' ] || exit 1
    exec sh -s -- "$FIXTURE_PORT" "$FIXTURE_CONTAINER_PKI"
  fi
  shift
done
SH
chmod +x bin/docker
for tool in awk basename dirname env find grep id mktemp openssl readlink rm sed sh stat timeout; do
  ln -s "$(command -v "$tool")" "no-curl/$tool"
done
normal_path="$tmp/bin:$PATH"
openssl_path="$tmp/bin:$tmp/no-curl"

check_status() {
  source=$1 expected=$2
  output=$(PATH="$3" https_proxy=http://127.0.0.1:1 no_proxy='' sh "$root/scripts/status.sh")
  actual=$(printf '%s\n' "$output" | grep '^platform_health_http=')
  observed_source=$(printf '%s\n' "$output" | grep '^platform_health_source=')
  if [ "$actual" != "platform_health_http=$expected" ] || [ "$observed_source" != "platform_health_source=$source" ]; then
    echo "expected $source $expected, got $observed_source $actual" >&2
    exit 1
  fi
  printf '%s\n' "$output" | grep -Fxq 'status done (secrets/env contents not printed)'
}

for path in "$normal_path" "$openssl_path"; do
  echo 200 > response
  check_status host 200 "$path"
  echo 503 > response
  check_status host 503 "$path"
  cp foreign.crt "$pki/ca.crt"
  check_status host 'error (host mTLS probe failed)' "$path"
  cp ca.crt "$pki/ca.crt"
  touch wrong-name
  check_status host 'error (host mTLS probe failed)' "$path"
  rm wrong-name
  echo stall > response
  check_status host 'error (host mTLS probe failed)' "$path"
done
echo 503 > response

mv "$pki" "$AURIGA_DATA_ROOT/config/pki"
check_status host 503 "$normal_path"
mv "$AURIGA_DATA_ROOT/config/pki" "$FIXTURE_CONTAINER_PKI"
check_status container 503 "$openssl_path"
rm "$FIXTURE_CONTAINER_PKI/edge-iam.crt"
check_status container 'unavailable (mTLS files unreadable)' "$normal_path"
echo 'PASS status_platform_real_mtls'
