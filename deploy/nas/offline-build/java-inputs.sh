#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 prepare|build|locks|hash-lock ABSOLUTE_OUTPUT_DIRECTORY" >&2
    echo "  hash-lock：对已 prepare 的输出目录枚举 repository 构件，写出 services/maven-deps.sha256" >&2
    exit 2
fi
mode=$1
out=$2
case "$mode" in
    prepare|build|locks|hash-lock) ;;
    *) echo "choose prepare, build, locks or hash-lock" >&2; exit 2 ;;
esac
case "$out" in
    /*) ;;
    *) echo "use an absolute output directory" >&2; exit 2 ;;
esac
src=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)

# Maven 锁文件 services/maven-deps.sha256：只用 POSIX sh、find、sha256sum、awk、sort
# （JDK 工具镜像 eclipse-temurin:25-jdk 没有 python3）。规则与 deploy/offline/maven_lock.go 相同：
#   - 非构件文件不参与摘要：_remote.repositories、resolver-status.properties、
#     maven-metadata*.xml（含 .xml.sha1 等）、*.lastUpdated、*.part、*.lock、*.tmp、*.sha1、*.md5；
#   - 其余普通文件都必须能按 group/artifact/version/artifact-version[-classifier].type 推出坐标，
#     推不出、符号链接或其他特殊文件一律失败（不会被悄悄跳过）。
# maven_lock_list 仓库目录 输出文件：写出按 C 语言序排序的「坐标 sha256」行。
# 不用管道串 awk：sh 没有 pipefail，管道中间的失败会被吞掉。
maven_lock_list() {
    list_repo=$1
    list_out=$2
    if [ -n "$(cd "$list_repo" && find . ! -type d ! -type f -print | head -n 1)" ]; then
        echo "Maven 本地仓库含符号链接或特殊文件，拒绝核对：$list_repo" >&2
        return 2
    fi
    (cd "$list_repo" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum) >"$list_out.sums" ||
        { rm -f -- "$list_out.sums"; return 2; }
    LC_ALL=C awk '
        function fail(msg) { print msg > "/dev/stderr"; exit 2 }
        {
            if (substr($0, 1, 1) == "\\") fail("Maven 本地仓库文件名含特殊字符：" $0)
            sum = substr($0, 1, 64); path = substr($0, 67)
            sub(/^\.\//, "", path)
            if (path ~ /[[:space:]]/) fail("Maven 本地仓库文件名含空白：" path)
            n = split(path, parts, "/"); name = parts[n]
            if (name == "_remote.repositories" || name == "resolver-status.properties") next
            if (name ~ /^maven-metadata/ && name ~ /\.xml($|\.)/) next
            if (name ~ /\.(lastUpdated|part|lock|tmp|sha1|md5)$/) next
            if (n < 4) fail("无法识别的 Maven 仓库文件（不在 group/artifact/version/ 下）：" path)
            version = parts[n - 1]; artifact = parts[n - 2]; group = parts[1]
            for (i = 2; i <= n - 3; i++) group = group "." parts[i]
            prefix = artifact "-" version
            if (substr(name, 1, length(prefix)) != prefix) fail("无法识别的 Maven 仓库文件：" path)
            rest = substr(name, length(prefix) + 1)
            if (substr(rest, 1, 1) == "-") {
                rest = substr(rest, 2); dot = index(rest, ".")
                if (dot <= 1 || dot == length(rest)) fail("无法识别的 Maven 仓库文件：" path)
                coord = group ":" artifact ":" substr(rest, 1, dot - 1) ":" substr(rest, dot + 1) ":" version
            } else if (substr(rest, 1, 1) == "." && length(rest) > 1) {
                coord = group ":" artifact ":" substr(rest, 2) ":" version
            } else fail("无法识别的 Maven 仓库文件：" path)
            if (coord in seen) fail("Maven 仓库内坐标重复：" coord)
            seen[coord] = 1
            print coord " " sum
        }' "$list_out.sums" >"$list_out.raw" || { rm -f -- "$list_out.sums" "$list_out.raw"; return 2; }
    LC_ALL=C sort "$list_out.raw" >"$list_out"
    rm -f -- "$list_out.sums" "$list_out.raw"
}

# maven_lock_entries 锁文件 输出文件：写出排序后的条目行；格式错、重复、摘要非 64 位小写十六进制失败。
maven_lock_entries() {
    LC_ALL=C awk '
        function fail(msg) { print msg > "/dev/stderr"; exit 2 }
        {
            line = $0; sub(/\r$/, "", line); sub(/^[ \t]+/, "", line); sub(/[ \t]+$/, "", line)
            if (line == "" || substr(line, 1, 1) == "#") next
            if (line !~ /^[^ \t]+ [0-9a-f]+$/) fail("Maven 锁文件第 " NR " 行格式无效（期望 坐标 sha256）")
            split(line, f, " ")
            if (length(f[2]) != 64) fail("Maven 锁文件第 " NR " 行摘要不是 64 位十六进制")
            k = split(f[1], c, ":")
            if (k != 4 && k != 5) fail("Maven 锁文件第 " NR " 行坐标须为 group:artifact[:classifier]:type:version")
            for (i = 1; i <= k; i++) if (c[i] == "") fail("Maven 锁文件第 " NR " 行坐标有空段")
            if (f[1] in seen) fail("Maven 锁文件重复坐标 " f[1])
            seen[f[1]] = 1
            print f[1] " " f[2]
        }' "$1" >"$2.raw" || { rm -f -- "$2.raw"; return 2; }
    LC_ALL=C sort "$2.raw" >"$2"
    rm -f -- "$2.raw"
}

# verify_maven_distribution wrapper/dists目录 [maven-wrapper.properties]：按源码
# distributionSha256Sum 复核发行 zip（须保留 zip；不符或缺失即失败）。通过时把 zip
# 路径写入全局变量 VERIFIED_MAVEN_ZIP，供 stage_verified_maven_distribution 现解开。
verify_maven_distribution() {
    v_dists=$1
    v_props=${2:-"$src/.mvn/wrapper/maven-wrapper.properties"}
    VERIFIED_MAVEN_ZIP=
    v_want=$(sed -n 's/^distributionSha256Sum=//p' "$v_props" | tr -d '\r')
    v_url=$(sed -n 's/^distributionUrl=//p' "$v_props" | tr -d '\r')
    if ! printf '%s\n' "$v_want" | grep -Exq '[0-9a-f]{64}'; then
        echo "maven-wrapper.properties 的 distributionSha256Sum 无效" >&2
        return 2
    fi
    v_name=$(basename -- "$v_url")
    case "$v_name" in
        *-bin.zip) ;;
        *) echo "意外的 Maven 发行包名：$v_name" >&2; return 2 ;;
    esac
    if [ ! -d "$v_dists" ]; then
        echo "缺少 Maven wrapper/dists：$v_dists" >&2
        return 2
    fi
    # 只认发行 zip 本体；解压目录不参与摘要。
    v_found=$(find "$v_dists" -type f -name "$v_name" -print | LC_ALL=C sort)
    v_count=$(printf '%s\n' "$v_found" | sed '/^$/d' | wc -l | tr -d ' ')
    if [ "$v_count" != 1 ]; then
        echo "Maven wrapper/dists 中应恰好有一份 $v_name（实际 $v_count）；prepare 须保留发行 zip 供 build 按 distributionSha256Sum 复核" >&2
        return 2
    fi
    v_zip=$(printf '%s\n' "$v_found" | head -n 1)
    v_got=$(sha256sum -- "$v_zip" | cut -d' ' -f1)
    if [ "$v_got" != "$v_want" ]; then
        echo "Maven 发行包摘要不符：$v_zip（得 $v_got，期望 $v_want）" >&2
        return 2
    fi
    VERIFIED_MAVEN_ZIP=$v_zip
}

# Seed the canonical wrapper location with the exact same pinned ZIP from the
# mirror. The wrapper derives its cache directory from the download URL.
prepare_mirrored_maven_distribution() {
    pm_mirror=$1
    MAVEN_DISTRIBUTION_ZIP=$(MVNW_REPOURL= java -cp .mvn/wrapper/maven-wrapper.jar \
        .mvn/wrapper/DistributionCachePath.java "$MAVEN_USER_HOME")
    if [ -f "$MAVEN_DISTRIBUTION_ZIP" ]; then
        verify_maven_distribution "$(dirname -- "$MAVEN_DISTRIBUTION_ZIP")"
        return
    fi
    pm_download="$scratch/maven-wrapper-download"
    MAVEN_USER_HOME="$pm_download" MVNW_REPOURL="$pm_mirror" sh .mvn/wrapper/bootstrap.sh
    verify_maven_distribution "$pm_download/wrapper/dists"
    mkdir -p -- "$(dirname -- "$MAVEN_DISTRIBUTION_ZIP")"
    cp -- "$VERIFIED_MAVEN_ZIP" "$MAVEN_DISTRIBUTION_ZIP"
}

configure_maven_ca() {
    ca_bundle=$1
    ca_dir="$scratch/java-ca"
    mkdir -- "$ca_dir"
    cp -- "${JAVA_HOME:?}/lib/security/cacerts" "$ca_dir/cacerts"
    awk -v dir="$ca_dir" '
        /^-----BEGIN CERTIFICATE-----[[:space:]]*$/ {
            if (open) exit 2
            file = dir "/ca-" ++count ".pem"
            open = 1
        }
        open { print > file }
        /^-----END CERTIFICATE-----[[:space:]]*$/ {
            if (!open) exit 2
            close(file)
            open = 0
        }
        END { if (!count || open) exit 2 }
    ' "$ca_bundle"
    for ca_cert in "$ca_dir"/ca-*.pem; do
        keytool -importcert -noprompt -alias "auriga-site-$(basename -- "$ca_cert")" \
            -keystore "$ca_dir/cacerts" -storepass changeit -file "$ca_cert"
    done
    JDK_JAVA_OPTIONS="${JDK_JAVA_OPTIONS:+$JDK_JAVA_OPTIONS }-Djavax.net.ssl.trustStore=\"$ca_dir/cacerts\" -Djavax.net.ssl.trustStorePassword=changeit"
    export JDK_JAVA_OPTIONS
}

prepare_contract_node() {
    node_version=$(sed -n 's/^[[:space:]]*<nodeVersion>\([^<]*\)<\/nodeVersion>[[:space:]]*$/\1/p' services/contracts/pom.xml)
    printf '%s\n' "$node_version" | grep -Exq 'v[0-9]+\.[0-9]+\.[0-9]+'
    version=${node_version#v}
    node_file="node-$node_version-linux-x64.tar.gz"
    node_archive="$repo/com/github/eirslett/node/$version/node-$version-linux-x64.tar.gz"
    node_hash=$(awk -v coordinate="com.github.eirslett:node:linux-x64:tar.gz:$version" \
        '$1 == coordinate { hash = $2; count++ } END { if (count == 1) print hash }' services/maven-deps.sha256)
    if ! printf '%s\n' "$node_hash" | grep -Exq '[0-9a-f]{64}'; then
        echo "缺少 contracts Node $node_version 的唯一锁定摘要" >&2
        return 2
    fi
    (
        node_input=$node_archive
        if [ ! -f "$node_archive" ]; then
            mkdir -p -- "$(dirname -- "$node_archive")"
            node_input=$(mktemp "$node_archive.XXXXXX.tmp")
            trap 'rm -f -- "$node_input"' EXIT
            node_path="dist/$node_version/$node_file"
            case "${AURIGA_JAVA_FILES:-}" in
                file:///*)
                    node_source=${AURIGA_JAVA_FILES#file://}
                    node_source_file="$node_source/$node_path"
                    if [ ! -f "$node_source_file" ]; then node_source_file="$node_source/$node_file"; fi
                    if [ -f "$node_source_file" ]; then
                        cp -P -- "$node_source_file" "$node_input"
                    fi
                    ;;
                https://?*)
                    node_url=${AURIGA_JAVA_FILES%/}
                    java "$src/deploy/nas/offline-build/DownloadJavaInput.java" \
                        "$node_url/$node_path" "$node_url/$node_file" "$node_input"
                    ;;
                *)
                    echo "缺少 contracts Node $node_version，配置 deps.sources.files 或导入已验证套件" >&2
                    exit 2
                    ;;
            esac
        fi
        if [ ! -f "$node_input" ] || [ -L "$node_input" ] ||
            ! printf '%s  %s\n' "$node_hash" "$node_input" | sha256sum -c --status; then
            echo "contracts Node 归档缺失或摘要不符：$node_file" >&2
            exit 2
        fi
        if [ "$node_input" != "$node_archive" ]; then
            mv -T -- "$node_input" "$node_archive"
        fi
    )
}

# stage_verified_maven_distribution 输入dists目录 目标dists目录 [maven-wrapper.properties]：
# 先按 distributionSha256Sum 复核输入里的发行 zip，再只拷贝该 zip 到目标目录并现解开，
# 不信任、不拷贝输入里已预先解压的目录。
stage_verified_maven_distribution() {
    s_src_dists=$1
    s_dst_dists=$2
    s_props=${3:-"$src/.mvn/wrapper/maven-wrapper.properties"}
    verify_maven_distribution "$s_src_dists" "$s_props" || return $?
    s_rel=${VERIFIED_MAVEN_ZIP#"$s_src_dists"/}
    s_rel_dir=$(dirname -- "$s_rel")
    s_zip_name=$(basename -- "$s_rel")
    s_target="$s_dst_dists/$s_rel_dir"
    mkdir -p -- "$s_target"
    cp -- "$VERIFIED_MAVEN_ZIP" "$s_target/$s_zip_name"
    if command -v unzip >/dev/null 2>&1; then
        (cd -- "$s_target" && unzip -q -- "$s_zip_name")
    elif command -v jar >/dev/null 2>&1; then
        (cd -- "$s_target" && jar xf "$s_zip_name")
        # jar xf 不保留 POSIX 可执行位，恢复 Maven 入口脚本权限。
        chmod +x "$s_target"/*/bin/mvn 2>/dev/null || true
    else
        echo "缺少 unzip 与 jar，无法解开已复核的 Maven 发行包" >&2
        return 2
    fi
}

# verify_maven_lock 仓库 锁文件 [prepare|build]：逐个核对，多出、缺少、摘要不符都失败并列出差异。
# 空锁或缺锁：build 失败；prepare 只警告（便于首次 hash-lock）。prepare 设 AURIGA_MAVEN_LOCK_UPDATE=1
# 时同样只警告不核对——仅供改了 POM、要重新生成锁文件的开发者使用。
verify_maven_lock() {
    v_repo=$1
    v_lock=$2
    v_when=${3:-build}
    v_dir=$(mktemp -d)
    if [ -f "$v_lock" ]; then
        maven_lock_entries "$v_lock" "$v_dir/want" || { rm -rf -- "$v_dir"; return 2; }
    else
        : >"$v_dir/want"
    fi
    if [ ! -s "$v_dir/want" ] || { [ "$v_when" = prepare ] && [ "${AURIGA_MAVEN_LOCK_UPDATE:-}" = 1 ]; }; then
        rm -rf -- "$v_dir"
        if [ "$v_when" = prepare ]; then
            echo "警告：跳过 Maven 锁核对（锁文件为空/不存在或 AURIGA_MAVEN_LOCK_UPDATE=1）；请随后运行 hash-lock 生成 $v_lock 并重新 prepare" >&2
            return 0
        fi
        echo "缺少非空 Maven 锁文件 $v_lock，拒绝用未钉定的 Maven 依赖构建" >&2
        return 2
    fi
    maven_lock_list "$v_repo" "$v_dir/got" || { rm -rf -- "$v_dir"; return 2; }
    cut -d' ' -f1 "$v_dir/want" >"$v_dir/want.coord"
    cut -d' ' -f1 "$v_dir/got" >"$v_dir/got.coord"
    LC_ALL=C comm -23 "$v_dir/want.coord" "$v_dir/got.coord" >"$v_dir/missing"
    LC_ALL=C comm -13 "$v_dir/want.coord" "$v_dir/got.coord" >"$v_dir/extra"
    LC_ALL=C comm -23 "$v_dir/want" "$v_dir/got" | cut -d' ' -f1 |
        LC_ALL=C comm -23 - "$v_dir/missing" >"$v_dir/mismatch"
    v_rc=0
    for v_kind in missing extra mismatch; do
        if [ -s "$v_dir/$v_kind" ]; then
            case "$v_kind" in
                missing) v_label="缺少（锁有、仓库无）" ;;
                extra) v_label="多出（仓库有、锁无）" ;;
                *) v_label="摘要不符" ;;
            esac
            echo "Maven 锁文件与本地仓库不符：$v_label $(wc -l <"$v_dir/$v_kind") 项：" >&2
            head -n 50 "$v_dir/$v_kind" | sed 's/^/  /' >&2
            v_rc=2
        fi
    done
    rm -rf -- "$v_dir"
    return "$v_rc"
}

# hash-lock：从已 prepare 的输出目录生成锁文件（不要求输出目录为空）。
if [ "$mode" = hash-lock ]; then
    if [ ! -d "$out/repository" ]; then
        echo "hash-lock 需要已 prepare 的输出目录（含 repository/）" >&2
        exit 2
    fi
    lock_tmp=$(mktemp)
    {
        echo "# Auriga Maven 依赖锁文件（services/maven-deps.sha256）"
        echo "# 格式：每行 groupId:artifactId[:classifier]:type:version<一个空格>sha256（64 位小写十六进制），按 C 语言序排序；# 开头为注释。"
        echo "# 覆盖 java-inputs.sh prepare 产出的本地仓库全部构件；非构件文件（_remote.repositories、resolver-status.properties、"
        echo "# *.lastUpdated、maven-metadata*.xml、*.sha1/*.md5 等）不参与摘要。Maven 发行包由 maven-wrapper.properties 的"
        echo "# distributionSha256Sum 钉定，不在本文件内。"
        echo "# 生成：AURIGA_MAVEN_LOCK_UPDATE=1 deploy/nas/offline-build/java-inputs.sh prepare <空目录>，"
        echo "#       再 deploy/nas/offline-build/java-inputs.sh hash-lock <该目录>，然后重新 prepare 得到可用的依赖输入。"
    } >"$lock_tmp"
    maven_lock_list "$out/repository" "$lock_tmp.list" || { rm -f -- "$lock_tmp" "$lock_tmp.list"; exit 2; }
    cat -- "$lock_tmp.list" >>"$lock_tmp"
    rm -f -- "$lock_tmp.list"
    entries=$(grep -vc '^#' "$lock_tmp" || true)
    if [ "$entries" -eq 0 ]; then
        rm -f -- "$lock_tmp"
        echo "repository/ 里没有构件，拒绝写出空锁文件" >&2
        exit 2
    fi
    cat -- "$lock_tmp" >"$src/services/maven-deps.sha256"
    rm -f -- "$lock_tmp"
    echo "已写出 $entries 条到 $src/services/maven-deps.sha256" >&2
    exit 0
fi

mkdir -p "$out"
if [ -n "$(ls -A "$out")" ]; then
    echo "output directory is not empty" >&2
    exit 2
fi
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
    services/contracts/package-lock.json services/maven-deps.sha256 >>"$scratch/source-locks"
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
    if [ -n "${AURIGA_JAVA_CA_FILE:-}" ]; then
        configure_maven_ca "$AURIGA_JAVA_CA_FILE"
    fi
    if [ "${AURIGA_JAVA_NO_PUBLIC:-}" = 1 ]; then
        if [ -z "${AURIGA_MAVEN_MIRROR_URL:-}" ]; then
            echo "Java 禁用公网源时必须设置 Maven 镜像" >&2
            exit 2
        fi
        prepare_contract_node
        if [ -z "${npm_config_registry:-}" ]; then
            export npm_config_offline=true
        fi
    fi
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
        if [ "${AURIGA_JAVA_NO_PUBLIC:-}" = 1 ]; then
            unset MVNW_REPOURL
            prepare_mirrored_maven_distribution "$AURIGA_MAVEN_MIRROR_URL"
        else
            sh .mvn/wrapper/bootstrap.sh
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
        sh .mvn/wrapper/bootstrap.sh
        sh mvnw -B -C "-Dmaven.repo.local=$repo" -pl services/platform-app -am -DskipTests package
    fi
    mkdir "$out/wrapper"
    if [ "${AURIGA_JAVA_NO_PUBLIC:-}" = 1 ]; then
        pm_relative=${MAVEN_DISTRIBUTION_ZIP#"$MAVEN_USER_HOME/wrapper/dists"/}
        mkdir -p -- "$out/wrapper/dists/$(dirname -- "$pm_relative")"
        cp -- "$MAVEN_DISTRIBUTION_ZIP" "$out/wrapper/dists/$pm_relative"
    else
        cp -a -- "$MAVEN_USER_HOME/wrapper/dists" "$out/wrapper/dists"
    fi
    # 保留发行 zip，并按源码 distributionSha256Sum 复核，供离线 build 再次核对。
    verify_maven_distribution "$out/wrapper/dists"
    cp -a -- "$repo" "$out/repository"
    find "$out/repository" -type f \( -name '*.lastUpdated' -o -name '*.part' -o -name '*.lock' -o -name '*.tmp' \) -delete
    # prepare 拉完依赖后对照锁文件；空锁仅警告（便于首次 hash-lock）。
    verify_maven_lock "$out/repository" "$src/services/maven-deps.sha256" prepare
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
    # 使用 wrapper/dists 前按源码 distributionSha256Sum 复核发行 zip 并现解开，不信任预解压目录。
    mkdir -p "$MAVEN_USER_HOME/wrapper/dists"
    stage_verified_maven_distribution "$inputs/wrapper/dists" "$MAVEN_USER_HOME/wrapper/dists"
    cp -a -- "$inputs/repository" "$repo"
    # build 用依赖前核对锁文件：核对 Maven 实际要读的副本，不留核对后被替换的窗口。
    verify_maven_lock "$repo" "$src/services/maven-deps.sha256"
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
