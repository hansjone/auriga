# NAS 公开预构建安装配方

本包在一个隔离的 Docker 项目 `auriga-nas-test` 中安装 combined、edge、agent、PostgreSQL、NATS 和本机 registry。NAS 不编译源码；用 `.github/workflows/nas-bundle.yml` 对完整40位提交生成包。工人只在本机验证，不访问 NAS、VPS、DNS 或旧 Hub。公开例子一律用 `example.com`、`192.0.2.x`、`/srv/container/auriga-test`，真实值只写私有现场笔记。

## 一份 access 配置

选择包中的 `access-domain.example.json`、`access-ip.example.json` 或 `access-proxy.example.json`，复制到数据根以外的绝对路径（例如 `/srv/container/auriga-access.json`），编辑后将它写入 `stack.env` 的 `AURIGA_ACCESS_FILE`。bootstrap 对原始字节调用生产 `deploymentconfig.Load`，先按完整原 schema 和启动语义校验，再验证 NAS 装配；不会用 Compose 的开发租户、固定 M2 服务端口、模板或 seed 验证器代替生产加载。

- direct HTTP：支持 DNS 域名和规范纯 IPv4，必须明确确认 HTTP；IP 门户与管理使用不同端口，不需要域名、hosts、伪 Host 或额外 IP。domain 实例继续使用独立子域；IP 实例使用 `/i/<instanceId>/`，只面向互信团队，不宣称具有 domain 的同等隔离。
- trusted_proxy HTTPS：外部浏览器访问 HTTPS；前置代理到 edge 的 portal/admin 两个 socket 仍必须 TLS+mTLS，保留完整 authority。独立的代理 CA、`proxy-client` 证书与唯一 URI SAN `auriga://proxy/nas-test` 绑定代理身份，access 的规范 `peerCidrs` 再绑定真实 TCP 来源。转发头不能选择 scheme、tenant、audience。旧版「TLS在VPS终止后明文8080+X-Forwarded-*」配方应迁移，不能继续套用。
- 三个 listener 必须在容器中分别绑定 `0.0.0.0`；内控 listener 单独使用 HTTP，内部 alias 必须与所有 browser Origin 分开。它不发布宿主端口，只接受正式 self/heartbeat。direct 公网 TLS、split 部署及其它 schema 能力需要另行装配，本配方会明确拒绝不支持的组合。

同一个最终 `config/materials/access.json` 只读挂载给 edge、IAM、agent。issuer、team Origin、RP host 从它派生，包含非默认端口；agent 不再使用旧 `INSTANCE_PLATFORM_URL`/`INSTANCE_PARENT_DOMAIN`。旧 `AURIGA_PORTAL_HOST`、`ADMIN_HOST`、`INSTANCE_PARENT_DOMAIN`、`ISSUER`、`TEAM_ORIGIN`、`TEAM_RP_ID`、`TLS_UPSTREAM`、`SECURE_COOKIES`、`TRUSTED_PROXIES` 输入会明确报迁移错误，不静默降级。access 的 tenant 必须由安装者与实际 IAM/org 租户对应；加载成功不代表租户已创建。combined 的既有首次 seed 创建演示租户 `00000000-0000-0000-0000-000000000001`，无 dev profile 或远程紧急登录例外。

## 初始化、启动、停止

工件下载会丢执行位。先核对提交、镜像 manifest 和校验和，再恢复二进制执行位（不是在 NAS 构建）：

```sh
sha256sum -c SHA256SUMS
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap
mkdir -p /srv/container/auriga-test
cp deploy-nas/env.example /srv/container/auriga-test/stack.env
cp deploy-nas/access-ip.example.json /srv/container/auriga-access.json
# 编辑上述两份文件；数据根预置内容只能有 stack.env。
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh deploy-nas/scripts/init.sh
sh deploy-nas/scripts/preflight.sh
sh deploy-nas/scripts/network-preflight.sh
sh deploy-nas/scripts/start.sh
sh deploy-nas/scripts/smoke.sh
sh deploy-nas/scripts/stop.sh
```

所有操作脚本都显式用 `sh` 调用，不依赖执行位。源码 checkout 对应 `deploy/nas/`。数据根须绝对路径、至少三层且 basename 为 `auriga-*`；拒绝链接、点段及未初始化的外来非空目录。首装/root初始化只为新建且空的材料根通过已打开 FD 设置固定 `10001:10001`，再让清空补充组的同一预构建 binary 子进程生成材料。重入按原身份校验 receipt、输入和完整材料，原 key 不轮换；错 owner/mode、额外链接、错误祖先、缺 receipt、template-only、PKI/模板目录链接都拒绝，不自动 chmod/chown 旧材料。

Linux root只从固定 `/proc/self/exe` 打开当前0755普通可执行文件，将该只读FD单独传为子进程fd3并通过 `/proc/self/fd/3` 以10001身份执行，因此bundle可在0700私有目录；材料根的祖先信任与实际可遍历要求仍独立强制，不复制binary或修改bundle目录权限。

`AURIGA_NAS_BOOTSTRAP_BINARY` 可显式指向已校验的预构建 Linux binary（用于独立包布局/CI），不会触发 Go 编译。Windows 可做 `-validate-access`，不支持 NAS 私密材料激活。`NAS_ALLOW_NONROOT` 是旧脚本测试标志，不能绕过固定 uid 的真实材料安装。

operator的 `stack.env` 与派生 `materials/access.env` 分层处理：派生文件整份通过固定9个入口字段的格式检查后才导入，未知、重复、缺项或非法值整份拒绝，不能覆盖binary／数据根／project等控制配置；该格式检查不代表receipt已验证，后续bootstrap的receipt与材料元数据校验仍独立强制。

第一次 `AURIGA_PLATFORM_SEED=true` 仅使用既有 `combined,e2e` seed；完成后设回 false，重新 render 后为 `combined`。不要给 NAS 配方追加 dev、远程 emergency 或 observer 例外。

## 材料与持续服务票据

源材料在 `config/materials/`，包括 `pki/`、identity 签名 key、生产 access、派生配置与 `iam-installation/`。源根、`pki/`、`templates/` 精确为10001:10001/0755；固定源叶子必须是10001:10001的单链接普通文件，所有PKI私钥及identity签名key为0600，其余固定公开材料为0644。NAS用持有的目录FD和no-follow打开验证这些元数据，并在receipt复用、生成封存前后复核。私密安装目录另为0700、文件0600、单链接；所有祖先只信 root/installer且不可组/其它写（root sticky临时祖先例外）。普通重入不修复源文件权限。`config/pki-edge/` 是单独的精确 runtime 叶副本，归65532，私钥0600，绝不包含 CA key、workspace key、IAM server key或proxy client key；identity签名key也独立复制，源文件不chown。

IAM 的 HTTPS HTTP-hop 使用专门 `iam` server 证书（DNS SAN `platform`）和 `edge-iam` client 证书（DNS SAN `edge.auriga.internal`），与节点mTLS和前置代理证书分开。platform 只认可固定 edge 组件网络 IP 和本地健康探针。gRPC 9091 使用同一专用hop mTLS，租户委派固定 `svc:edge`；edge不拿实例Ed25519 key伪造服务JWT。

platform维持uid10001，并加入reader group65532以给新JWT正确设gid。安装凭据目录只读挂载，实际 IAM exporter 每60秒重新认证并原子发布短时JWT；`internal-tokens/` 仅platform可写，edge只读挂载整个目录，逐RPC重读新inode，不挂单文件。输出叶目录0750、文件0640；没有把原文secret写进公开配置或环境。平台健康检查实际使用mTLS并要求当前票据文件非空。

前置HTTPS代理安装时，只把 `config/materials/pki/proxy-ca.crt`、`proxy-client.crt/key` 安装到受信代理的私密目录，不复制任何 CA 私钥或IAM/节点key。`templates/caddy.fragment.example` 分别反代portal/admin TLS listener，使用 `tls_trust_pool`、`tls_client_auth`、固定serverName；示例端口与 `access-proxy.example.json` 配套。配置语法依据 [Caddy官方文档](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)。实际代理IP/CIDR、浏览器证书和解析由部署者配置，本包不会改系统信任/DNS/VPS。

## 网络、权限与预算

`internal` 是 `internal:true` 的组件bridge，成员postgres/nats/platform/registry/edge；`AURIGA_INTERNAL_SUBNET` 和固定 `AURIGA_INTERNAL_EDGE_IP` 必须与LAN和现有路由不冲突。`publish` 只给platform/registry作宿主127.0.0.1端口映射，agent保持host网络。外部LAN网络只连接edge；其portal/admin按access listener实际端口提供服务，后端不接入LAN；内控虽为动态实例桥绑定wildcard，但edge在TCP Accept处联校内核实际本地／对端地址及当前直连前缀：目标须唯一属于非LAN私有接口，peer须处于该目标的同一前缀。LAN目标和任何外部可路由源到私有目标都早于HTTP／IAM被关闭。portal/admin保持原入口协议。网络预检不创建或调整NAS网络，不确定时exit4；端口/目标IP重入只接受本项目精确归属。

`AURIGA_EDGE_LAN_IP` 与 `AURIGA_LAN_SUBNET` 是必填可信安装输入（直接调用bootstrap时对应 `-lan-ip/-lan-subnet`），须为规范IPv4、地址归属该子网且与组件网不重叠。LAN配置纳入receipt输入摘要，变更或旧材料不匹配会拒绝，不自动修复。edge启动及每个新内控连接还核所有LAN IP在自身netns唯一归属UP接口，活跃接口前缀不得跨接口重叠。每连接两份一致的实际接口快照支持动态桥增删，缺失／多义／读取失败保守拒绝。该规则依赖既有LAN、组件及owned实例直连桥拓扑，不识别真实入接口，也不宣称与网络管理不存在竞态；不要另给edge接入外部路由网。

每实例owned bridge仍internal，只有该实例和经标签/身份/归属复核的edge两个成员，独立DNS alias指向内控listener；实例不加入组件网或publish网。共享socket父目录 `socket-share/` 为root:65532/0750，agent创建叶子；agent数据根保持0700，不预建叶子、不把私密agent目录给edge。

容器继续cap-drop ALL、no-new-privileges、非privileged。agent自身因Docker socket+host网络相当于宿主root，只补CHOWN/DAC_OVERRIDE/FOWNER以管理实例；它不是生产多租户强隔离模型。registry无认证但只在宿主loopback发布；前缀白名单不替代镜像摘要/签名信任。platform.env暂仍包含DB/NATS口令和加密key，Docker inspect权限意味着可以看到这些环境。

保留基础预算约3.5–4GiB、约2CPU；实例默认500m CPU、512MiB、128PIDs，容量MAX_INSTANCES=1。policy使用真实 `allowedImageRepos`，只允许当前registry loopback端口下 `auriga/`，镜像须固定repo@sha256摘要。公开real DSH既有模板若要求1024MiB，会被当前512MiB预算拒绝；不能私降模板或手改产物以宣称生命周期通过，需另行明确容量变更。

数据库仍PostgreSQL17 Debian uid999；workspace迁移/运行角色分开，iam/org combined仍沿既有限制单角色，RLS不变。NATS无匿名、监控只容器loopback；平台事件/审计、routes写、edge routes只读+sessions及撤销consumer ACL不扩权，JetStream保留内存/磁盘限制。

停止只操作固定项目及严格核对标签/绑定路径的登记实例，不prune、不down -v、不remove-orphans。升级换精确镜像Id/摘要；DB迁移不能假设换旧镜像即可回退。备份 `secrets/`、`config/materials/`、data/postgres、data/agent、enrollment、stack.env与原access输入；停止前后保持材料，不依赖手工修权限。证书叶约825天，CA约10年，轮换应另行受审并滚动服务，不用普通重入自动换key。

## 验证与证据边界

```sh
(cd deploy/bootstrap && go test -count=1 ./...)
(cd deploy/compose/bootstrap && go test -count=1 ./...)
(cd deploy/nas/bootstrap && go test -count=1 ./... && go build -o ../bin/auriga-nas-bootstrap .)
NAS_ALLOW_NONROOT=0 sh deploy/nas/test/run-tests.sh
(cd infra/agent && AURIGA_NAS_REQUIRE_ROOT_TEST=1 go test -count=1 -run '^TestNASRenderedAgentPolicy$' ./internal/policy)
(cd infra/edge && go test -count=1 ../../deploy/nas/test/nats_policy_test.go)
```

后3条真实权限/安装交叉测试须在独立Linux root测试环境运行；非root普通Go测试具名跳过固定uid安装，自动CI另有强制root步骤，不能以skip当通过。NAS脚本/Go负例在自动PR E2E中运行，不只在手动bundle workflow。smoke检查真实浏览器URL、mTLS健康与未登录拒绝；没有完整E2E hook时仍exit2/PARTIAL。静态Compose、配置加载或本机测试不等于真实NAS网络、浏览器登录、real DSH心跳或IP3完整验收。
