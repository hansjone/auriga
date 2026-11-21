#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 agent|platform|bifrost|operator ABSOLUTE_EMPTY_INPUT_DIRECTORY" >&2
    exit 2
fi
service=$1
out=$2
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
mkdir "$out/packages"

collect_debs() {
    # Resolve against the installed set in the exact deployment base image.
    # Only the package archives enter the dependency pack.
    chmod 0755 "$scratch"
    mkdir -p "$scratch/archives/partial"
    apt-get update
    apt-get -y --download-only --no-install-recommends \
        -o "Dir::Cache::archives=$scratch/archives" install "$@"
    find "$scratch/archives" -maxdepth 1 -type f -name '*.deb' -exec cp -- {} "$out/packages/" \;
    if [ -n "$(ls -A "$out/packages")" ]; then
        (
            cd "$out/packages"
            for package in ./*.deb; do dpkg-deb -f "$package" Package Version Architecture; done
        ) >"$out/packages.txt"
    else
        dpkg-query -W -f='${Package} ${Version} ${Architecture}\n' "$@" >"$out/packages.txt"
    fi
}

case "$service" in
    agent) collect_debs ca-certificates e2fsprogs util-linux ;;
    platform) collect_debs curl openssl ;;
    bifrost)
        apk update
        apk fetch --recursive --output "$out/packages" ca-certificates
        (cd "$out/packages" && find . -type f -name '*.apk' -print | LC_ALL=C sort) >"$out/packages.txt"
        ;;
    operator)
        requirements=deploy/nas/test/admin-bootstrap-requirements.txt
        cd "$src"
        sha256sum "$requirements" >"$out/source-locks.sha256"
        python -m pip download --disable-pip-version-check --no-cache-dir --only-binary=:all: \
            --dest "$out/packages" -r "$requirements"
        (cd "$out/packages" && find . -type f -name '*.whl' -print | LC_ALL=C sort) >"$out/packages.txt"
        ;;
    *) echo "unknown NAS service" >&2; exit 2 ;;
esac

(cd "$out" &&
    find packages -type f -print | LC_ALL=C sort | xargs -r sha256sum > SHA256SUMS &&
    sha256sum packages.txt >> SHA256SUMS &&
    if [ -f source-locks.sha256 ]; then sha256sum source-locks.sha256 >> SHA256SUMS; fi)
