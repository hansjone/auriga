# NAS 公开预构建安装配方

本包在一个隔离的 Docker 项目 `auriga-nas-test` 中安装 combined、edge、agent、PostgreSQL、NATS 和本机 registry。NAS 不编译源码；用 `.github/workflows/nas-bundle.yml` 对完整40位提交生成包。工人只在本机验证，不访问 NAS、VPS、DNS 或旧 Hub。公开例子一律用 `example.com`、`192.0.2.x`、`/srv/container/auriga-test`，真实值只写私有现场笔记。

## Network submit 私有入口

新准备包复用既有 edge 镜像制品承载 ai-gateway、network Broker 和就绪探针，不新增现场拉取或第七种离线镜像。Compose 以独立 `network-broker`、`network` 服务运行，固定 `10001:10001`、只读根文件系统、无特权，仅加入 internal 网络，不发布 submit/Broker/WSS 宿主端口。两服务均由现有 start/status/stop 管理。

bootstrap 在 `config/materials/network`（`10001:10001/0700`，文件 `0600`）生成 receipt 管理的专用材料、Broker 配置和 `runtime.json`。submit 服务端 CA、客户端 CA 和 IAM CA 相互独立，CA 私钥不保存。只有 edge 叶证书由 submit 客户端 CA 签发；`submitHTTP.clientCAFile` 必填，缺失或错误材料拒绝启动。Broker 使用受限 auth/ops/data 身份和另一组叶证书。

edge 配置 `upstreams.network-submit=https://network:18443`，固定服务端名 `submit.network.internal`。init/render 沿既有 `install_edge_material` 机制，只复制 `network-edge.pem`、`network-edge.key`、`network-server-ca.pem` 到 pki-edge，设置 edge 所有权并检查重入一致性；不会向 edge 挂载网络服务端私钥、NKeys 或 Broker 密码。platform 读取私有票据导出配置，独立 `network-tokens/gateway` 保存固定 ai-gateway 票据，network 只读挂载；IAM RPC 证书仍与 submit 信任材料分开。

健康探针在真实 mTLS 连接上发起无设备凭据 POST，收到 `401 network.unauthorized` 才标就绪；edge 等待该健康条件。该检查不代表完整 collab 网络业务已启用，也不代表 READY、SEND 或持久化成功。完整业务装配和协议公钥发布需另外验收。旧 receipt 缺新材料会拒绝重入，须准备新的隔离安装根并保留旧数据；不手补文件、不放宽 TLS、不修复旧 owner 或轮换已有凭据。

## 一份 access 配置

选择包中的 `access-domain.example.json`、`access-ip.example.json` 或 `access-proxy.example.json`，复制到数据根以外的绝对路径（例如 `/srv/container/auriga-access.json`），编辑后将它写入 `stack.env` 的 `AURIGA_ACCESS_FILE`。bootstrap 对原始字节调用生产 `deploymentconfig.Load`，先按完整原 schema 和启动语义校验，再验证 NAS 装配；不会用 Compose 的开发租户、固定 M2 服务端口、模板或 seed 验证器代替生产加载。

- direct HTTP：支持 DNS 域名和规范纯 IPv4，必须明确确认 HTTP；IP 门户与管理使用不同端口，不需要域名、hosts、伪 Host 或额外 IP。domain 实例继续使用独立子域；IP 实例使用 `/i/<instanceId>/`，只面向互信团队，不宣称具有 domain 的同等隔离。
- trusted_proxy HTTPS：外部浏览器访问 HTTPS；前置代理到 edge 的 portal/admin 两个 socket 仍必须 TLS+mTLS，保留完整 authority。独立的代理 CA、`proxy-client` 证书与唯一 URI SAN `auriga://proxy/nas-test` 绑定代理身份，access 的规范 `peerCidrs` 再绑定真实 TCP 来源。转发头不能选择 scheme、tenant、audience。旧版「TLS在VPS终止后明文8080+X-Forwarded-*」配方应迁移，不能继续套用。
- 三个 listener 必须在容器中分别绑定 `0.0.0.0`；内控 listener 单独使用 HTTP，内部 alias 必须与所有 browser Origin 分开。它不发布宿主端口，只接受正式 self/heartbeat。direct 公网 TLS、split 部署及其它 schema 能力需要另行装配，本配方会明确拒绝不支持的组合。

同一个最终 `config/materials/access.json` 只读挂载给 edge、IAM、agent。issuer、team Origin、RP host 从它派生，包含非默认端口；agent 不再使用旧 `INSTANCE_PLATFORM_URL`/`INSTANCE_PARENT_DOMAIN`。旧 `AURIGA_PORTAL_HOST`、`ADMIN_HOST`、`INSTANCE_PARENT_DOMAIN`、`ISSUER`、`TEAM_ORIGIN`、`TEAM_RP_ID`、`TLS_UPSTREAM`、`SECURE_COOKIES`、`TRUSTED_PROXIES` 输入会明确报迁移错误，不静默降级。access 的 tenant 必须由安装者与实际 IAM/org 租户对应；加载成功不代表租户已创建。combined 的既有首次 seed 创建演示租户 `00000000-0000-0000-0000-000000000001`，无 dev profile 或远程紧急登录例外。

## 按访问来源生成对外链接

IP access 模式叠加外部呈现代理时，可在 `stack.env` 配置：

```dotenv
AURIGA_LINKS_ALLOWED_ORIGINS=http://192.0.2.10:8080,https://portal.example.com
AURIGA_LINKS_TRUSTED_PROXIES=192.0.2.20/32
```

允许列表只接受完整、规范的 HTTP(S) origin（不含尾斜杠、路径、凭据、query 或 fragment）。
edge 按实际 TCP 对端判断是否属于独立的链接代理信任列表；可信对端才可用单值
`X-Forwarded-Host` 与 `X-Forwarded-Proto` 声明浏览器呈现来源。其他对端的这些头一律忽略，
从原始 Host 和实际连接 scheme 选来源。候选必须精确命中允许列表，否则使用原默认来源。
代理须覆盖客户端提供的这两个头，不透传不可信链；只登记实际代理 IP 或最小 CIDR，禁止 `/0`。

edge 清除所有外来转发/内部身份头后，仅向平台 API 写入经校验的呈现头。NAS Compose 把
platform 的链接代理信任范围固定为内部 edge IP；拆分部署须在后端配置同一允许列表和
`auriga.links.trusted-proxies`（直接连接后端的 edge 地址），保留后端 mTLS 与网络隔离。
后端必须使用 `server.forward-headers-strategy=none`，不能先让容器按外来头改写真实对端；
链接信任配置与容器自动转发头处理同时启用时，启动会拒绝。
邀请、连接器回调、OIDC 端点地址及设备验证链接跟随该请求来源；登录事务保存开始时的
呈现地址，后续回调仍校验原 entry 绑定。相对回跳保持相对地址，自然跟随当前浏览器来源。
重置操作目前没有生成绝对重置链接的公开接口，不新增一条私有流程。

这个配置只控制链接呈现，不参与 tenant、audience、issuer、客户端 IP、Cookie 或 CSRF 判断；
原 access 文件和来源门禁仍有效。它不替代 `trusted_proxy` 的 TLS+mTLS 配方。空列表保持原行为。
经公网代理访问应产出公网链接，直接从 IP 访问应产出 IP 链接；从独立内网管理入口创建邀请时，
其管理 Origin 不属于门户允许列表，邀请回落到默认门户 IP，不能把它强制改为域名链接。

开发 Compose 也透传同名允许列表和 edge 链接代理列表，后端默认只认可 host 网络上
edge 的回环对端；换用其他网络时显式设置 `AURIGA_LINKS_BACKEND_PROXY_PEERS`。
NAS 的后端对端始终由已有 `AURIGA_INTERNAL_EDGE_IP` 派生，不能填成前置代理 IP。

## 初始化、启动、停止

### 普通共享路径的容器初始化

宿主默认使用 `AURIGA_INSTALL_MODE=host`，原 root 和祖先权限要求继续适用。
共享目录部署可以明确选择 `container` 模式：operator 必须管理宿主 bind 源路径，
先用 `readlink -f` 解析共享路径别名，将规范绝对路径写入配置。脚本拒绝任何叶软链、
非规范输入，以及 package/access/bootstrap/runtime 输入与 RW 数据根互相包含。
此模式不宣称防御拥有宿主存储写权限的人；容器中的 root755 祖先不代表宿主共享目录可信。

布局例子：共享父目录下并列放 `package/`、`release/`、`operator-approved/root.pb`、
`access.json`、`auriga-data/`，把数据根作为唯一 RW bind。包内 bootstrap 必须已核对摘要且
恢复 0755；access、公开 runtime 和包外独立根保留稳定 locator，并供固定 uid10001 读取。
不得挂载整个共享目录、宿主 `/` 或 Docker socket 给 helper。

```sh
# SHARED_PARENT 由 operator 先创建；可包含宿主软链别名。
PARENT=$(readlink -f "$SHARED_PARENT") || exit 1
mkdir "$PARENT/auriga-data" # 新空目录；也允许只有一份普通 stack.env
cp "$PARENT/package/env.example" "$PARENT/auriga-data/stack.env"
chmod 0600 "$PARENT/auriga-data/stack.env"
# 编辑 stack.env，所有路径填规范绝对路径：
# AURIGA_DATA_ROOT=<PARENT>/auriga-data
# AURIGA_ACCESS_FILE=<PARENT>/access.json
# AURIGA_INSTALL_MODE=container
# AURIGA_INSTALLER_IMAGE=postgres:17@sha256:<operator已批准并预先加载的摘要>
# 可复用已准备的 PostgreSQL 17 Debian 镜像，须含 /bin/sh、openssl、tar 和 GNU/POSIX 工具。
export AURIGA_DATA_ROOT="$PARENT/auriga-data"
sh "$PARENT/package/scripts/init.sh"
sh "$PARENT/package/scripts/preflight.sh"
sh "$PARENT/package/scripts/network-preflight.sh"
sh "$PARENT/package/scripts/start.sh"
```

installer 镜像必须已在本机且为 linux/amd64，不能用可变 tag；不会自动 pull 或自动降级到
测试用 `NAS_ALLOW_NONROOT`。helper 使用 user0、network none、cap-drop ALL，仅添加
CHOWN/DAC_OVERRIDE/FOWNER/SETUID/SETGID，启用 no-new-privileges 和只读根文件系统。
一次性容器和其匿名卷结束后回收，不删除数据或镜像。只在首装空根/仅 stack.env 的根设置
root0755；已有材料、owner、权限、receipt、硬链接错误明确拒绝，不修复未知旧目录。

`init.sh`、`render.sh` 自动按显式模式委派；`installer.sh check` 和 `prepare-socket` 可独立运行。
预检仍在宿主检查 Engine、端口、资源和镜像，严格祖先/材料检查交给 helper。
Compose 固定项目与主文件，仅从 helper 经私有临时流导出 platform/edge/agent 三份 env，
写入宿主 mktemp0700 目录和0600叶，再用固定生成的短期 `env_file: !override` 覆盖引用。
两处宿主临时目录均放在数据根的直接父目录，该父目录必须为规范真实目录、属于当前
operator 或 root 且无 group/other 写位；不依赖可能为非 sticky0777 的宿主 `/tmp`，不改变共享祖先权限。
原服务私有文件权限不变；临时副本在成功/失败/信号退出时清理，退出码保留。需要 Compose
2.29+；postgres secret 继续使用主文件里的原 bind 源，不额外导出。拥有 Docker socket 的
operator 本身具有宿主 root 等价能力。不要运行会输出完整秘密配置的 `docker compose config`；
公开入口使用 `config --quiet`，日志和报告不包含秘密。

先显式准备第三方基础镜像。operator 从可信发布者取得并独立批准 PostgreSQL、NATS、registry
各自的精确 digest（确认支持 `linux/amd64`），将三个完整引用固定到 `stack.env` 的
`AURIGA_IMAGE_POSTGRES`、`AURIGA_IMAGE_NATS`、`AURIGA_IMAGE_REGISTRY`。包内全零值只是占位，
预检会拒绝；不能根据可变 tag 自动选择或批准新 digest。在线准备示例（先替换三个占位摘要）：

```sh
POSTGRES_REF='postgres:17@sha256:<已批准的64位hex摘要>'
NATS_REF='nats:2.10-alpine@sha256:<已批准的64位hex摘要>'
REGISTRY_REF='registry:2@sha256:<已批准的64位hex摘要>'
for image in "$POSTGRES_REF" "$NATS_REF" "$REGISTRY_REF"; do
  docker pull --platform linux/amd64 "$image" || exit 1
  docker image inspect --format '{{json .RepoDigests}} {{.Os}}/{{.Architecture}} {{.Id}}' "$image" || exit 1
done
```

逐项核对 inspect 的 RepoDigests 对应批准引用、平台为 `linux/amd64`，并记录目标 Engine 的本地
不可变 Id。确认后将上述完整引用写入配置；若必须离线运输，`docker save` / `docker load` **不保证
保留 RepoDigests**，不能只因归档加载成功就沿用原 digest 引用。应在加载后独立核对镜像运输关联、
平台及实际本地不可变身份，再将已核对的目标 Engine `sha256:...` Id 写入对应配置项。
`config_id` 是 config 字节摘要，不能当成 containerd store 的通用 `Id`。

`preflight.sh` 只读检查七项明确配置的本地镜像，包括 digest 引用；任何缺失、全零占位、错平台或
非法 inspect 输出都会失败，不执行 pull。`start.sh` 只调用一次 `up -d --no-build --pull never`，
Compose 失败时保留非零退出码；应修正准备步骤或 Compose 版本后重试。

工件下载会丢执行位。先核对提交、镜像 manifest 和校验和，再恢复二进制执行位（不是在 NAS 构建）：

```sh
sha256sum -c SHA256SUMS
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap deploy-nas/bin/runtime-release
# 在解包目录载入三组件；只采用 loader 核验完成后输出的目标 Engine 本地 ID。
sh deploy-nas/scripts/load-images.sh "$PWD"
mkdir -p /srv/container/auriga-test
cp deploy-nas/env.example /srv/container/auriga-test/stack.env
cp deploy-nas/access-ip.example.json /srv/container/auriga-access.json
# 将 loader 输出的三条 AURIGA_IMAGE_* 和上面已准备的三个基础镜像引用填入 stack.env；另按 LiteLLM 指南预载固定镜像。
# 再编辑上述两份文件，七项镜像都必须已经在本机且为 linux/amd64。
# 若正式 runtime image 的已批准 locator 使用 127.0.0.1:50123，
# 签发/审批前就把 AURIGA_HOST_PUBLISH_REGISTRY 设为 50123（示例默认15000）。
# 数据根预置内容只能有 stack.env。
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh deploy-nas/scripts/init.sh
sh deploy-nas/scripts/preflight.sh
sh deploy-nas/scripts/network-preflight.sh
sh deploy-nas/scripts/start.sh
sh deploy-nas/scripts/smoke.sh
sh deploy-nas/scripts/stop.sh
```

面板由无网络常驻 `panel-renew` 侧车每小时检查，不依赖宿主 cron 或 sudo。
失败/心跳陈旧仅通过侧车状态与日志暴露，edge 监听健康不依赖面板；缺少可用清单仅面板路径返回503。
`stop.sh` 同时停止侧车；卸载或移动部署材料前先运行 `stop.sh`。
详细的日志、健康信号和手动恢复见 [面板部署与续签](panel-deployment.md)。

所有操作脚本都显式用 `sh` 调用，不依赖执行位。源码 checkout 对应 `deploy/nas/`。数据根须绝对路径、至少三层且 basename 为 `auriga-*`；拒绝链接、点段及未初始化的外来非空目录。首装/root初始化只为新建且空的材料根通过已打开 FD 设置固定 `10001:10001`，再让清空补充组的同一预构建 binary 子进程生成材料。重入按原身份校验 receipt、输入和完整材料，原 key 不轮换；错 owner/mode、额外链接、错误祖先、缺 receipt、template-only、PKI/模板目录链接都拒绝，不自动 chmod/chown 旧材料。

应用镜像须走包内 `load-images.sh`：加载前只读验证完整校验和、新 `config_id` 元数据和固定三份真实 tar，
加载后对固定 tag 的实际本地不可变 Id 执行导出，逐项比原 config 字节摘要、唯一 `linux/amd64` 与有序层 diffID。
仅三镜像全部成功后才写原制品 manifest 的 release marker 并输出目标 Engine 的三条镜像建议。
CI 的 `images.*.id` 保持原制品身份，不能直接用于另一种 store；`config_id` 也不是通用 Engine Id。
loader 不改 env、不创建数据根或安装身份、不依赖现场 Python／Go，失败不删除已加载镜像和旧 tag。
缺新元数据／校验和应重新获取完整新包，不能退回只信 tag 的加载方式。运输身份核验不替代正式 runtime
签名授权；归档和子进程的具体预算、只读入口见 [NAS README](../../deploy/nas/README.md)。

Linux root只从固定 `/proc/self/exe` 打开当前0755普通可执行文件，将该只读FD单独传为子进程fd3并通过 `/proc/self/fd/3` 以10001身份执行，因此bundle可在0700私有目录；材料根的祖先信任与实际可遍历要求仍独立强制，不复制binary或修改bundle目录权限。

`AURIGA_NAS_BOOTSTRAP_BINARY` 可显式指向已校验的预构建 Linux binary（用于独立包布局/CI），不会触发 Go 编译。Windows 可做 `-validate-access`，不支持 NAS 私密材料激活。`NAS_ALLOW_NONROOT` 是旧脚本测试标志，不能绕过固定 uid 的真实材料安装。

operator的 `stack.env` 与派生 `materials/access.env` 分层处理：派生文件整份通过固定9个入口字段的格式检查后才导入，未知、重复、缺项或非法值整份拒绝，不能覆盖binary／数据根／project等控制配置；该格式检查不代表receipt已验证，后续bootstrap的receipt与材料元数据校验仍独立强制。

第一次 `AURIGA_PLATFORM_SEED=true` 仅使用既有 `combined,e2e` seed；完成后设回 false，重新 render 后为 `combined`。不要给 NAS 配方追加 dev、远程 emergency 或 observer 例外。

## 首次正常管理员（真实回环）

IP 部署首次启动会生成私有 emergency enrollment；应急登录保持仅回环，不能拿它从
另一台电脑直接登录管理台。公开 `scripts/admin-bootstrap.sh` 在已运行 edge 的网络空间
启动一个临时、无端口发布的 helper，通过真实 `127.0.0.1` 完成应急登录、普通团队账号
注册/TOTP 登记和独立正常管理员登录。Host/Origin 使用已验证 access 的规范入口，仅用于
listener/虚拟主机选择，不发送 Forwarded、X-Forwarded-For 或任何伪造来源头，不改数据库。
当前脚本明确只接受 IP 模式；HTTPS 不跳过证书校验，可读取该部署的公开浏览器证书链。

helper 复用已独立批准并加载的精确 LiteLLM 镜像中的 Python 3/cryptography，以10001身份、
cap-drop ALL、只读根运行。仅只读挂脚本、access、enrollment、收件公钥及可选公开证书，
不挂 Docker socket、私钥、模型 Key 或整个数据根。普通 NAS operator 无须 sudo。

在**操作者本机**准备仅本人可读的私有目录（Windows 须受限ACL），生成一次性 RSA3072
或更强收件密钥；只有公钥可传到 NAS 的独立规范路径：

```sh
umask 077
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out /private/bootstrap-recipient.key
openssl pkey -in /private/bootstrap-recipient.key -pubout -out /private/bootstrap-recipient.pub
# 只传 .pub，允许安装器UID10001读取它；私钥绝不上传。
# NAS 上，使用已初始化的数据根和包：
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh /srv/packages/deploy-nas/scripts/admin-bootstrap.sh /srv/approved/bootstrap-recipient.pub
```

stdout 仅含 `AURIGA_BOOTSTRAP_ENVELOPE` 加密检查点，可由命令通道在本机保存。
管理员、测试成员及原应急恢复凭据均使用 RSA-OAEP-SHA256 + AES256-GCM 封装，
明文只在 helper 内存及服务端既有账号存储中存在，不写 NAS 文件、命令参数或日志。
先封装登记的 TOTP，再提交确认；异常时保留最后密文供恢复，**不要盲目重新创建账号**。
脚本用内置 owner 角色建立第一个正常超管，再建立 member 测试成员，各用空 cookie jar
重新登录验证；正常管理员通过角色和节点权限检查后，使用既有API登出应急会话再发布完整交接，
最后清理本次其他会话。应急账号仍保持原来的私有、仅回环边界；保留原 enrollment，
妥善保管密文、收件私钥和解密后的恢复材料。脚本不依赖契约中尚未实现的应急轮换接口。

在本机建立隔离 Python 环境并安装固定依赖，解密到新的私有文件（不会覆盖）：

```sh
python -m venv /private/admin-python
/private/admin-python/bin/pip install -r deploy/nas/test/admin-bootstrap-requirements.txt
/private/admin-python/bin/python deploy/nas/scripts/admin-bootstrap-decrypt.py \
  --input /private/bootstrap-encrypted.log --private-key /private/bootstrap-recipient.key \
  --output /private/bootstrap-accounts.json
```

Windows 使用该环境的 `Scripts/python.exe`；解密器在写入前移除新文件的继承ACL并只授当前SID。
默认仅接受 `stage=complete`；失败恢复须显式 `--allow-partial`，只代表取回已建立步骤的凭据。
测试凭据不是正式运营账号；用户自行登录后应按现有账号流程更换口令/MFA。不要把该私有文件提交或粘贴到报告。

## 消费独立审核的正式运行时

安装可选接收 [runtime-release](runtime-release.md) 的公开签发目录。operator 先通过可信发布渠道独立审核并固定公开根，
再在 `stack.env` 同时设置下列三项；全空只准备基础栈，缺任何一项均在生成安装材料前拒绝：

```sh
AURIGA_RUNTIME_BUNDLE=/srv/container/releases/release-7
AURIGA_RUNTIME_ROOT=/srv/container/operator-approved/root.pb
AURIGA_RUNTIME_IMAGE=127.0.0.1:50123/auriga/dsh@sha256:<原始manifest的64位小写hex>
```

对应 bootstrap 参数为 `-runtime-bundle`、`-runtime-root`、`-runtime-image`。根必须位于 bundle 外，不能把包内根直接当作外部授权，
不能每次安装新建一个发布根。目录和根须已由 operator 准备为固定 uid10001 可读取、祖先可遍历的公开只读材料；
bootstrap 不修改输入的 owner/mode，不复制发布私钥。即使以 root 调用，预检与装配也通过清空补充组的固定 uid10001 子进程，
直接调用生产 `runtimebundle.Verify`，固定目标 `linux/amd64`，要求与独立批准的完整 image 一致。
验签、根独立性、原始 M/C、目录额外叶和链接检查均由生产导入器执行，失败不生成 PKI/安装身份。

装配只使用成功 `Material` 的复制型快照，生成 `materials/templates/real-dsh.pb`、`runtime-keys/root.pb` 及
`runtime-proofs/<signedMhex>/{manifest.json,config.json}`。模板保留 Docker-only 正式声明、real profile 的端口7860及
`imBots=none`、`terminalSources=browser-only`，secrets 只含**本次安装**的 identity 公钥；没有发布私钥或安装凭据。
资源请求为 CPU500m、PIDs128、disk1GiB，内存直接使用本次 `AURIGA_INSTANCE_MAX_MEMORY_MB`，不隐式提高默认512。
启用时该值至少256MiB，以满足生产 `WorkspaceTemplates` 的形状检查；这不是 DSH 已验证的最低资源要求。
基础栈准备模式仍接受原来的内存输入范围。

上述模板、公开根、原字节 proof、三项原输入与内容摘要和预算一起封入安装 receipt。
同本版本同输入重入逐字节无损复用，不旋转、补叶或修权限；变 image、bundle/root 路径或内容、预算、启用模式，以及坏/缺 receipt、
未知模板或 proof、链接/硬链接和异常 owner/mode 均拒绝。首装中途 I/O 失败保留痕迹，不能删除 receipt 来补全旧信任，
应保留材料诊断并使用全新安装目录。旧目录缺新增固定叶/目录时拒绝，不自动补写或迁移；保留无 runtime 的原 receipt 摘要字节语义不代表完成旧安装兼容迁移。

platform 从 receipt 保护的模板和 artifact-keys 目录只读消费，并在既有 CA SPKI pin + TLS 首次 Join 中交付审核根；
agent 只读挂载同一封存 proof 目录。镜像 policy 仅加入批准镜像的完整 repository 前缀值，并通过额外的
`allowedImageRefs` 精确限制唯一批准的完整 `repo@sha256`；原前缀策略仍是并行必需条件，不自动放行其他仓库。
没有添根 RPC、force 或明文 Join 旁路；激活仍执行生产当前根、持久高水位、防回滚和 Engine 身份检查。

公开 bundle 只有签名声明与原 M/C，**不含镜像层，也不是完整离线安装包**。部署者应按下面顺序准备本地 registry；发布者须在材料签发前独立固定最终 `127.0.0.1:<规范端口>/<仓库>@sha256:<原M>`，NAS 与源构建机可使用相同的数字 loopback authority/repo。不能事后改已签包的 image 或放宽 operator 的精确批准策略。

1. 核对 bundle 和三组件 tar 的 SHA-256，执行上面的 `load-images.sh .`，把输出的 platform/edge/agent 本地 image ID 写入 `stack.env`。在正式材料签发/审批前，确认已固定 locator 的数字 loopback 端口等于 `AURIGA_HOST_PUBLISH_REGISTRY`；本例两个值均为 `50123`，不能装好后再改已签 image。
2. 从同一独立审核的正式材料执行 `init.sh`/bootstrap，生成封存的模板、公开根及原 M/C proof；保留 receipt。再运行 `preflight.sh`、`network-preflight.sh` 和 `render.sh`。材料验签成功本身不使镜像层就位。
3. 只启动既有 registry，例如 `docker compose -f deploy-nas/docker-compose.yml -p auriga-nas-test --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-deps --no-build --pull never registry`（示例中 `deploy-nas` 是解包后的目录）。宿主须能在批准的 `127.0.0.1:50123` 通过匿名 HTTP 到达它；不开放新的公网 registry 入口。
4. 将独立审核的含原 M/C/全部层的 OCI layout 与 ORAS 1.3.0 二进制交给 operator；核对二进制来源与 SHA-256。按 [runtime-release import-layout](runtime-release.md#linux-本地-registry-导入与真实读回) 给全套显式参数，在**全新**输出目录完成验签、快照、复制和逐对象实际读回。只有命令成功时，原仓库的 `repo@sha256:<原M>` 才获得当次可读证明。
5. 再按现有公开流程启动其它服务并做登录、网络、资源、真实 DSH 创建与完整生命周期验收。导入不写 identity/keyring/receipt，也不代替生产 agent 启用检查。

例如把已审核的 `AURIGA_RUNTIME_IMAGE` 设为 `127.0.0.1:50123/auriga/dsh@sha256:<原M的64位小写hex>`，然后将**同一完整值**给 `import-layout --allow-image`。不能重标 repository 或用 `docker save` 内部 JSON 冒充 proof；没有 public 官方根时，本机临时根也不能称为官方发行。本批不生成自动 NAS bundle 工作流，不在现场拉取镜像、登录 registry 或部署。全空模式的基础栈健康/static 检查不能启动真实 DSH 实例，
不能宣称实例已就绪；即使成功装配了正式材料，也仍需另做真实资源、网络、登录和完整生命周期验收。

## 材料与持续服务票据

源材料在 `config/materials/`，包括 `pki/`、identity 签名 key、生产 access、派生配置与 `iam-installation/`。源根、`pki/`、`templates/`、`runtime-keys/`、`runtime-proofs/` 及启用时的精确 proof 子目录均为10001:10001/0755；固定源叶子必须是10001:10001的单链接普通文件，所有PKI私钥及identity签名key为0600，其余固定公开材料为0644。NAS用持有的目录FD和no-follow打开验证这些元数据，拒绝未知叶/目录，并在receipt复用、生成封存前后复核。私密安装目录另为0700、文件0600、单链接；所有祖先只信 root/installer且不可组/其它写（root sticky临时祖先例外）。普通重入不修复源文件权限。`config/pki-edge/` 是单独的精确 runtime 叶副本，归65532，私钥0600，绝不包含 CA key、workspace key、IAM server key或proxy client key；identity签名key也独立复制，源文件不chown。

IAM 的 HTTPS HTTP-hop 使用专门 `iam` server 证书（DNS SAN `platform`）和 `edge-iam` client 证书（DNS SAN `edge.auriga.internal`），与节点mTLS和前置代理证书分开。platform 只认可固定 edge 组件网络 IP 和本地健康探针。gRPC 9091 使用同一专用hop mTLS，租户委派固定 `svc:edge`；edge不拿实例Ed25519 key伪造服务JWT。

platform维持uid10001，并加入reader group65532以给新JWT正确设gid。安装凭据目录只读挂载，实际 IAM exporter 每60秒重新认证并原子发布短时JWT；`internal-tokens/` 仅platform可写，edge只读挂载整个目录，逐RPC重读新inode，不挂单文件。输出叶目录0750、文件0640；没有把原文secret写进公开配置或环境。平台健康检查实际使用mTLS并要求当前票据文件非空。

前置HTTPS代理安装时，只把 `config/materials/pki/proxy-ca.crt`、`proxy-client.crt/key` 安装到受信代理的私密目录，不复制任何 CA 私钥或IAM/节点key。`templates/caddy.fragment.example` 分别反代portal/admin TLS listener，使用 `tls_trust_pool`、`tls_client_auth`、固定serverName；示例端口与 `access-proxy.example.json` 配套。配置语法依据 [Caddy官方文档](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)。实际代理IP/CIDR、浏览器证书和解析由部署者配置，本包不会改系统信任/DNS/VPS。

## 网络、权限与预算

`internal` 是 `internal:true` 的组件bridge，成员postgres/nats/platform/registry/edge；`AURIGA_INTERNAL_SUBNET` 和固定 `AURIGA_INTERNAL_EDGE_IP` 必须与LAN和现有路由不冲突。`publish` 只给platform/registry作宿主127.0.0.1端口映射，agent保持host网络。外部LAN网络只连接edge；其portal/admin按access listener实际端口提供服务，后端不接入LAN；内控虽为动态实例桥绑定wildcard，但edge在TCP Accept处联校内核实际本地／对端地址及当前直连前缀：目标须唯一属于非LAN私有接口，peer须处于该目标的同一前缀。LAN目标和任何外部可路由源到私有目标都早于HTTP／IAM被关闭。portal/admin保持原入口协议。网络预检不创建或调整NAS网络，不确定时exit4；端口/目标IP重入只接受本项目精确归属。

`AURIGA_EDGE_LAN_IP` 与 `AURIGA_LAN_SUBNET` 是必填可信安装输入（直接调用bootstrap时对应 `-lan-ip/-lan-subnet`），须为规范IPv4、地址归属该子网且与组件网不重叠。LAN配置纳入receipt输入摘要，变更或旧材料不匹配会拒绝，不自动修复。edge启动及每个新内控连接还核所有LAN IP在自身netns唯一归属UP接口，活跃接口前缀不得跨接口重叠。每连接两份一致的实际接口快照支持动态桥增删，缺失／多义／读取失败保守拒绝。该规则依赖既有LAN、组件及owned实例直连桥拓扑，不识别真实入接口，也不宣称与网络管理不存在竞态；不要另给edge接入外部路由网。

每实例owned bridge仍internal，只有该实例和经标签/身份/归属复核的edge两个成员，独立DNS alias指向内控listener；实例不加入组件网或publish网。共享socket父目录 `socket-share/` 为root:65532/0750，agent创建叶子；agent数据根保持0700，不预建叶子、不把私密agent目录给edge。

容器继续cap-drop ALL、no-new-privileges、非privileged。agent自身因Docker socket+host网络相当于宿主root，只补CHOWN/DAC_OVERRIDE/FOWNER以管理实例；它不是生产多租户强隔离模型。registry无认证但只在宿主loopback发布；前缀白名单不替代镜像摘要/签名信任。platform.env暂仍包含DB/NATS口令和加密key，Docker inspect权限意味着可以看到这些环境。

保留基础预算约3.5–4GiB、约2CPU；实例默认500m CPU、512MiB、128PIDs，容量MAX_INSTANCES=1。未启用runtime时保留原本地registry前缀policy，但没有可创建实例的模板；启用时改为上述独立批准的完整repository与唯一精确image限制。

operator 在 `stack.env` 设置 `AURIGA_INSTANCE_MAX_MEMORY_MB`，生成每实例 `agent-policy.yaml` 的 `maxMemoryMB`；直接调用 bootstrap 对应字符串参数 `-instance-max-memory-mb`。缺省和显式 `512` 都得到512MiB；确认宿主实际可用容量后可明确选择 `1024`。仅接受无前导零的正ASCII十进制整数，范围1–8796093022207MiB（`MaxInt64/(1024*1024)`），防止Docker字节换算溢出；显式空值、0、正负号、空白、小数、指数、非ASCII数字及越界在写材料前拒绝。该输入通过校验、root到固定uid10001子进程及生成使用同一规范值，不放入派生九字段 `access.env`。

每实例上限、节点总容量 `AURIGA_AGENT_CAPACITY_MEMORY_MB` 和 agent 容器自身的 cgroup 内存限制是三个独立配置；本参数不会自动提高后两者，也不调整CPU/PID或实例数。NAS正式模板请求使用同一预算值，默认/显式512请求512MiB，选择1024请求1024MiB；agent继续取min(规格请求,本地上限)，生产容量检查仍独立执行。512/1024都不是已验证的最低需求；生成参数成功不代表真实DSH在该预算下可用，不能把截断后的启动或未测场景记为完整生命周期通过。

规范内存值纳入receipt输入摘要；同值重入保留材料与receipt，变值拒绝且不修复原件。旧bootstrap生成的receipt不包含该输入，即使本次使用缺省或显式512也会拒绝。升级前应保留完整材料，另走独立受控迁移；本变更不提供旧receipt迁移、不删除receipt重建信任。现场预算仍须按真实可用容量选择和验收。

数据库仍PostgreSQL17 Debian uid999；workspace迁移/运行角色分开，iam/org combined仍沿既有限制单角色，RLS不变。NATS无匿名、监控只容器loopback；平台事件/审计、routes写、edge routes只读+sessions及撤销consumer ACL不扩权，JetStream保留内存/磁盘限制。

## 挂载访问与继承 ACL 排障

`ls -l` 显示的 POSIX mode（包括0755）不等于服务身份的有效访问权限。共享文件夹的继承 ACL、命名用户条目及 ACL mask 都可能使固定 UID 无法遍历目录或读取文件；宿主 root 能读取也不能证明 uid10001、uid65532 或 uid999 能读取。

已有 bootstrap 在清空补充组后以10001:10001真实读取并验证安装源材料、公开 runtime bundle 和独立根；`installer.sh check` 继续执行这条校验。新增的 `mount-access.sh` 补查 Compose 服务的运行挂载边界，`preflight.sh` 在安装完成且 Compose 配置校验成功后自动调用，`start.sh` 因而在启动服务前失败关闭。也可由具有本机 Docker 使用权限的部署者单独运行：

```sh
AURIGA_DATA_ROOT=/srv/container/auriga-test sh deploy-nas/scripts/mount-access.sh
```

探针只使用 `stack.env` 中已固定且本地存在的 platform 镜像提供的 `/bin/sh`、`cat`（edge 镜像本身无 shell）；不替换服务镜像。检查身份与包内 Compose 一致：platform为10001:10001并附加65532组，edge/NATS为65532:65532，PostgreSQL启动阶段读口令为0:0、降权后的SQL和数据目录为999:999，agent及包内registry为0:0。每次仅只读挂载该服务需要的源路径，禁用网络、丢弃全部capability、只读根文件系统并启用no-new-privileges，不挂Docker socket，不拉镜像。公开文件实际读取到`/dev/null`，私钥、口令、内部凭据和已有JWT仅验证打开权限，不读取或输出内容。首次启动前JWT尚未发布时只检查已有父目录，后续重跑会检查已存在的JWT。检查数据目录仅验证遍历，不递归读取用户数据，也不验证写入、磁盘配额或真实网络。

PostgreSQL 的 PGDATA 分阶段检查：仅当丢弃全部 capability 的 root 只读探针能遍历、列举并确认目录完全为空（包括隐藏项、悬空符号链接）时，输出`mount-access PGDATA phase=entrypoint-empty`，允许官方 entrypoint 在首启时按其既有流程准备所有者和权限。因此，真实`init.sh`在`umask 077`下创建的`root:root/0700`空目录可进入启动准备阶段。目录非空或无法确认空时输出`phase=runtime`，仍按999:999检查实际遍历；SQL始终按999:999检查。空目录放行只表示等待 entrypoint 准备，不证明运行身份已能访问，也不读取数据库内容或修改权限。

失败信息包含`service`、`uid:gid`、`supplementary`、`path-category`和操作类别，例如`service=edge uid:gid=65532:65532 supplementary=none path-category=pki operation=traverse`。这指向`config/pki-edge/`的运行挂载访问问题；`secret-open`表示必要秘密叶无法打开，日志不含秘密值。若探针镜像不具备`sh/cat`、本地镜像缺失或源路径缺失，也直接失败，不会回退root、联网拉取或放宽权限重试。

部署者应在自己新建的专用挂载目录上，先核对`stat`/`namei -l`显示的目录链和所有者，再用可用的`getfacl -p`或NAS厂商的共享文件夹ACL界面检查有效/继承规则、命名UID和mask。分别检查材料目录、edge副本目录及其实际挂载叶；宿主父目录的继承规则也影响新建子目录。保留只读诊断结果，确认是哪个服务身份、哪个目录或叶文件被拒绝，再在该专用目录按厂商支持的流程授予该身份必要访问，或另选权限已正确配置的新专用位置按正式流程安装。不要删除共享上级ACL、递归放宽整个共享目录、使用`chmod 777`，也不要把秘密文件改为公开可读。探针和安装重入不会自动修ACL；元数据或receipt不符仍按原规则拒绝，修复后重跑预检。

## 节点 label 与模板匹配

正式NAS模板的调度标签为`template=real-dsh`。节点已经入网、在线或有足够容量仍可能因label不匹配而无法调度；还需核对驱动、OS/架构、可调度状态及剩余容量。管理台的节点详情支持编辑名称、标签及是否调度；标签变更需确认并刷新服务端状态。没有该页面的旧版本也可使用正式管理API调整。在正式管理入口以具备`workspace.nodes.read`/`workspace.nodes.admin`权限的管理员调用`GET /api/workspace/v1/admin/nodes`及`PATCH /api/workspace/v1/admin/nodes/{nodeId}`，PATCH使用`application/merge-patch+json`，示例请求体：

```json
{ "labels": { "template": "real-dsh" }, "schedulable": true }
```

`labels`更新替换整份标签映射：先读取当前值，将其他需要保留的标签一并带回；仅在节点确实可承载该正式模板且准备接收实例时设置上述值。浏览器管理写入仍走正式会话、完整管理Origin和CSRF流程（包括IP模式的内存CSRF token）；不要通过直接改数据库、修改封存模板/receipt或绕过管理权限排障。更新后重新读取节点确认，再通过正常门户重试实例申请，并检查正式调度诊断。

停止只操作固定项目及严格核对标签/绑定路径的登记实例，不prune、不down -v、不remove-orphans。升级换精确镜像Id/摘要；DB迁移不能假设换旧镜像即可回退。备份 `secrets/`、`config/materials/`、data/postgres、data/agent、enrollment、stack.env与原access输入；停止前后保持材料，不依赖手工修权限。证书叶约825天，CA约10年，轮换应另行受审并滚动服务，不用普通重入自动换key。

## 验证与证据边界

设备 WSS 的内部接线由 bootstrap 生成：edge 的精确 `/net/v1/ws` 代理到 `wss://network-broker:18444`，以现有只读 `pki-edge/network-server-ca.pem` 验证固定 SAN `broker.network.internal`。broker 只在 `internal` 网络，没有 `ports` 发布；edge 不挂 broker 私钥、密码或 `network-platform`。Origin 白名单仅取已验证 PortalURL 的完整 scheme/host/port，不含管理台或 URL 路径。NAS receipt 输入升级 `network-wss-v2-https` 并绑定外部材料内容，旧材料重入拒绝，不修复/轮换，需保留备份后在新专用目录安装。

已有 `trusted_proxy` profile 保留独立 mTLS；前置代理须转发门户 `/net/v1/ws` Upgrade，保留可信 Host/Origin，并让客户端信任外部证书。可选 direct HTTPS 沿同一 Access 合同设置 portal/admin/issuer 为 HTTPS、对应 listener.tls=true，实例子域名 scheme/port 严格匹配 portal，instance-control 仍隔离 HTTP。在 `stack.env` 显式设置绝对 `AURIGA_HTTPS_CERT_FILE` / `AURIGA_HTTPS_KEY_FILE`，或 bootstrap 的 `-https-cert-file` / `-https-key-file`；container installer 只读挂载这两个外部文件。HTTP 和 trusted_proxy 不接受此直接 TLS 输入；默认 HTTP 不变。

direct HTTPS 证书须覆盖门户/管理域名及精确实例通配 SAN；纯 IP 使用匹配的真正 IP SAN。安装器校验期限、所含链内签名、密钥匹配后，由 root 通过固定继承 FD 向 uid10001 传只读快照，源私钥可保持 root:root/0600，受管副本纳入 receipt/UID 守卫，再按既有方式复制为 edge UID65532/0600 并只读挂载。完整 fullchain 可不包含根，但外部签发链的客户端信任由部署者提供，不自动安装信任，不以内部 broker CA 代替；Windows 仍仅支持 `-validate-access`，不激活 NAS 私密材料。不降级 WS、不自动把 HTTP 变成 WSS。

network submit 健康状态与生成 JSON 只证明部署接线的部分条件。正式设备入网还需真实 HTTPS 入口、插件 `wss://` 配置、IAM 分配的 realm、真实签名链提供的 Ed25519 安装根及真实连接/鉴权/收发证据；传输 TLS CA 不能充当安装根，本节不宣称 L55 已闭环。

Network 使用独立一年有效期的 CA/叶子，与上述节点 PKI 的有效期不同。platform 只目录挂载独立 `network-platform`（仅 `platform.properties`/`gateway.secret`，源目录 uid/gid10001/0700，文件0600，生成的 installation-owner=10001），broker 只挂载 `broker.conf`/`broker.pem`/`broker.key`/`server-ca.pem`；原源文件、安装回执与 uid10001:10001、目录0700/文件0600校验保持一致。目录与文件挂载更换材料后，均须重新创建相关容器。

start/render 重入、network 健康探针及 `sh deploy/nas/scripts/status.sh` 检查独立 network CA 和全部叶子：到期前30天输出 `network_certificate_expiring`，过期/损坏分别输出 `expired`/`invalid` 并检查失败，普通重入不更换私钥。停机或无法取得容器状态时 status 报 `unavailable_or_invalid`，不能当正常。请每天采集状态输出告警并提前安排维护；本次提供到期告警，不提供在线自动轮换。命令、权限与维护边界见 [部署材料维护](../../deploy/bootstrap/network/README.md)。

```sh
(cd deploy/bootstrap && go test -count=1 ./...)
(cd deploy/compose/bootstrap && go test -count=1 ./...)
(cd deploy/nas/bootstrap && go test -count=1 ./... && go build -o ../bin/auriga-nas-bootstrap .)
NAS_ALLOW_NONROOT=0 sh deploy/nas/test/run-tests.sh
# 独立Linux root + 已核实的本机Docker；两个参数都填本地已加载的精确Id/摘要。
AURIGA_MOUNT_TEST_IMAGE=sha256:<platform-image-id> \
  AURIGA_MOUNT_TEST_REGISTRY_IMAGE=sha256:<registry-image-id> \
  sh deploy/nas/test/mount-access-real.sh
(cd infra/agent && AURIGA_NAS_REQUIRE_ROOT_TEST=1 go test -count=1 -run '^TestNASRenderedAgentPolicy$' ./internal/policy)
(cd infra/edge && go test -count=1 ../../deploy/nas/test/nats_policy_test.go)
```

后3条真实权限/安装交叉测试须在独立Linux root测试环境运行；非root普通Go测试具名跳过固定uid安装，自动CI另有强制root步骤，不能以skip当通过。NAS脚本/Go负例在自动PR E2E中运行，不只在手动bundle workflow。smoke检查真实浏览器URL、mTLS健康与未登录拒绝；没有完整E2E hook时仍exit2/PARTIAL。静态Compose、配置加载或本机测试不等于真实NAS网络、浏览器登录、real DSH心跳或IP3完整验收。

???????????????? Key ?????? [LiteLLM ?????](relay-litellm.md)?
