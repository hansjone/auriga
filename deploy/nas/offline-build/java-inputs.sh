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

if [ "$(uname -m)" != x86_64 ] || ! javac -version 2>&1 | grep -q '^javac 25\.'; then
    echo "JDK 25 linux/amd64 is required" >&2
    exit 2
fi
workspace="$scratch/source"
mkdir "$workspace"
# 打包清单与 deploy CLI 共用（设计 D6）：deploy/site/input-sets/java-pack.list
pack_args=
while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
        ''|\#*) continue ;;
    esac
    pack_args="$pack_args $line"
done <"$src/deploy/site/input-sets/java-pack.list"
# 清单条目是不含空白的相对路径，按词展开。
# shellcheck disable=SC2086
(cd "$src" &&
    tar -cf - --exclude=node_modules --exclude=dist --exclude=target $pack_args) |
    tar -xf - -C "$workspace"
cd "$workspace"
find services -name pom.xml -type f -print |
    LC_ALL=C sort | xargs sha256sum >"$scratch/source-locks"
sha256sum pom.xml mvnw .mvn/wrapper/maven-wrapper.properties \
    .mvn/wrapper/maven-wrapper.jar services/contracts/package.json \
    services/contracts/package-lock.json >>"$scratch/source-locks"
# locks: print the source-lock inventory a prepare run would record, nothing else.
if [ "$mode" = locks ]; then
    cp -- "$scratch/source-locks" "$out/source-locks.sha256"
    exit 0
fi

cache="$scratch"
if [ "$mode" = prepare ] && [ -n "${AURIGA_JAVA_PREP_CACHE_DIR:-}" ]; then
    case "$AURIGA_JAVA_PREP_CACHE_DIR" in
        /*) ;;
        *) echo "use an absolute private Java preparation cache directory" >&2; exit 2 ;;
    esac
    cache=$(CDPATH='' cd -- "$AURIGA_JAVA_PREP_CACHE_DIR" && pwd)
    if [ -f "$cache/source-locks" ]; then
        cmp "$scratch/source-locks" "$cache/source-locks"
    else
        cp -- "$scratch/source-locks" "$cache/source-locks"
    fi
fi
export MAVEN_USER_HOME="$cache/maven"
repo="$cache/repository"
npm_cache=services/contracts/target/npm-cache

stage_contract_node() {
    node_version=$(sed -n 's/^[[:space:]]*<nodeVersion>\([^<]*\)<\/nodeVersion>[[:space:]]*$/\1/p' services/contracts/pom.xml)
    version=${node_version#v}
    archive="$repo/com/github/eirslett/node/$version/node-$version-linux-x64.tar.gz"
    if [ -f "$archive" ]; then
        destination=services/contracts/target/node
        distribution="node-$node_version-linux-x64"
        mkdir -p "$destination"
        tar -xzf "$archive" -C "$destination" --strip-components=2 \
            "$distribution/bin/node" "$distribution/lib/node_modules"
        for name in npm npm.cmd npx npx.cmd; do
            program="$destination/node_modules/npm/bin/$name"
            if [ -f "$program" ]; then
                cp -- "$program" "$destination/$name"
                chmod a+x "$destination/$name"
            fi
        done
    fi
}

if [ "$mode" = prepare ]; then
    if [ "$cache" != "$scratch" ]; then
        mkdir -p "$cache/npm-cache" services/contracts/target
        ln -s "$cache/npm-cache" "$npm_cache"
    fi
    sh .mvn/wrapper/bootstrap.sh
    # -C makes Maven fail on a checksum mismatch instead of warning. It only
    # compares against checksums served by the same Central or mirror, so it does
    # NOT protect against a malicious or compromised mirror: pom.xml pins no
    # artifact hashes. Mirror trust is the site's responsibility. Build mode is
    # unchanged.
    if [ -n "${AURIGA_MAVEN_MIRROR_URL:-}" ]; then
        # https only, strict character whitelist (no newline, quote, angle
        # bracket, ampersand or whitespace), checked in the C locale.
        if ! ( LC_ALL=C; export LC_ALL
               case "$AURIGA_MAVEN_MIRROR_URL" in
                   *[!A-Za-z0-9._~:/?%+=,-]*) exit 1 ;;
                   https://?*) exit 0 ;;
                   *) exit 1 ;;
               esac ); then
            echo "AURIGA_MAVEN_MIRROR_URL must be an https URL of letters, digits and ._~:/?%+=,-" >&2
            exit 2
        fi
        xml_url=$(printf '%s' "$AURIGA_MAVEN_MIRROR_URL" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g')
        # The mirror id must be "central": the local repository records the
        # repository id of every downloaded artifact (_remote.repositories), and
        # the offline build resolves them as "central" without any settings file.
        cat >"$scratch/settings.xml" <<SETTINGS
<settings>
  <mirrors>
    <mirror>
      <id>central</id>
      <mirrorOf>central</mirrorOf>
      <url>$xml_url</url>
    </mirror>
  </mirrors>
</settings>
SETTINGS
        sh mvnw -B -C -s "$scratch/settings.xml" "-Dmaven.repo.local=$repo" -pl services/platform-app -am -DskipTests package
    else
        sh mvnw -B -C "-Dmaven.repo.local=$repo" -pl services/platform-app -am -DskipTests package
    fi
    mkdir "$out/wrapper"
    cp -a -- "$MAVEN_USER_HOME/wrapper/dists" "$out/wrapper/dists"
    cp -a -- "$repo" "$out/repository"
    find "$out/repository" -type f \( -name '*.lastUpdated' -o -name '*.part' -o -name '*.lock' -o -name '*.tmp' \) -delete
    cp -a -- "$npm_cache/_cacache" "$out/npm-cacache"
    cp -- "$scratch/source-locks" "$out/source-locks.sha256"
    (cd "$out" &&
        find repository npm-cacache wrapper -type f -print | LC_ALL=C sort | xargs sha256sum > SHA256SUMS &&
        sha256sum source-locks.sha256 >> SHA256SUMS)
else
    case "${AURIGA_JAVA_INPUTS_DIR:-}" in
        /*) ;;
        *) echo "set AURIGA_JAVA_INPUTS_DIR to the extracted dependency directory" >&2; exit 2 ;;
    esac
    inputs=$(CDPATH='' cd -- "$AURIGA_JAVA_INPUTS_DIR" && pwd)
    (cd "$inputs" && sha256sum --quiet -c SHA256SUMS)
    cmp "$scratch/source-locks" "$inputs/source-locks.sha256"
    mkdir "$MAVEN_USER_HOME"
    cp -a -- "$inputs/wrapper" "$MAVEN_USER_HOME/wrapper"
    cp -a -- "$inputs/repository" "$repo"
    mkdir -p "$npm_cache"
    cp -a -- "$inputs/npm-cacache" "$npm_cache/_cacache"
    # Restore the tools from the verified upstream tarball. Maven still checks
    # the Node version and executes the POM's npm and contract generation goals.
    stage_contract_node
    export npm_config_offline=true
    sh mvnw -B -o "-Dmaven.repo.local=$repo" -pl services/platform-app -am -DskipTests package
    cp -- services/platform-app/target/platform-app-*-exec.jar "$out/platform.jar"
    (cd "$out" && sha256sum platform.jar > SHA256SUMS)
fi
