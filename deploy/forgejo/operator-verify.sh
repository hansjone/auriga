#!/bin/sh
# Run after download.mjs has verified operator-inputs.json into an empty directory.
set -eu
if [ "$#" -ne 2 ]; then
    echo "usage: $0 ABSOLUTE_DOWNLOAD_DIRECTORY ABSOLUTE_EMPTY_WORK_DIRECTORY" >&2
    exit 2
fi
downloads=$1
work=$2
case "$downloads:$work" in /*:/*) ;; *) exit 2 ;; esac
mkdir -p "$work"
test -z "$(ls -A "$work")"
cd "$work"
cat "$downloads"/auriga-build-operator-base-linux-amd64.tar.gz.part[0-9][0-9][0-9] > base.tar.gz
cat "$downloads"/auriga-build-operator-wheels-linux-amd64.tar.gz.part[0-9][0-9][0-9] > wheels.tar.gz
printf '%s\n' \
    '4c33af41773d16ade196a3e6b597c6f742c42d7759114c45b00cd52b9db00c03  base.tar.gz' \
    '994662bf14687e1662d9cbcb85f27ffa6f6e464c0269d520dc52e365f07459f0  wheels.tar.gz' | sha256sum -c -
mkdir base artifacts
tar -xzf base.tar.gz -C base
tar -xzf wheels.tar.gz -C artifacts
find base artifacts -maxdepth 3 -type f -name SHA256SUMS -print
echo 'Verified archives extracted; inspect archive layout before loading the approved image.'
