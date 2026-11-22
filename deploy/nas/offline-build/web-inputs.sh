#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 prepare|build|locks ABSOLUTE_EMPTY_OUTPUT_DIRECTORY" >&2
    exit 2
fi
mode=$1
out=$2
case "$mode" in
    prepare|build|locks) ;;
    *) echo "choose prepare, build or locks" >&2; exit 2 ;;
esac
case "$out" in
    /*) ;;
    *) echo "use an absolute output directory" >&2; exit 2 ;;
esac
mkdir -p "$out"
if [ -n "$(ls -A "$out")" ]; then
    echo "output directory is not empty" >&2
    exit 2
fi
src=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT

if [ "$(node -p 'process.platform + "/" + process.arch + "/" + process.versions.node.split(".")[0]')" != linux/x64/22 ]; then
    echo "Node 22 linux/amd64 is required" >&2
    exit 2
fi
export COREPACK_HOME="$scratch/corepack" COREPACK_ENABLE_DOWNLOAD_PROMPT=0 CI=1
export XDG_CACHE_HOME="$scratch/policy-cache"
workspace="$scratch/source"
mkdir "$workspace"
# 打包清单与 deploy CLI 共用（设计 D6）：deploy/site/input-sets/web-pack.list
pack_args=
while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
        ''|\#*) continue ;;
    esac
    pack_args="$pack_args $line"
done <"$src/deploy/site/input-sets/web-pack.list"
# 清单条目是不含空白的相对路径，按词展开。
# shellcheck disable=SC2086
(cd "$src" &&
    tar -cf - --exclude=node_modules --exclude=dist --exclude=target $pack_args) |
    tar -xf - -C "$workspace"
cd "$workspace"
find web clients -name package.json -type f -print |
    LC_ALL=C sort | xargs sha256sum >"$scratch/source-locks"
sha256sum package.json pnpm-workspace.yaml .npmrc web/pnpm-lock.yaml >>"$scratch/source-locks"
# locks: print the source-lock inventory a prepare run would record, nothing else.
if [ "$mode" = locks ]; then
    cp -- "$scratch/source-locks" "$out/source-locks.sha256"
    exit 0
fi
manager=$(node -p 'require("./package.json").packageManager')
case "$manager" in
    pnpm@*) ;;
    *) echo "the repository must pin pnpm" >&2; exit 2 ;;
esac
store="$scratch/store"
if [ "$mode" = prepare ]; then
    corepack pack "$manager" -o "$out/corepack.tgz"
    if [ -n "${AURIGA_WEB_PREPARE_STORE_TAR:-}" ]; then
        # The previous inputs' web-cache.tar.gz seeds the content store so only new
        # packages are downloaded. Its lock inventory may differ from the current
        # one; pnpm still verifies every package against the current lockfile.
        tar -xzf "$AURIGA_WEB_PREPARE_STORE_TAR" -C "$scratch" store
        # pnpm also needs its release-age metadata cache; older archives may lack it.
        tar -xzf "$AURIGA_WEB_PREPARE_STORE_TAR" -C "$scratch" policy-cache/pnpm 2>/dev/null || true
    fi
else
    case "${AURIGA_WEB_INPUTS_DIR:-}" in
        /*) ;;
        *) echo "set AURIGA_WEB_INPUTS_DIR to the extracted dependency directory" >&2; exit 2 ;;
    esac
    inputs=$(CDPATH='' cd -- "$AURIGA_WEB_INPUTS_DIR" && pwd)
    (cd "$inputs" && sha256sum --quiet -c SHA256SUMS)
    cmp "$scratch/source-locks" "$inputs/source-locks.sha256"
    export COREPACK_ENABLE_NETWORK=0
    tar -xzf "$inputs/web-cache.tar.gz" -C "$scratch"
fi
corepack install -g --cache-only "${inputs:-$out}/corepack.tgz"
mkdir "$scratch/bin"
corepack enable --install-directory "$scratch/bin" pnpm
export PATH="$scratch/bin:$PATH"
if [ "$mode" = prepare ]; then
    corepack pnpm fetch --frozen-lockfile --store-dir "$store"
    # Modern pnpm rechecks registry provenance and release-age data even
    # with --offline, so ship its metadata as well as the content store.
    test -d "$XDG_CACHE_HOME/pnpm"
    tar -czf "$out/web-cache.tar.gz" -C "$scratch" store policy-cache/pnpm
    cp -- "$scratch/source-locks" "$out/source-locks.sha256"
    (cd "$out" && sha256sum web-cache.tar.gz corepack.tgz source-locks.sha256 > SHA256SUMS)
else
    # 固定时间戳，避免产物内嵌入构建时刻导致指纹/摘要抖动（现场部署 D6）
    export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-0}"
    corepack pnpm install --offline --frozen-lockfile --store-dir "$store"
    corepack pnpm gen:api
    corepack pnpm gen:steps
    for app in portal admin panel; do
        corepack pnpm --filter "@auriga/$app" build
        cp -a -- "web/$app/dist" "$out/$app"
    done
    (cd "$out" && find portal admin panel -type f -print | LC_ALL=C sort | xargs sha256sum > SHA256SUMS)
fi
