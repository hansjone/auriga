# 常驻共享 Node 安装与验收

【待审查】本批 Node 接线需要主控整合并行的 N 消息和 Host 消息来源交付。此处是部署配方，工位未执行部署、真实 PG、DSH、NAS 或供应商请求。普通门户入口由其作者交付；不能将合成夹具当成正常页面验收。

## 安装材料与启动顺序

1. 沿现有安装流程准备 platform 的独立迁移/运行角色和 mTLS 服务认证。W、IAM、Resource 运行在 platform 原组合；加载 [公开开关样例](shared-runtime-platform.example.properties)，ticket/三用途签名文件由安装流程提供。Node 内部服务 JWT 的 principal 必须为 `svc:agent`、audience 为 `auriga.internal`，委托租户等于部署租户；按既有签发器续期并原子替换文件。每次 RPC 重新读取该文件，令牌不装到 Host。IAM 企业代发另按 [IAM 配置](../../services/iam/impl/SHARED-TASKS.zh.md) 设置私有 `sharedOutbound` 白名单；raw=false 仍可代发。
2. 在 N 原 `--bifrost-config` 配置中添加 [模型独立 listener](../../infra/ai-gateway/SHARED-DSH.zh.md)。同时整合 N 消息作者的 `sharedMessages` listener：固定 `listen,tenantId,allowedNodeIds,tlsCertFile,tlsKeyFile,clientCAFile,messagePublicKeys,collab`，`collab` 沿原 `team.RPCConfig` 的 mTLS/轮换服务 JWT。模型和消息使用独立监听地址；Node 配置各自固定 origin/CA，不能把成员入口当内部入口。证书必须具有唯一 `auriga://node/<Node UUID>` URI SAN，N 的专用 CA 和精确 allowlist 必须认可该 Node。N 使用原迁移、真实调用账本和 collab 邮箱，不另启内存实现。
3. 启动 platform 原服务，再启动 N：`auriga-ai-gateway --bifrost-config /etc/auriga/shared-runtime/bifrost.json`。服务监听端口只向授权 Node 开放。普通 keyset 暂时过期时保留已安装消息 handler，调用真实失败；不移除 handler 令 Host/model 启动失败。
4. 将 [Agent 公开样例](shared-runtime-agent.example.json) 与既有 node enrollment 配置合并，安装到 `config/materials/shared-runtime/agent.json`；将 [服务样例](shared-runtime-services.example.json) 填为部署 tenant、真实固定 origins 和各 CA 的绝对路径。服务 token 文件只给 Agent，Unix 0600；各 CA/公开配置禁止组/其他人写入。已 enroll 的 `agent/node.crt`、私钥和 CA 沿既有存储加载，不重新生成身份。
5. 将批准的完整 `InstanceSpec`（`dsh` 内有原签名发行信封）装入 `runtime.json`，原始 M/C 证明装入 `runtime-proofs`。运行时必须导出 `auriga-dsh/shared-ports`；`profile.yml` 使用生产 profile（`auriga-dsh/shared`、`auriga-dsh/shared-ports`、webserver、native tools、仅平台 filesystem skills）。禁止凭空填写发行摘要、跳过发行校验或临时挂可变 ports 模块。Agent 渲染 launcher 并启动唯一常驻 Host，不手工另起第二个 DSH。
6. Resource 复用既有 `agent/resource-trust.json` 的 `schemaVersion:1,origin,keys`、Node 客户端证书和 CA。origin 指向 Resource 直接 HTTPS 服务。每成员的 signed ref 仅直接 GET `/internal/resource/v1/catalog/downloads/<lease>/manifest` 和 `/artifacts/<artifactId>`；不再配置 `sharedResources`，不使用旧 POST 或成员 API。release/revocation/downgrade 用途公钥必须独立，沿原安装信任包与轮换流程更新。
7. 用原批准 Edge identity、IAM 三用途公开 JWKS 和企业公开策略渲染 Host 公开配置。`message-trust.json` 只含批准的 `{realm,roots:[{kid,public_key_spki}]}`，SPKI 为 Ed25519 DER 的无 padding base64url；不能从 context 回包复制任意公钥为根：

```sh
auriga-agent render-shared-host-config \
  --identity-keys /etc/auriga/public/identity.jwks.json \
  --enterprise-keys /etc/auriga/public/enterprise.jwks.json \
  --model-keys /etc/auriga/public/model.jwks.json \
  --message-keys /etc/auriga/public/message.jwks.json \
  --enterprise-config /etc/auriga/public/enterprise-policy.json \
  --message-trust /etc/auriga/public/message-trust.json \
  --model approved-model-id \
  --output /etc/auriga/shared-runtime/host.json
```

输出必须是新文件；在既有池上改变公开配置、信任根或发行须走显式维护，不覆盖运行中安装。`messageTrust` 可缺省，只使消息不可用，不影响模型/工具。Host 验证 context 返回的原 control/publication/announcement、SourceGet/page/item/admit 签名，不采信 `verified:true`。

8. 按现有环境装载批准镜像和变量，检查 Compose 渲染后只启动 Node：

```sh
docker compose -f deploy/nas/docker-compose.yml -f deploy/nas/shared-runtime-compose.yml config
docker compose -f deploy/nas/docker-compose.yml -f deploy/nas/shared-runtime-compose.yml up -d agent
```

入口最终为 `auriga-agent run --data-root /var/lib/auriga --config /etc/auriga/shared-runtime/agent.json`。服务端 NodeControl mTLS Welcome 提供可信时间；认证、资源签名/撤销、真实字节、完整成员视图、配额和签名发行全部确认后才发 READY。主干的同执行分区 intent、高水位、实际执行身份围栏保留；未决 Engine 动作不因取消或重连被当成排空。

## Linux/NAS 存储与进程边界

NAS 原 ext4 不要求 project quota，也不重格式化。`ext4-image` 在 Node 私有 control 下预分配普通 backing file，以 loop 挂为独立 ext4/project quota，Docker 用同一 loop 设备建立受管 volume。镜像仅启用内核 5.10 兼容特性：关闭 orphan_file/metadata_csum_seed。任何设备、superblock、挂载 prjquota、工程配额或读回不符均拒绝 READY，不回退软配额。

Agent overlay 明确要求 root、host user/PID namespace、loop 设备、CHOWN/DAC_OVERRIDE/FOWNER/SYS_ADMIN；Host 自身仍是私有 PID namespace、非 root、只读根、无 capabilities。host PID namespace 仅用于将 UDS SO_PEERCRED 的 PID/启动 tick 沿 `/proc` 父链绑定到已验签的真实容器进程；每次调用和结果前复核，不能用 body 的 boot/task/member 绕过。Docker endpoint 必须是同机 daemon。

20 成员各 5GiB/10万 inode，Host home、message-trust、message-preferences、message-inboxes 各另占一份同等硬配额；镜像最低 24×5GiB，样例 128GiB 只是起点。30天归档同样占镜像实际容量，须另加保留数据、文件系统元数据及余量，不能据配额总和声称容量充足。空间或项目号耗尽保持 blocked，不能清除活跃账本或挪动在途成员。

归档位于镜像内 `members/node-archives/<operation>`，父目录 Node UID 0700，Host UID 不可读取；control journal 在镜像外、未挂入 Host。重试以持久的原 pool/member/epoch/operation 恢复 rename、归档配额、新空工作区，不跨设备移动、不覆盖原数据、不把已删除操作重跑。保留期删除先落墓碑，再删目录，确认内核已无使用/open inode 并清配额读回后才回收项目号。

Host 的固定 `/var/lib/auriga/pools/<pool>/host/message-trust/ledger.json` 映射到同一持久镜像内的 `members/message-trust/ledger.json`，独立硬配额并归属池 UID。重连、重启不重建或删账本，不借此绕过签名链防退。
Host 新增的同根 `host/message-preferences`、`host/message-inboxes` 同样映射到 members 下对应 Node 预备目录，Node 先安装并读回各硬配额、严格交给池 UID 才启动 Host；不把当前目录缺失当成清空旧邮箱的理由。

## 正常 shared 原生浏览器装配

须整合原生 browser 作者已冻结的 `auriga-dsh/shared-browser` export 及 Host 的 `admitBrowser/refresh/authorize/create/messagingSessions/messagingSettings` 实现后，才可安装该 profile。Node 不复制另一作者未提交源码，也不以现有 JSON carrier 当成聊天页面。在完整 shared Cordis profile 的 `auriga-dsh/shared` 行之后显式加入固定 `auriga-dsh/shared-browser` 行；config 只允许 `distIndex`，其值必须为已批准 runtime 中与原 frontend-static 相同的真实绝对 `index.html` 路径，不是 URL、Node 文件或可变 JS 表达式。该行进入原不可变配置摘要，不在运行中覆盖配置。

shared 正常 Open URL 是实例根 `/`（或 `/index.html`）。浏览器使用同源 POST `/auriga/shared/browser/rpc` 与 `/auriga/shared/browser/stream` 的有限原生操作和 NDJSON，消息设置 POST `/auriga/shared/messages` 同样经过当前身份。每次请求由 Edge 签当前完整 E 的精确方法/路径，Node 保留 W 当前归属、单次 JTI 与撤销 watcher，并剥除到 shared Host 的 Cookie/Authorization。

Node 对 shared 明确拒绝旧 `/api`、`/connect` 命名空间（包括转义别名）和所有 Upgrade；不能把原 `/api/remote.mux` owner WebSocket 加个 E 头就放行。Edge 也须同样拒绝，Host 监听地址只允许 Node 私有转发访问。dedicated 不加载 shared-browser，此批不将其既有 E/W 门锁降为 owner 票。仅 HTML、health 或 profile JSON 返回200都不足以证明可聊天，主控须用正常入口完成真实页面/会话/消息设置验收。

## 可回读验收

格式化参数使用 mke2fs 的 `-E lazy_itable_init=0,lazy_journal_init=0,quotatype=prjquota`，不使用其不支持的 `-Q`。固定 mkfs.ext4/mke2fs/losetup 失败时保留原错误链、参数及最多8KiB且已转义的 stderr，工具环境不含服务令牌。公开 CI/本机 Linux 可先执行只创建临时普通文件的真实工具回归：

```sh
cd infra/agent
GOMAXPROCS=2 go test -p 1 ./internal/storage \
  -run '^Test(Ext4FormatWithRealMke2fs|StorageCommandDiagnostics)' -count=1 -v
```

需已安装真实 e2fsprogs（Agent 镜像已包含）；测试以非 root 即可调用生产格式化函数并读回真实64MiB文件的 superblock/精确大小/特性，不连接 loop、不挂载或修改已有磁盘。工具缺失明确失败，不以 fake 录参通过代替。格式化通过仅证明 CLI/文件格式，不证明 NAS 的 project quota、继承、写满、重挂或整机恢复已验收；这些由主控另跑真实内核探针。

普通受控任务沿 enterprise 目的的独立 `task.execute`，不与 credentials/outbound 混发，不能在原 handle 上升级。Authorize 返回生成 enum 的正式字符串 enterpriseCredentialState：ABSENT 只在有效平台会话确无公司材料时可继续普通工具，AVAILABLE 才由 Host 同一真实 execution 开独立企业任务；0/未知状态拒绝。公司 token 轮换不改变普通任务版本，平台登录本身失效仍拒绝。该能力不增加 shared 的工具或插件白名单。

最终 messageContext 保留 mailbox 的 receiveEnabled/receiveVersion/headSeq/committedCursor 和原 messageTrust 三签名对象；Node 核当前完整 K、task/context、connection/boot/native/witness 与 canonical uint64，不初始化 Host inbox。只有 Host 原耐久账本规则允许时才首次建空收件箱，不能以 false 或丢账本清空历史。

主控先串行做纯合成 W/IAM/N/Node/Host 组合与非 root Linux CI，再按授权小批现场验收。Node 的真实安装 health 包含 enterpriseOutbound，以及按配置安装的 model/messageContext/send/receive。公开模型固定 call UUID，原 JSON/SSE 转 Host NDJSON；取消、断流和 unknown 不重放，不伪造 DONE。

消息 context bootstrap 只登记真实来源；send 的 platform_stored/pending 不等于投递/执行成功。receive 仅可信后台 `receiveSet/fetch/attemptBegin/executionAdmit/receipt`，初始不自动开启接收，Host 真正持久接收后才收据。当前 N 首批只承诺 shared↔shared，ordinary desktop 交叉明确拒绝。正常门户、实际供应商、真实磁盘配额与崩溃恢复仍须主控验收，不能以本工位 fixture 的通过替代。
