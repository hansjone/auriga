#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
    echo "usage: $0 ABSOLUTE_EMPTY_OUTPUT_DIRECTORY" >&2
    exit 2
fi
out=$1
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
properties="$src/.mvn/wrapper/maven-wrapper.properties"
distribution=$(sed -n 's/^distributionUrl=//p' "$properties")
distribution_hash=$(sed -n 's/^distributionSha256Sum=//p' "$properties")
wrapper_hash=$(sed -n 's/^wrapperSha256Sum=//p' "$properties")
version=$(sed -n 's/^[[:space:]]*<nodeVersion>\([^<]*\)<\/nodeVersion>[[:space:]]*$/\1/p' "$src/services/contracts/pom.xml")
case "$distribution" in
    https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/*/apache-maven-*-bin.zip) ;;
    *) echo "unexpected Apache Maven distribution location" >&2; exit 2 ;;
esac
printf '%s\n' "$version" | grep -Ex 'v[0-9]+\.[0-9]+\.[0-9]+'
printf '%s\n' "$distribution_hash" | grep -Ex '[a-f0-9]{64}'
printf '%s\n' "$wrapper_hash" | grep -Ex '[a-f0-9]{64}'

maven_file=$(basename -- "$distribution")
node_file="node-$version-linux-x64.tar.gz"
node_release="https://nodejs.org/dist/$version"
download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --retry 2 --connect-timeout 12 --max-time 180 "$1" --output "$2"
}

cp -- "$src/.mvn/wrapper/maven-wrapper.jar" "$out/"
download "$distribution" "$out/$maven_file"
download "$node_release/SHASUMS256.txt" "$out/node-SHASUMS256.txt"
node_hash=$(awk -v file="$node_file" '$2 == file {print $1}' "$out/node-SHASUMS256.txt")
printf '%s\n' "$node_hash" | grep -Ex '[a-f0-9]{64}'
download "$node_release/$node_file" "$out/$node_file"

(
    cd "$out"
    printf '%s  %s\n' "$distribution_hash" "$maven_file" "$wrapper_hash" maven-wrapper.jar \
        "$node_hash" "$node_file" > SHA256SUMS
    sha256sum -c SHA256SUMS
    printf '%s\n' "$distribution" "$node_release/$node_file" "$node_release/SHASUMS256.txt" > sources.txt
    sha256sum node-SHASUMS256.txt sources.txt >> SHA256SUMS
)
