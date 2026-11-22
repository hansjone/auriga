#!/bin/sh
# Python（operator）依赖输入：prepare / locks（统一现场部署第 8 步）。
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 prepare|locks ABSOLUTE_EMPTY_OUTPUT_DIRECTORY" >&2
    exit 2
fi
mode=$1
out=$2
case "$mode" in
    prepare|locks) ;;
    *) echo "choose prepare or locks" >&2; exit 2 ;;
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
requirements=deploy/nas/test/admin-bootstrap-requirements.txt
cd "$src"
sha256sum "$requirements" >"$out/source-locks.sha256"
if [ "$mode" = locks ]; then
    exit 0
fi

mkdir "$out/packages"
# --require-hashes：requirements 须全部钉版本并带 sha256；依赖源由 PIP_INDEX_URL / AURIGA_PIP_INDEX_URL 提供。
index_args=
if [ -n "${AURIGA_PIP_INDEX_URL:-${PIP_INDEX_URL:-}}" ]; then
    index_args="--index-url ${AURIGA_PIP_INDEX_URL:-$PIP_INDEX_URL}"
fi
# shellcheck disable=SC2086
python -m pip download --disable-pip-version-check --no-cache-dir --only-binary=:all: \
    --require-hashes $index_args --dest "$out/packages" -r "$requirements"
(cd "$out/packages" && find . -type f -name '*.whl' -print | LC_ALL=C sort) >"$out/packages.txt"
(cd "$out" &&
    find packages -type f -print | LC_ALL=C sort | xargs -r sha256sum > SHA256SUMS &&
    sha256sum packages.txt source-locks.sha256 >> SHA256SUMS)
