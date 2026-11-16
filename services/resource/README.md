# 插件资源工作流

合并部署启用 `auriga.services.resource.enabled=true`，使用 platform-app 已有的独立
resource 迁移角色、运行角色、数据库与游标签名配置。V3 迁移保存审核证据、24 小时幂等结果、
发行高水位、撤销清单、分发意图及节点下载授权。既有未核验归档的元数据版本不能直接审核或发布。

上传、签名和分发还需要这些部署配置：

- `auriga.resource.object-directory`：已存在的绝对物理目录，由服务账号独占写入。
  对象按租户和校验过的原始 SHA-256 定位；上传不执行插件或安装脚本。
- `auriga.resource.release-kid`、`auriga.resource.release-key-file`：发行 Ed25519 私钥。
- `auriga.resource.revocation-kid`、`auriga.resource.revocation-key-file`：独立撤销 Ed25519 私钥。
  两个 kid 和两把密钥必须不同。文件采用 PKCS#8 DER（不是 PEM），至多 4096 字节；
  禁止符号链接、间接路径、非受信主体可读的私钥及可写的密钥目录。POSIX 用服务账号
  所有的 0600 文件、0700 目录；Windows ACL 仅允许所有者、SYSTEM、Administrators。
  不将私钥、真实部署路径或授权下载 URL 放入日志、事件、仓库或浏览器。
- `auriga.resource.download-base-url`：例如
  `https://resource.example.com/internal/resource/v1/downloads/`。
  这是节点可达、直接验证客户端证书的内部 TLS 入口。必须将 servlet 连接的真实 X.509
  客户端证书交给应用；不接受转发证书头或 bearer 作为节点身份。复用 workspace 节点 CA、
  URI SAN、当前证书序列号和吊销状态验证。公网 edge 不放行 `/internal/resource/**`。
- combined 模式开启 workspace，并沿既有独立可信部署路径分发发行与撤销公钥根。
  ApplySpec 只携带签名声明与撤销清单，不能替换客户端根信任。

缺少对象存储、签名或 workspace 配置时，相应操作返回 503；只读元数据入口仍可用。
前台可使用已有域名或纯 IP/HTTP 模式，内部节点下载始终要求 TLS/mTLS。

## 开发与现场资源签名初始化

### 本机生成与保管

在可信本机、仓库根目录使用 Node 22 执行以下命令。输出目录的父目录须已存在，
选择仓库外的新目录，并由生成账号独占管理其父目录。所有路径均为部署占位符。

```powershell
node scripts/resource-keys/generate.mjs --out C:\secure\auriga-resource-dev-batch1 --kid-prefix dev-batch1
```

Linux 同样使用 `--out /secure/auriga-resource-dev-batch1`。省略 `--kid-prefix` 时生成随机前缀，
指定前缀支持 1–110 位字母、数字、点、下划线和短横线。每个用途追加独立后缀作为 kid。
脚本仅调用本地密码学和文件系统，Windows 使用系统 PowerShell 设置 DACL。
三把密钥临时生成后写入受保护文件，标准输出仅含目录和公钥根文件 SHA-256。

- `platform/release.pk8` 与 `platform/revocation.pk8` 为平台签名私钥。
- `offline/downgrade.pk8` 为独立降级授权私钥，应在离线受控介质保存。
- `resource-roots.json` 仅含 release、revocation、downgrade 的 kid 与 SPKI DER base64 公钥。
- `platform.properties` 含两个平台用途的 kid 和容器内私钥路径占位符。

私钥采用无口令 PKCS#8 DER，Node 使用 `type: 'pkcs8', format: 'der'` 导出，
对应 `ResourceSigning.load` 的 `PKCS8EncodedKeySpec`。POSIX 目录为 0700，文件为 0600。
Windows 在写入私钥前关闭目录 ACL 继承，仅授予当前用户 SID 完全控制，文件继承该 DACL。
输出目录已存在、父路径为符号链接或 junction 时拒绝。并发或重复生成不会覆盖旧输出。
中途失败可能留下受保护的部分输出，核对后选用新的目录重新生成，保留原目录供排查。
私钥、目录完整备份和终端重定向内容均应留在受控本机或离线介质，公钥 JSON 可用于 CI。

平台 `ResourceRuntimeConfiguration.resourceSigning` 仅装配 release 与 revocation 私钥。
当前平台没有 downgrade 私钥配置和在线降级授权签发入口，现有信任协议提供降级验签规则。
本次生成的 downgrade 私钥只作离线保管，后续正式降级工具与审批流程另行接线。

### NAS 或现场平台放置

主控通过受控传输只复制 `platform` 下两把私钥到 NAS/现场密钥目录。
在启动容器前将宿主机目录和文件所有者设为**容器实际服务 UID/GID**，目录权限 0700、
文件权限 0600，逐项核实绝对物理路径及所有祖先路径均无符号链接。
例如宿主机目录 `/srv/auriga/secrets/resource/platform` 只读挂载至
`/run/secrets/auriga-resource`，Compose volume 片段如下。

```yaml
volumes:
  - /srv/auriga/secrets/resource/platform:/run/secrets/auriga-resource:ro
```

私钥无需复制进镜像、构建上下文、对象存储或桌面包。服务使用生成的 properties 片段，
按实际只读挂载路径调整 `key-file`。

```properties
auriga.resource.release-kid=dev-batch1-release
auriga.resource.release-key-file=/run/secrets/auriga-resource/release.pk8
auriga.resource.revocation-kid=dev-batch1-revocation
auriga.resource.revocation-key-file=/run/secrets/auriga-resource/revocation.pk8
```

Windows 原生平台部署时使用实际服务账号所有的物理目录，配置绝对路径并使用正斜杠，
如 `C:/secure/auriga-resource/platform/release.pk8`。生成账号与服务账号不同时，由部署管理员
重新设置目录和文件所有者及受保护 DACL，使实际服务账号可读取、其他普通账号无权读私钥或写目录。
不要使用映射盘、UNC 或 junction。启动后通过正常管理台上传、审核、发布，
再从桌面资源市场安装一个已签资源，并核对撤销清单可获取及客户端验签成功。
健康检查和 503 消失只证明部分配置就绪，完整验收仍需正常页面操作。

### 桌面固定根与摘要核对

在 Installer workflow 的 `workflow_dispatch` 输入 `resource_trust_json` 中提供同批次
`resource-roots.json` 的原始 JSON 文本或标准 base64。该输入仅用于公钥，
平台的私钥文件始终留在受控部署端。CI 写临时文件、复用构建器校验后传给
`--resource-trust-json`，结构错误、非 Ed25519、公钥用途重复及公开测试向量公钥会被拒绝。
未提供输入时沿用现有构建行为，包只提供私有技能能力。

本机可使用以下命令读取公钥 JSON 并计算可比较的摘要。

```powershell
Get-Content -Raw C:\secure\auriga-resource-dev-batch1\resource-roots.json
Get-FileHash -Algorithm SHA256 C:\secure\auriga-resource-dev-batch1\resource-roots.json
```

生成器输出紧凑 JSON，CI 和构建器保持相同 JSON 序列化。
CI 日志的 `resourceTrustSha256` 与产物 `desktop-build.json` 同名字段
记录实际包内 `payload/auriga/runtime/resource-roots.json` 的原字节 SHA-256，
未固定根时记录 `null`。手工重排 JSON 字段或格式会改变文件摘要，核对时使用同一原字节文件。
该文件还在包 manifest 的受校验文件清单内。Desktop 自身发行签名公钥
`DESKTOP_RELEASE_PUBLIC_KEY` 是另一项现有配置，资源根按本节单独提供。

### 轮换与撤销

每个环境与每次轮换使用新目录、新密钥和新 kid，安全备份对应公钥、私钥及用途记录。
当前 Desktop 每个用途只固定一个根，没有自动下载新根或双根过渡机制。
轮换前需统筹平台签发、公钥固定的 Desktop 重建/升级与旧客户端停用窗口，
旧根客户端无法验证新根签发的资源和撤销清单，新根客户端也无法验证旧根签名。
需先完成受控切换方案和现场验收，保持撤销清单的可验证性及有效期。
保留客户端既有高水位、已消费降级授权和撤销修订，按原数据重签并增加必要修订。
不复用旧 kid 指向不同公钥，不通过清空客户端状态完成轮换。

正常下架阻止新的下载与启用。恶意资源按正式紧急撤销流程签发更高修订清单，
保留已有撤销事实；在线及时传播，离线最迟停用仍受有效期不超过 24 小时的清单约束。
发行私钥泄露时使用独立撤销私钥撤销受影响发行 kid/制品，再执行受控根轮换。
撤销私钥泄露或丢失时需通过独立可信的客户端发布渠道更换固定根，
平台当前界面尚无发行 kid 撤销及根轮换管理入口，应由主控制定对应操作批次。

管理台入口为 `/resources/review`、`/resources/published`、`/resources/distribution`。
共享版本作者不能自审或自发；L4 本人私有版本显示“未审核”并独立签名。
所有写操作确认后提交，冲突重新读取服务端版本，敏感操作沿既有身份再验证流程。
审核详情提供变更说明、文件摘要清单、权限声明和带原因的审核记录。
上传必填包修订的 `If-Match`，分发登记要求 600 秒内 step-up，成员和管理员调用均适用。
这两个接口语义已由主控接受，MARKET-CLIENT 按更新后的 OpenAPI 对齐。
上传版本使用严格 SemVer，发布低于当前发行高水位版本的 SemVer 返回冲突。
新声明统一生成 UTC 最短小数时间，旧 SignedArtifact 按 Timestamp 最短映射签名。
已有 envelope 保留原时间字符串，兼容原精度的签名正文。

新增接口详见 `contracts/openapi/resource.yaml`：审核/发布详情、草稿恢复、个人签名、
管理侧分发列表、当前租户撤销清单，以及节点 `POST .../{downloadId}/renewals`。
节点续领要求 Idempotency-Key；同键在 24 小时内返回原到期时间，新一轮续领使用新键。
locator 至多授权 5 分钟，每次下载或续领都复查当前节点调度、主人、精确选择和资源 ACL。
普通对账保留原授权到期时间，只有显式节点续领会延长授权。
仅支持整包下载，Range 返回 400。

分发 202 仅为已登记。resource 保存意图/outbox，platform 适配器经 workspace 服务事务
写入固定版本、selectionRevision 和新 generation。节点回报同一目标代数且 ready 才已生效。
后台公平轮询意图，重试不解析 latest；发行声明临近到期时按相同版本/摘要/序号重签并记审计，
更新规格代数后重新等待节点确认。下架阻止新安装、启用和下载；紧急撤销另签撤销清单，
workspace 将受影响实例设为 STOPPED。断网最迟停用期限由客户端可信清单有效期（≤24h）控制。
V4 保存撤销事实和每实例待处置修订，对账和即时传播都幂等地禁用插件并停实例。
单实例传播使用独立资源事务，数据库异常只回滚该实例待项，其他实例继续。
传播失败留待重试，规格已知某清单修订仍会重新核对停用事实。
V4 对历史重复撤销项去重并标记待重签，读取/传播前以更高修订重签。
再次恶意撤销同版本和摘要为幂等空操作，已紧急撤销版本隐藏管理台撤销按钮。
普通下架且尚未撤销的版本仍可在管理台发起紧急撤销。
新选择取代旧选择时，旧行的 applied/failed 保留最后记录的结果、错误及观察代数。
requested/resolved/applying 转为 superseded，显示“已取代”，保留已有观察字段。
appliedGeneration 仅是过去成功应用的证据，后续真实失败仍保持 failed。
同一固定选择的自动重签属于服务端重新授权，在当前成员、资源 ACL、策略及撤销事实复核后进行，
只续相同版本/摘要/发行序号并记 SYSTEM 审计，新增版本仍走人工审核发布。

暂存由对象存储清理过期、未持锁的 stage。独立回收器每小时扫描物理对象，
仅在对象超过 24 小时、持有与上传共用的摘要数据库锁并再次确认无版本引用时删除。
上传在提交对象前检查实时授权、ETag、幂等冲突和版本唯一性。
Servlet multipart 使用独立临时目录，资源上传允许 64 MiB 归档及受限元数据/边界，
edge 仅放宽精确资源上传路由，其他 API 沿原请求体上限。显式降级授权、
撤销发行 kid 和根轮换管理不在这三页开放，低于发行高水位的新安装失败封闭。

本模块的 HTTP 装配测试使用随机端口、真实 PostgreSQL、真实归档与临时签名密钥，
workspace 实测规格写入和 inventory 状态；它不替代 NAS 正常页面、真实节点/DSH 执行、
mTLS 正向连接与现场离线停用验收。

## 桌面成员接入

`/api/resource/v1/member/download-tickets` 接受 `clientId/packageId/versionId/digest`，
返回 60 秒、单次使用的票据及原始签名 envelope 和撤销清单，响应禁止缓存。
调用者需当前有效成员和 `resource.packages.read`，支持 USER 的 portal/admin 会话和 DEVICE 的 device 令牌。
DEVICE 的 clientId 必须等于可信令牌 did。所有数据库记录按租户、成员和客户端隔离。
签发幂等记录只保存完成标记，V6 迁移清除历史缓存中的明文票据。
同键在24小时内再次请求返回409，响应不确定时也使用新键重新领票。
`POST /member/downloads` 在 JSON 正文提交 `clientId/ticket`，同时提交成员 Authorization 和 Idempotency-Key。
票据以 SHA-256 保存，消费时锁定并重核 ACL、策略、发布状态、摘要及撤销，再标记已消费。
重复或并发消费仅一次返回归档，其余返回 409。断线重领新票据及请求键，Range 返回 400。
消费接口只校验 Idempotency-Key 的格式，由票据自身防止重复消费，消费请求不缓存幂等响应。
票据始终放在正文，接口不接受 URL 票据，审计只记录制品标识。
`POST /member/installations` 接受同一精确引用与 `action`，用 `If-Match: "0"` 首次登记，
后续使用响应安装修订。支持 install/enable/disable/remove，L0 固定、L1 禁关卸、L2 禁卸，L4 复核本人和策略。
返回 202/applying，卸载后保留发行高水位。`POST /member/installation-reports` 提交同一引用、
`installationRevision` 和 applied/failed，陈旧修订冲突。`GET /member/installations?clientId=...`
按 nextAfterPackageId 翻页，reportSource=member 明示为成员客户端回报，workspace 节点状态仍走原分发接口。
这组成员接口不要求管理分发权限或额外 step-up，身份、ACL、等级和策略由资源服务实时核对。
