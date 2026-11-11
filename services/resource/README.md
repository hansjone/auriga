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

管理台入口为 `/resources/review`、`/resources/published`、`/resources/distribution`。
共享版本作者不能自审或自发；L4 本人私有版本显示“未审核”并独立签名。
所有写操作确认后提交，冲突重新读取服务端版本，敏感操作沿既有身份再验证流程。
审核详情提供变更说明、文件摘要清单、权限声明和带原因的审核记录。

新增接口详见 `contracts/openapi/resource.yaml`：审核/发布详情、草稿恢复、个人签名、
管理侧分发列表、当前租户撤销清单，以及节点 `POST .../{downloadId}/renewals`。
节点续领要求 Idempotency-Key；同键在 24 小时内返回原到期时间，新一轮续领使用新键。
locator 至多授权 5 分钟，每次下载或续领都复查当前节点调度、主人、精确选择和资源 ACL。
仅支持整包下载，Range 返回 400。

分发 202 仅为已登记。resource 保存意图/outbox，platform 适配器经 workspace 服务事务
写入固定版本、selectionRevision 和新 generation。节点回报同一目标代数且 ready 才已生效。
后台公平轮询意图，重试不解析 latest；发行声明临近到期时按相同版本/摘要/序号重签并记审计，
更新规格代数后重新等待节点确认。下架阻止新安装、启用和下载；紧急撤销另签撤销清单，
workspace 将受影响实例设为 STOPPED。断网最迟停用期限由客户端可信清单有效期（≤24h）控制。

暂存由对象存储清理过期、未持锁的 stage。DB 失败产生的已提交孤儿对象目前保留，
没有自动删除已提交对象的无引用回收器；不能把下架等同于物理删除。显式降级授权、
撤销发行 kid 和根轮换管理不在这三页开放，低于发行高水位的新安装失败封闭。

本模块的 HTTP 装配测试使用随机端口、真实 PostgreSQL、真实归档与临时签名密钥，
workspace 实测规格写入和 inventory 状态；它不替代 NAS 正常页面、真实节点/DSH 执行、
mTLS 正向连接与现场离线停用验收。
