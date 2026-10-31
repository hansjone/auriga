# Auriga NAS isolated test pack

公网预算、时钟容差、断线工具行为与 join 日志定位见 [网络运维说明](../../docs/operations/network-timing.zh.md)。

## 首次安装：装 → 看应急账号 → 登录管理台 → 邀请成员

1. 按[安装指南](../../docs/dev/nas.md#初始化启动停止)准备公开制品、已批准的本地镜像、
   `stack.env` 和 domain / ip / proxy 对应的 access 配置，再执行 `init.sh`、预检和 `start.sh`。
   `init` 准备材料；应急账号由 platform 首次 seed 生成，Compose 等其健康后完成启动。
2. 在私密交互终端运行 `start.sh`，首次成功会直接显示管理台地址、用户名、口令、
   TOTP 密钥和可导入验证器的 `otpauth://` URI。无额外二维码依赖时只提供 URI。
   管理台使用已安装 access 的 `adminOrigin`，proxy 模式显示外部 HTTPS 来源，
   不是内部 listener 或门户链接。无需为手工首装准备收件人公钥。
3. 将 TOTP 导入验证器，直接用浏览器登录显示的管理台地址，再进入“成员 → 邀请”。
   NAS 配方默认允许应急账号远程登录，每次仍须提供口令和 TOTP；不需要 tunnel 或收件公钥。
   若将 `AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=false` 写入 `stack.env`，重新 render 并重启
   平台后会收紧到仅回环来源，此时不能从远程浏览器使用应急账号。
4. 成功显示后仅在已有私有 enrollment 目录记录 `.emergency-displayed`（不含凭据）。
   重复启动不再显示；显示失败保留重试机会，正常 Compose 启动结果不受显示失败影响。
   seed 完成后按指南关闭 `AURIGA_PLATFORM_SEED`，继续用正常成员账号工作。

需要再次查看时，在 NAS 上用 root 或具有本机 Docker 权限的操作者执行：

```sh
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh deploy-nas/scripts/emergency.sh show
```

`show` 只接受 stdout 为 TTY，管道和重定向均拒绝；SSH 使用分配了 TTY 的交互会话。
首次启动被重定向时仅提示执行 `show`，不会显示凭据或写“已显示”标记。
helper 复用已批准的本地 LiteLLM 镜像，以固定 UID10001、无网络、无 Docker socket、
只读根运行；关闭 Docker 日志驱动，不经过 Compose/服务日志，不创建新的明文凭据文件。
终端自身的录屏、会话记录和滚屏仍由操作者管理，不要在共享或录制中的终端查看。
公开命令目前只有 `show`。应急轮换虽有契约定义，平台尚无对应实现，留待后续；
不会改数据库、重跑 seed 或修改现有 enrollment。`show` 读取原始安装凭据，不验证其当前有效性。
默认远程开启使应急登录可经已配置的公开管理入口访问；部署者应保护口令、TOTP 和入口的
网络/TLS 边界，需要收紧时显式设为 false。普通会话、管理来源、CSRF 和 TOTP 校验保持生效。

[`admin-bootstrap.sh`](scripts/admin-bootstrap.sh) 保留为**可选自动化**，不是手工首装必经步骤：
目前只支持 IP 模式，通过真实回环登录、邀请并建立普通管理员/测试成员，将交接材料加密给
操作者提供的公钥。详细过程见[可选自动化说明](../../docs/dev/nas.md#可选admin-bootstrap-自动化真实回环)。

## Managed instances on isolated networks

Managed device registration, tickets, keys, WSS and native model calls can use
an installation-owned internal HTTPS origin, without public DNS or egress.
Set `AURIGA_MANAGED_PLATFORM_HOST` to the exact `instanceControl.dnsAlias` in
`access.json`, and `AURIGA_MANAGED_PLATFORM_ORIGIN` to
`https://<that-alias>:<unused-port>` (for example port `18446`). Add that exact
origin to `AURIGA_LINKS_ALLOWED_ORIGINS`. These values belong in `stack.env`.
The internal bridge gets the same edge alias already used on instance networks;
the NAS resolver, public ingress and instance isolation are unchanged.

Run normal `scripts/render.sh` and `scripts/start.sh` using updated platform,
edge, agent and real DSH images. The root installer uses its bundled OpenSSL to
issue a separate one-year TLS server leaf from the existing installation TLS CA.
It keeps the leaf in `config/managed-source`, projects only its leaf/key to edge,
and projects only the public CA to platform and managed instances. The original
sealed materials and all identity/network signing roots remain unchanged.
Repeated render verifies the original source, key pair, CA, SAN, expiry and file
permissions; it refuses origin changes, widened keys or interrupted preparation.
Rotation/expiry requires an explicit operator procedure; render never replaces
these materials silently.

The listener reuses instance control's private-interface source/destination
restrictions, excludes the LAN interface, checks exact Host and Origin and serves
only native device/network/model paths. Browser cookies, admin APIs, login pages,
instance pages and unapproved origins are denied. Normal authorization remains
in force. Device JWT issuer, network URL and model URL use the internal origin.
Node trusts the public CA through a renderer-owned, digest-bound file and reserved
`NODE_EXTRA_CA_CERTS`; a profile cannot override it. TLS verification stays enabled.

Existing material is versioned: after deployment, use the normal member stop/open
workflow to rotate and deliver it. Confirm the new private file's origin without
printing credentials, network READY, and a real native provider call with relay
usage attributed to the member. Page health or a transport-only probe is not
network/message acceptance.

Public deployment templates for a separate NAS test stack (`auriga-nas-test`).

普通 NAS 共享路径可显式设置 `AURIGA_INSTALL_MODE=container`，以预先审核并加载的不可变
`AURIGA_INSTALLER_IMAGE` 初始化新数据目录，无须宿主 sudo 或更改共享上级权限。
数据根、包、access 和独立 runtime 输入必须先解析为规范路径，数据根与输入互不包含。
helper 只挂一个数据根为 RW，其它输入 RO，无网络或 Docker socket；原 bootstrap 的
UID、祖先、receipt 和硬链接检查在容器内照常执行。宿主源目录由 operator 管理，
此模式不防御拥有宿主存储写权限的人。完整步骤见[操作指南](../../docs/dev/nas.md#普通共享路径的容器初始化)。

- Operator guide: [`docs/dev/nas.md`](../../docs/dev/nas.md)
- Runtime release guide: [`docs/dev/runtime-release.md`](../../docs/dev/runtime-release.md)

内部 bridge 必须设置 `AURIGA_INTERNAL_DYNAMIC_RANGE`，为 `AURIGA_INTERNAL_SUBNET` 内
且不含固定 `AURIGA_INTERNAL_EDGE_IP` 的动态 CIDR；示例将 /26 的后半 /27 留给自动地址。
否则新增服务可能在 edge 重建期间先占用其固定 IP，导致 Docker 返回 Address already in use。
已有栈先保留数据备份并在 `stack.env` 设置范围，再执行本包 `stop.sh`（不加实例清理参数），
最后执行 `start.sh`；只重建固定 Auriga 项目的私有 bridge，数据卷、安装根和外部 LAN 网络保留。

## 已有 NAS 的签名 runtime 升级

保留原 `stack.env` 中的 `AURIGA_RUNTIME_BUNDLE/ROOT/IMAGE` 和预算。这些输入、
`config/materials`、bootstrap receipt、identity 和 collab 源继续按原封存规则检查，不能改写。
先备份数据根及数据库，独立核对新发布包、原始 OCI manifest 摘要和批准的完整镜像 locator，
沿 [runtime 发行指南](../../docs/dev/runtime-release.md) 导入 registry 全部原始层并核验。
更新 agent 镜像仍用 `load-images.sh`，使用其目标 Engine ID 建议更新 `stack.env`；不得填运输 ConfigID。

换入同一精确源码构建、已核 SHA 的 NAS 脚本和 bootstrap 后执行（host 模式须 root，
container 模式沿既有无网络 installer，不需要宿主 sudo）：

```sh
export AURIGA_DATA_ROOT=/share/Container/auriga-test
sh deploy-nas/scripts/runtime-upgrade.sh /absolute/approved/release-bundle \
  registry.example.com/auriga/dsh@sha256:<independently-approved-manifest-digest>
```

命令只信原封存 `runtime-keys/root.pb`，不从候选包接受新根；独立核验签名与 OCI 证据，
严格递增 release sequence。同 sequence 仅接受完全相同的发行声明和 locator 重入。
`config/runtime-source/s-<sequence>` 保存完整公开投影和追加的 `upgrade.receipt`，
绑定原 receipt 摘要、上轮记录及文件摘要。活动指针经同文件系统 rename 与 fsync 原子切换，
只重建 platform/agent，等待两者就绪后提交；失败自动恢复前指针及服务，记录的 sequence
高水位仍保留。原模板预算、公开身份、旧实例所需证明和精确镜像允许列表继续保留。
`render.sh/start.sh`、容器模式 Compose 和挂载探针均读取经校验的活动投影。

中断留下 pending 激活时，重新执行同候选命令即可继续。若需明确撤回尚未提交的激活，
container 模式执行 `installer.sh runtime-rollback /absolute/approved/release-bundle <same-image>`，
host root 执行 `auriga-nas-bootstrap runtime-upgrade -action rollback -base <data>/config/materials
-out <data>/config/runtime-source -bundle <same-bundle> -image <same-image>`，随后 `start.sh`。已提交版本不能用此命令降级。
未知中间文件、源篡改或不安全权限失败封闭，保留现场供检查；不能删除 receipt 或修改活动文件来修复。

更新默认模板不等于已有实例已升级。已有实例须经公开 Workspace 更新规格和 restart API，
核实同实例 generation、observedGeneration、pendingRestart、实际插件版本及 network READY；
身份/collab 未轮换、控制服务健康也不能替代这些实例验收。

## 启用 collab 网络与已有 NAS 升级

启用前须完成真实 IAM native 设备登记并核实租户对应的 realm，然后由离线网络根运营者
按[密钥运维契约](../../contracts/network/key-operations.md)交付根签发布与安装状态。
沿用[网络安装指南](../../docs/dev/network-installation.md)核对来源、独立根指纹、版本下限、
租户和 realm。根私钥不进入 NAS；TLS CA、实例 identity key、runtime root 都不能替代网络根。
安装器不生成网络根、不预注册目标桌面设备、不伪造 READY。

在数据根外准备独立、规范绝对路径的审批目录，例如 `/srv/installation/collab`。
目录归执行安装的 root 管理，私钥文件单硬链接、0600，禁止 symlink。
容器安装模式会将整个目录只读挂载，目录内材料路径用相对路径，入口为 `input.json`：

```json
{
  "tenantId": "00000000-0000-0000-0000-000000000001",
  "realm": "1",
  "rootKid": "offline-root",
  "rootFingerprint": "sha256:<independently-verified-64-hex-fingerprint>",
  "minimumVersion": "7",
  "rootPublicFile": "root.der",
  "signedFile": "signed.json",
  "announcementFile": "announcement.json",
  "stateFile": "state.json",
  "privateFiles": {
    "business-key-id": "business.der",
    "protocol-key-id": "protocol.der",
    "management-key-id": "management.der"
  }
}
```

以上都是占位值，须替换成独立批准的材料。DER 分别为 Ed25519 公钥 SPKI 与业务私钥
PKCS#8；三个 active 用途为 `business_delivery`、`protocol_response`、`management_state`。
`state.json` 必须是同一批准发布的安装状态，禁止手工拼出一个版本号冒充已发布。
清单/公告和状态校验代码由 compose、NAS 共用；服务启动仍由 Java
`RootKeyPublication` / `SigningKeyLoader` 完整验证根签、schema、有效期与 PostgreSQL 高水位。
激活收据封存不代表上述服务校验通过，也不代表设备 READY。

在 `stack.env` 设置 `AURIGA_COLLAB_INPUT_DIR=/srv/installation/collab`。
直接 HTTPS 或 `trusted_proxy` access 基线默认使用其门户来源。HTTP/IP 基线必须另外设置
`AURIGA_COLLAB_PUBLIC_ORIGIN=https://portal.example.com`，并将**同一个**来源加入既有
`AURIGA_LINKS_ALLOWED_ORIGINS`；可信呈现反代地址仍由 `AURIGA_LINKS_TRUSTED_PROXIES`
限定。外部反代须已按 NAS 允许来源机制处理 Host/Proto、HTTPS 证书与 WebSocket 升级。
新变量只选择已批准的 HTTPS/WSS 呈现来源，不修改 access 基线、CORS 或客户端校验。
只有 HTTP、没有可验证 WSS 入口时不能启用本网络；普通 HTTP/IP 部署其它功能保持可用。

全新数据根：按原流程执行 `init.sh`，它会封存原安装材料后生成独立 collab 激活材料；
然后执行 `start.sh`。已有 NAS：备份原安装根、数据库、审批材料及 uid/gid/mode，换入
本次精确提交构建并核 SHA 的公开 NAS 包与镜像，保持原 access 和 runtime 输入，再执行：

```sh
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh deploy-nas/scripts/activate-collab.sh
sh deploy-nas/scripts/start.sh
sh deploy-nas/scripts/status.sh
```

`activate-collab.sh` 仅停止固定 NAS project 的 platform/edge/network/collab，保留数据卷与
已有实例；容器安装模式使用原有离线 helper，无宿主 Go 编译需求。
原 `config/materials` 和 bootstrap receipt 不重写。独立 `config/collab-source` 收据绑定原
收据、输入材料与呈现来源；`.pending` 残留、元数据变化、材料篡改都失败关闭，不自动修复。
`render.sh` / `start.sh` 重复执行只校验和补齐缺少的运行副本，不轮换密钥或覆盖漂移文件。
已激活后清空输入变量不会禁用 collab；已有数据库回退/密钥续签需要专门的运维批次，不能删收据重做。

Compose 自动选入 `collab-compose.yml`。独立 collab 使用同一 platform 镜像、uid10002，
容器安装模式由离线 helper 校验私有激活收据，并在既有受保护 env 导出流中附带启用标记；
普通宿主用户不需要遍历 root 私有收据目录，缺失或无效标记拒绝执行 Compose。
只在内部网络提供 HTTP/mTLS 8085 与 gRPC/mTLS 9092；数据库由一次性 `collab-db`
创建独立 `auriga_collab_owner` / `auriga_collab_app`、schema 和默认 DML 权限，口令每套安装随机生成。
JWKS 固定走 `https://platform:8081/oauth2/jwks`，进程内客户端证书/信任库保持 mTLS；
公共 issuer 及已批准别名沿 NAS 允许来源配置验证。edge、网关、platform、collab 分别仅挂所需副本。

现场需要：新的 `linux/amd64` bootstrap、包含上述脚本与 overlay 的完整 NAS 包、带 curl/openssl/keytool
及现行 collab/managed-network 后端的 platform 镜像，以及原已审核的 edge、agent、PostgreSQL、
NATS、registry、LiteLLM 镜像与签名 DSH runtime 包。edge/agent 没有本次二进制改动，仍须与批准
基线相容。目标桌面用同一现行 Auriga 插件包，按网络安装指南导出可信 loader 配置；
云端新的 managed-network 文件随托管凭据下发，已有实例由原 reconcile 机制更新，必要时经公开入口重开。

验收分别记录：`/api/collab/v1/network/keys` 经实际门户返回 200 且根签正确；云端插件严格
READY；桌面正常登录、入网并严格 READY；最后云↔桌各请求和回复均有真实收件证据。
health、keys 200 或托管实例 READY 均不能代替插件 READY/互发。
`deploy/nas/test/collab.sh` 覆盖 domain/ip/proxy 的实际 init/render、权限与篡改负例；
可选 `AURIGA_NAS_COLLAB_LIVE_IMAGE` 启用 `collab-live.sh`，仅启动单 collab 服务和临时 PG
验证真实签名发布，不宣称已验证 IAM 入网、插件 READY 或整栈云↔桌。

开发机回归（使用锁定 Go 工具链；脚本生命周期测试须在 Linux root 下运行）：

```sh
go -C deploy/bootstrap test -count=1 ./...
go -C deploy/compose/bootstrap test -count=1 ./...
go -C deploy/nas/bootstrap test -count=1 ./...
sh deploy/nas/test/run-tests.sh
# 可选单服务 Docker 验证；应用镜像须为已核对、本地存在的不可变 ID：
AURIGA_NAS_COLLAB_LIVE_IMAGE=sha256:<approved-local-platform-id> \
  go -C deploy/nas/bootstrap test -count=1 -run TestNASCollabScriptLifecycle/ip -v
```

`preflight.sh`在初始化完成后还会执行只读的`mount-access.sh`：以各服务实际UID/GID和必要补充组，检查实际挂载目录遍历、公开材料读取及必要秘密文件打开权限。它复用已pin且本地存在的platform镜像提供`sh/cat`，所有源路径只读、无网络、无额外capability、无Docker socket，不拉镜像、不修改材料或ACL。可单独运行`AURIGA_DATA_ROOT=/srv/container/auriga-test sh deploy-nas/scripts/mount-access.sh`。0755不代表继承ACL下有效可读；出现`service`/`uid:gid`/`path-category`错误时，按[公开排障指南](../../docs/dev/nas.md#挂载访问与继承-acl-排障)核查自己专用目录的有效权限。节点在线但模板无法调度时，另按[节点label指南](../../docs/dev/nas.md#节点-label-与模板匹配)经正式管理台/API核对`template=real-dsh`，禁止直接改库绕过。

PostgreSQL仅对只读探针可确认完全为空的PGDATA放行官方entrypoint准备阶段，支持`umask 077`初始化产生的`root:root/0700`空目录。非空（含隐藏项、悬空链接）或无法确认空时，仍按999:999检查数据目录；SQL始终按999:999检查。探针不执行chown/chmod。

LiteLLM 原秘密保留在 `config/materials/litellm/`，归安装器 UID10001、文件0600；render 生成 `config/litellm/` 运行时副本（文件0640，master/salt组10003、数据库口令组999、团队Key组10001）。compose secrets 仅挂副本，LiteLLM 与 init 无额外 capability。备份原件、回执和数据库并保留 UID/GID/模式；恢复后由 render 校验或重建副本，已有内容或元数据漂移会拒绝。详见[中转站部署与备份](../../docs/dev/relay-litellm.md)。

- Do not put real domains, LAN IPs, or credentials in this tree
- Build images via `.github/workflows/nas-bundle.yml` (`workflow_dispatch` + exact commit)
- Never deploy from a worker session; main controller runs site instantiation from private notes

## 下载工件后的准备

GitHub Actions 工件不会保留文件执行位（见 [Permission Loss](https://github.com/actions/upload-artifact#permission-loss)）。
在解包后的工件根目录，先确认 `images/manifest.json` 的 `commit`、`source_tree` 对应独立核对的精确源码提交，
并核对 `bootstrap`、`runtime_release` 的包内相对路径及 SHA-256（后者目标为 `linux/amd64`）。
再验证工件根 `SHA256SUMS` 中的两个二进制、manifest、versions 摘要，以及 `images/SHA256SUMS` 中的镜像归档摘要，恢复两个工具的执行位：

```sh
sha256sum -c SHA256SUMS
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap deploy-nas/bin/runtime-release
sh deploy-nas/scripts/load-images.sh "$PWD"
```

部分 NAS 共享目录给新解包文件继承命名 ACL；即使显示 `0755`，仍可能显式拒绝固定
installer UID10001，表现为降权执行或挂载探针 `permission denied`。先用 `getfacl` 核对
已验 SHA 的**新发布包公开配方**。若存在这种继承拒绝，在新包根仅对 `deploy-nas` 的
公开目录和文件去除继承命名 ACL，再恢复两个工具的 `0755`（脚本等文件保持包内模式）：

```sh
find deploy-nas -type d -exec setfacl -b -k {} \;
find deploy-nas -type f -exec setfacl -b {} \;
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap deploy-nas/bin/runtime-release
```

不更改共享目录/NAS 系统 ACL，不对安装数据根、封存材料、外部审批根或私钥使用此步骤。
升级器只对本次新建的公开投影目录去除继承 ACL，不修复现有目录或改变原封存源。

`load-images.sh` 是公开包的应用镜像加载入口。它先用已核对的静态 bootstrap 验证根和镜像校验和、
固定三份 tar 的 tag、原 config 摘要 `config_id`、唯一 `linux/amd64` 与实际层字节的有序 diffID 链，
再只加载 `platform.tar`、`edge.tar`、`agent.tar`。加载后从目标 Engine 结构化 inspect 得到精确本地 Id，
只按该不可变 Id 导出并再次核对原 config 字节及层链；不会再次用可变 tag 选择导出对象。
三项全部成功后才复制原 `images/manifest.json` 为 release marker，输出三条 `AURIGA_IMAGE_*` 建议。
将这些建议手工填入 `stack.env`；脚本不会改写 `stack.env`／`STACK_ENV`，不会创建或认领数据根。
默认 marker 位于包根；`AURIGA_RELEASE_DIR` 可指向已存在的独立目录，同时接收原 `versions.txt`。
失败会保留已加载镜像和原有 tag，不自动删除、修复或回退。

另外，operator 须从可信发布者独立批准并固定 PostgreSQL、NATS、registry 三个基础镜像的精确
digest，逐个执行 `docker pull --platform linux/amd64 'name@sha256:<已批准摘要>'`，再用
`docker image inspect --format '{{json .RepoDigests}} {{.Os}}/{{.Architecture}} {{.Id}}' 'name@sha256:<已批准摘要>'`
核对摘要与 `linux/amd64` 平台，并把完整引用写入 `stack.env`。
完整三镜像在线示例见 [操作指南](../../docs/dev/nas.md#初始化启动停止)。
全零占位不允许启动；预检只读核验七项镜像（包括 digest 引用）都在本机，启动只执行一次
`up -d --no-build --pull never`，失败保留非零退出码，不隐式拉取或重试放宽策略。

离线运输的 `docker save` / `docker load` 不保证 RepoDigests 保留。加载后应独立核对运输关联、
平台和目标 Engine 本地不可变身份，将已核对的本地 `sha256:...` Id 写入配置。
不能只沿用源机 Id，也不能将 config ID 当成 containerd store 的通用 Id。

`images.*.id` 保留构建时 CI Engine 的原身份；不同 Docker store 的实际本地 Id 可能是 config、manifest
或 index 摘要，不能直接把 CI 值或 `config_id` 当成目标 Engine 值。新增 `config_id`、`os`、`arch` 来自真实
docker-save 归档；这些是运输关联元数据，不是正式 runtime 签名、官方可信根或独立发布授权。
缺少新元数据或任一校验和的旧包会明确失败，应重新获取新 workflow 生成的完整包。

bootstrap 的 `image-metadata IMAGES_DIR`、`image-check BUNDLE_DIR`、`image-resolve BUNDLE_DIR` 三个入口均只读，
在安装参数校验之前分派，不需要 access／PKI／identity／数据根，也不需要 NAS 安装 Python 或 Go。
元数据入口供 CI 使用；check 无成功输出，resolve 仅在三镜像核验完后输出本地 Id 建议，错误走 stderr。
解析不解包到文件系统，拒绝重复 JSON／tar 条目、链接、不可信路径、截断和多可运行候选；允许有序重复层，
OCI attestation 必须绑定唯一的可运行 manifest。每份 tar 原字节上限 8 GiB、展开字节总量上限 16 GiB、
条目上限 32768，单 JSON 上限 2 MiB、保留元数据总量上限 32 MiB；单次命令总时限 10 分钟，
inspect 各 30 秒、每次不可变 Id 导出各 3 分钟，失败取消并回收 Docker 子进程。

两个工具均由 workflow 从同一精确 checkout 使用 Go 1.27.3、`CGO_ENABLED=0` 预构建为 Linux/amd64 二进制。
源代码 checkout 的对应路径为 `deploy/nas/bin/auriga-nas-bootstrap` 和 `deploy/nas/bin/runtime-release`。
操作脚本使用 `sh deploy-nas/scripts/<操作>.sh`，脚本不要求执行位。bootstrap 准备好后再按操作指南配置数据根并初始化。
在 NAS 上可直接使用 `deploy-nas/bin/runtime-release`，按 runtime release 指南运行 `verify`、`stage-layout` 等命令，无须现场安装 Go 或编译。

本工件是准备包，**不是完整离线安装包**：它不提供官方可信根、已签名的真实 DSH 发行材料、DSH 全部镜像层或第三方基础镜像。
工具不会自动生成或信任发布根；operator 必须从可信渠道独立审核并固定包外公开根、正式材料和精确允许镜像。
仅本机匿名 HTTP import profile 的运输流程、经 operator 独立审核的 ORAS 工具及其使用路径仍需另行准备，
遵照 runtime release 指南按原始 manifest 摘要运输与核验，并按对应精确版本的显式命令执行导入；
工具不自动启动 registry、不管理 registry 认证，也不自动执行现场部署。
完成摘要检查、材料验证或基础栈初始化不代表已能登录、真实实例已就绪或现场验收通过。

模型接入、独立 UI Origin、成员预算和外部 Key 操作见 [LiteLLM 部署指南](../../docs/dev/relay-litellm.md)。

## 可选集中日志

NAS 也支持 `observability` profile，使用与 Compose 开发栈相同版本的
Loki 3.5.5、Alloy 1.10.2。在可信渠道独立批准这两个版本的 linux/amd64 摘要，
显式拉取后核对 `RepoDigests`、平台和本地 Id；在数据根的 `stack.env` 添加：

```dotenv
COMPOSE_PROFILES=observability
AURIGA_IMAGE_LOKI=grafana/loki:3.5.5@sha256:<已批准摘要>
AURIGA_IMAGE_ALLOY=grafana/alloy:v1.10.2@sha256:<已批准摘要>
```

然后重新执行 `scripts/start.sh`。预检增加两项精确镜像检查；仍不自动拉取、
构建或改变部署根。关闭 profile 不会删除日志卷，也不会自动停止已经运行的采集器；
如需停采集，用同一 project 的 Compose 显式停止 `alloy loki`，保留业务服务。
新增服务仅接内部日志网络，不发布端口；日志保存七天，资源上限约 448 MiB。
Alloy 仅筛选固定 `auriga-nas-test` project 的容器。一次性的 `alloy-prepare` 无网络、
只持有 CHOWN，固定其状态卷顶层为 UID/GID 0；运行时 Alloy 以 UID 0 访问 Docker socket，
仍丢弃全部 capability，不能依赖 DAC_OVERRIDE 写入上游镜像拥有的状态目录。
公开日志配置须能由 Loki 的 UID10001 和 Alloy 的 UID0 读取；通过 SMB 单文件更新时，
需恢复这些公开文件的发布权限与所有权，并重新创建消费容器，不能只检查宿主的 mode 位。
不要将此权限操作应用于签名密钥、凭据或其他部署材料。
Docker socket 即使只读挂载仍有
宿主 root 等效权限；只在受控测试 NAS 启用，审阅日志后才分享。
此 profile 收集服务容器日志，独立桌面插件日志仍需另行按原日志指南收集。

不必对公网暴露无鉴权 Loki。在已授权的 NAS 操作通道运行
`scripts/observability-query.sh`，stdin 传一个换行结尾的
`http://127.0.0.1:3100/loki/api/v1/query_range?...` URL；stdout 返回原始 Loki JSON。
脚本只接受这个固定回环查询端点，通过 `compose exec -T loki` 查询，拒绝其他地址或多行输入。
它使用相同 `AURIGA_DATA_ROOT`、安装模式和项目，不读取业务凭据。
统一验收的 `observability.queryCommand` 可配置受信任的 argv 数组（例如既有 SSH
操作通道调用此脚本），`lokiUrl` 固定为 `http://127.0.0.1:3100`。
命令通过 stdin 接收 URL，stdout 只能输出 Loki JSON；操作通道的提示信息写 stderr。
`streamSelector` 使用 `{job="auriga-nas"}`，`expectedServices` 填真实结构化日志的
服务字段（如 `edge`、`iam`）。验收仍要求同一失败 trace 覆盖所有指定服务，不能用健康状态代替。

锁定镜像的真实状态卷回归可在本机 Unix Docker 上运行：
`AURIGA_IMAGE_ALLOY=sha256:<已核对本地Id> sh deploy/nas/test/observability-volume.sh`。
它只创建带唯一标签的临时卷，先复现 capless UID0 对镜像所属目录的写拒绝，再运行正式
prepare 命令并验证可持久写入；结束时仅清理标签匹配的测试卷，不接业务网络或 Docker socket。
