#!/bin/sh
# Run in a local Linux Docker context after cross-building auriga-{panel,edge}-test.
# Both binaries are static; the temporary runtime image has no extra files.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)
node_image=${PANEL_TEST_NODE_IMAGE:-node:22.19.0-bookworm-slim}
id="auriga-panel-renew-test-$$"
temp=$(mktemp -d)
cleanup() {
  result=$?
  if [ "$result" -ne 0 ]; then docker logs "$id-edge" 2>/dev/null || :; fi
  docker rm -f "$id-edge" >/dev/null 2>&1 || :
  for name in keys public dist config; do docker volume rm "$id-$name" >/dev/null 2>&1 || :; done
  docker image rm "$id:local" >/dev/null 2>&1 || :
  rm -rf -- "$temp"
}
trap cleanup EXIT HUP INT TERM
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=panel-test \
  -keyout "$temp/edge.key" -out "$temp/ca.crt" >/dev/null 2>&1
docker build --pull=false -t "$id:local" -f - "$root" <<'EOF'
FROM scratch
COPY auriga-panel-test /auriga-panel
COPY auriga-edge-test /auriga-edge
EOF
for name in keys public dist config; do docker volume create "$id-$name" >/dev/null; done
port=$(shuf -i 20000-60000 -n 1)
fixture() {
  docker run --rm --network none -v "$id-keys:/keys" -v "$id-public:/out" \
    -v "$id-dist:/dist" -v "$id-config:/config" -v "$temp:/input:ro" \
    -v "$root/docker-fixture.mjs:/fixture.mjs:ro" "$node_image" node /fixture.mjs "$@"
}
sign() {
  docker run --rm --network none --user 10001:10001 --cap-drop ALL --read-only \
    --security-opt no-new-privileges:true --entrypoint /auriga-panel \
    -v "$id-keys:/keys:ro" -v "$id-public:/out" -v "$id-dist:/dist:ro" \
    "$id:local" -key /keys/panel.key -dist /dist "$@"
}
health() {
  docker run --rm --network none --user 65532:65532 --cap-drop ALL --read-only \
    --entrypoint /auriga-panel -v "$id-public:/out:ro" "$id:local" -check
}
fixture seed "$port"
sign
fixture verify
fixture age
sign
fixture verify
health
docker run -d --name "$id-edge" --user 65532:65532 --cap-drop ALL --read-only \
  --entrypoint /auriga-edge -v "$id-public:/out:ro" -v "$id-config:/config:ro" \
  "$id:local" -config /config/edge.json -instance-routes memory >/dev/null
docker run --rm --network "container:$id-edge" "$node_image" node -e '
  const http=await import("node:http");
  const end=Date.now()+10000;
  for (;;) { try {
    const [status,text]=await new Promise((resolve,reject)=>{
      const r=http.get({hostname:"127.0.0.1",port:process.argv[1],path:"/panel/manifest.json",headers:{Host:"portal.example.com"}},res=>{
        let text="";res.on("data",data=>text+=data);res.on("end",()=>resolve([res.statusCode,text]));
      });r.on("error",reject);
    });
    if(status!==200 || JSON.parse(text).version!=="0.0.1") throw Error(`wrong manifest: ${status} ${text}`);
    break;
  } catch(e) { if(Date.now()>end) throw e; await new Promise(r=>setTimeout(r,100)); } }
' "$port"
fixture rollback
if sign; then echo 'rollback accepted' >&2; exit 1; fi
if health; then echo 'renewal failure was healthy' >&2; exit 1; fi
fixture restore
sign
health
echo 'PASS Docker: legacy upgrade, renewal, real edge HTTP, rollback, failure health, recovery; zero signer capabilities'
