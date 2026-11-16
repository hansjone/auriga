# NAS linux/amd64 源码离线重建

制品维护机按源码锁文件收集依赖，现场从发布索引校验归档、加载基镜像并重建。现场使用独立的工作目录，源码可包含本地代码改动。修改依赖锁文件或上游 Dockerfile 后需在制品维护机重新生成输入并验证。

现场安装、目录布局与升级恢复步骤见[现场操作说明](field-operations.zh.md)。

## 制品维护机

按以下配方所用的镜像标签准备 Linux amd64 基镜像。每个实际下载的标签要与发布索引里的审核摘要一致，并输出 Docker 归档、来源摘要和文件 SHA-256。

~~~sh
bash deploy/nas/offline-build/export-base.sh golang:1.27.3 /outputs/go-linux-amd64.tar.gz
~~~

- 在 golang:1.27.3 中执行 go-inputs.sh prepare /outputs/go。输入由 NAS 的实际 Go 构建目标解析，包含 Go workspace 与独立 bootstrap 模块的公共代理文件、源码锁哈希和逐文件哈希。
- 在 node:22-bookworm 中执行 web-inputs.sh prepare /outputs/web。脚本保存钉定版本的 Corepack 工具包、pnpm store 和 workspace 锁文件哈希。
- 在具有 curl 的 node:22-bookworm 中执行 java-tools.sh /outputs/java-tools。它从 Maven Wrapper 配置和契约工具的 POM 读取版本，校验 Maven 发行 ZIP、Wrapper JAR，以及 Linux amd64 Node 发行包。这个工具包可单独下载和校验。
- 在 eclipse-temurin:25-jdk 中执行 java-inputs.sh prepare /outputs/java。它按生产的 Maven reactor 构建，保留 Wrapper 发行缓存、公共 Maven 依赖和契约工具的 npm 内容缓存。在线生成的应用 JAR 不进入输入包。已冻结的 Maven 发布索引、源码锁覆盖及整栈验收范围见[Forgejo 构建说明](../../../docs/dev/forgejo-only-build.zh.md)。
- 在对应的运行基镜像中执行 system-inputs.sh SERVICE /outputs/system/SERVICE。agent 用 debian:bookworm-slim，platform 用 eclipse-temurin:25-jre，bifrost 用官方 Dockerfile 固定的 alpine:3.22 摘要，operator 用 python:3.13.8-slim-bookworm。edge 直接使用 gcr.io/distroless/static-debian12:nonroot。

以上脚本在本目录。容器的 /outputs 应映射到发布目录，源码绑定为只读。Java 和 Web 脚本将必要源码复制到新建临时目录。归档每个输出目录，登记包版本、精确源码提交、架构、基镜像摘要、归档校验和及锁清单。

Java 采集需要中断后续做时，可将专用可写目录挂进容器，用 AURIGA_JAVA_PREP_CACHE_DIR 指向这个容器内的绝对路径。每次启动使用空输出目录，脚本在复用 Maven、Wrapper 和 npm 缓存前会重新核对源码锁。采集缓存由一个任务独占，现场重建只从发布归档恢复输入。

## 现场重建

从发布索引取得构建输入。核对外层 SHA-256，解包到独立目录，加载索引中同一版本的工具链镜像和运行基镜像。以本地下载目录 release-inputs 为例，文件名以发布索引为准。使用本地 Docker Engine 的默认 BuildKit。设置外网访问限制时保留 Forgejo 和已配置的内网访问规则。

~~~sh
source_dir=$(pwd)
inputs="$source_dir/release-inputs"
artifacts="$source_dir/rebuild-output"

mkdir -p "$artifacts/go"
docker run --rm --pull never --network none --platform linux/amd64 \
  -v "$source_dir:/source:ro" -v "$inputs/go:/inputs:ro" -v "$artifacts/go:/output" \
  -e AURIGA_GO_PROXY_DIR=/inputs golang:1.27.3 \
  sh /source/deploy/nas/offline-build/go-inputs.sh build /output
~~~

Web 与 Java 分别改用 node:22-bookworm 和 eclipse-temurin:25-jdk，并将各自输入目录绑定到 /inputs、输出目录绑定到 /output。Web 设置 AURIGA_WEB_INPUTS_DIR=/inputs 后调用 web-inputs.sh build /output，Java 设置 AURIGA_JAVA_INPUTS_DIR=/inputs 后调用 java-inputs.sh build /output。三个脚本均校验归档内清单与当前源码锁，并要求空输出目录。

Java 重建会将已核验的官方 Node Linux 发行包解开到临时构建目录。原 Maven 插件再校验 Node 版本，按 POM 和 lock 文件运行离线 npm 安装与契约生成。

把系统输入放进 rebuild-output/system/SERVICE，再从已校验的本地基镜像装配。Go 脚本默认编译全部目标，也可在 build /output 后追加程序名，例如 auriga-agent。

~~~sh
sh deploy/nas/offline-build/assemble-image.sh agent auriga/agent:site-rebuild \
  debian:bookworm-slim "$artifacts"
docker run --rm --pull never --network none \
  --entrypoint /usr/local/bin/auriga-agent auriga/agent:site-rebuild --version
~~~

edge 需要 Go 与 Web 输出，platform 需要 Java 与 system/platform，agent 和 bifrost 需要各自的 Go 和系统包，operator 需要 system/operator。两个 Go 辅助程序 auriga-nas-bootstrap 和 runtime-release 与其他 Go 程序一起生成，按公开 NAS 打包配方装入发行目录。

Docker build 的 --network=none 限制 Dockerfile 的运行步骤。验收整台机器的访问范围时，需在目标网络中限制 Docker daemon 的出站，再照发布索引安装和重建。公开 [NAS 装配文档](../README.md) 的安装、启动和运行时验签门禁仍适用。
