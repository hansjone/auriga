#!/bin/sh
# Connected input preparation only. The public image build consumes these bytes offline.
set -eu
if [ "$#" -ne 2 ] || [ "$1" != prepare ]; then
    echo "usage: $0 prepare ABSOLUTE_EMPTY_OUTPUT_DIRECTORY" >&2; exit 2
fi
out=$2
case "$out" in /*) ;; *) echo "use an absolute output directory" >&2; exit 2 ;; esac
case "${AURIGA_DSH_WEB_INPUTS_DIR:-}" in /*) ;; *) echo "set AURIGA_DSH_WEB_INPUTS_DIR to the approved web inputs" >&2; exit 2 ;; esac
case "${AURIGA_DSH_NODE_IMAGE_CONFIG:-}" in sha256:*) ;; *) echo "set the approved original Node image config SHA256" >&2; exit 2 ;; esac
npm_registry=${AURIGA_DSH_NPM_REGISTRY:-https://registry.npmjs.org}
case "$npm_registry" in
    http://?* | https://?*) ;;
    *) echo "AURIGA_DSH_NPM_REGISTRY must be an http:// or https:// URL" >&2; exit 2 ;;
esac
case "$npm_registry" in
    *[[:space:]]*) echo "AURIGA_DSH_NPM_REGISTRY must not contain whitespace" >&2; exit 2 ;;
esac
mkdir -p "$out"
test -z "$(ls -A "$out")" || { echo "output directory must be empty" >&2; exit 2; }
src=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)
recipe="$src/deploy/compose/real-dsh"
test "$(node -p 'process.platform+"/"+process.arch+"/"+process.versions.node')" = "linux/x64/$(node -p 'require(process.argv[1]).nodeVersion' "$recipe/versions.json")"
test "$(npm --version)" = "$(node -p 'require(process.argv[1]).npmVersion' "$recipe/versions.json")"
web=$(CDPATH='' cd -- "$AURIGA_DSH_WEB_INPUTS_DIR" && pwd)
(cd "$web" && sha256sum --quiet -c SHA256SUMS)
lock=$(sha256sum "$src/web/pnpm-lock.yaml" | cut -d ' ' -f 1)
grep -Fx "$lock  web/pnpm-lock.yaml" "$web/source-locks.sha256" >/dev/null || { echo "web pnpm lock differs; prepare matching web cache" >&2; exit 2; }
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
cache="$scratch/npm-cache"
mkdir "$cache"
if [ -n "${AURIGA_DSH_PREPARE_NPM_CACHE_TAR:-}" ]; then
    case "$AURIGA_DSH_PREPARE_NPM_CACHE_TAR" in /*) ;; *) echo "npm seed must be absolute" >&2; exit 2 ;; esac
    seed_sha=${AURIGA_DSH_PREPARE_NPM_CACHE_SHA256:-}
    test "${#seed_sha}" = 64 || { echo "approved npm seed SHA256 is required" >&2; exit 2; }
    printf '%s  %s\n' "$seed_sha" "$AURIGA_DSH_PREPARE_NPM_CACHE_TAR" | sha256sum --quiet -c -
    tar -xzf "$AURIGA_DSH_PREPARE_NPM_CACHE_TAR" -C "$cache" -- _cacache
fi
export npm_config_userconfig="$scratch/empty-npmrc" npm_config_update_notifier=false
: >"$npm_config_userconfig"
mkdir "$scratch/host" "$scratch/plugin"
cp "$recipe/package.json" "$recipe/package-lock.json" "$scratch/host/"
node --input-type=module - "$src" "$scratch/plugin" <<'NODE'
import {readFileSync,writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
const [root,out]=process.argv.slice(2);
const {productionInputs}=await import(pathToFileURL(join(root,'deploy/compose/real-dsh/inputs.mjs')).href);
const {pkg,lock}=productionInputs(JSON.parse(readFileSync(join(root,'clients/dsh-plugin/package.json'))),JSON.parse(readFileSync(join(root,'clients/dsh-plugin/package-lock.json'))));
writeFileSync(join(out,'package.json'),JSON.stringify(pkg,null,2)+'\n');
writeFileSync(join(out,'package-lock.json'),JSON.stringify(lock,null,2)+'\n');
NODE
for package in host plugin; do
    (cd "$scratch/$package" && npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false \
        --no-audit --no-fund "--registry=$npm_registry" --os=linux --cpu=x64 --libc=glibc --cache "$cache")
done
cp "$web/corepack.tgz" "$out/corepack.tgz"
cp "$web/web-cache.tar.gz" "$out/web-cache.tar.gz"
# No installed project, npmrc, logs, credential or prebuilt broker enters the input.
tar -czf "$out/npm-cache.tar.gz" -C "$cache" _cacache
seed=$(sha256sum "$web/source-locks.sha256" | cut -d ' ' -f 1)
node "$recipe/dependencies.mjs" seal "$src" "$out" "$AURIGA_DSH_NODE_IMAGE_CONFIG" "$seed"
printf 'runtime dependency input files:\n'
(cd "$out" && sha256sum runtime-dependencies.json corepack.tgz web-cache.tar.gz npm-cache.tar.gz)
