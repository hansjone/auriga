# 中转站：Bifrost 核心与 Auriga 身份

后端由 Auriga 维护，管理台使用自己的中文页面，不发布上游 UI。

成员调用仍走门户 `/ai/v1`，经 edge 和 ai-gateway 在线校验 IAM、组织权限和设备/托管身份，再按租户与成员确保唯一虚拟 Key。每次确保都读当前状态，禁止自动复活被禁用的 Key，也不覆盖管理员已设预算。成员虚拟 Key 不能单独从公网使用：edge 仅接受 external 来源，后端对 member 来源还要求身份桥的服务认证。

外部调用使用管理员签发的 `sk-bf-` Key，经门户相同 `/ai/v1/chat/completions`、`/ai/v1/models` 入口。edge 每次在线查 Key 来源和状态，没有允许缓存；调用方不能传入供应商地址、供应商密钥或自动切换目标。归属信息由服务覆盖。

管理接口位于管理员来源 `/api/ai/v1/admin/relay`，edge 每次重新内省平台会话并检查权限；写入必须同源、CSRF、10分钟内二次验证，跨成员用量查询也须近期二次验证。管理上游仅在 Docker 私网，浏览器不能直接访问。内网管理口令只交给 edge；成员身份桥只持独立成员口令，无法调用管理接口。后端从可信 access.json 绑定安装租户，管理头和成员请求均不能跨安装租户。

后端是 `infra/ai-gateway/cmd/relay-backend`，只借 Bifrost core/providers，不含 transports/framework/plugins。每次请求加载不可变路由快照，使用上游加权选择器、worker、fallback、SSE 解析；禁用供应商和密钥变更在后续请求生效。请求与结算都有期限；下游断开后取消并等待 worker 结束。

主密钥经 HKDF 独立派生签名键和加密键；虚拟 Key 使用 HMAC，数据库不存明文。供应商凭据与管理幂等响应使用 AES-GCM，密文带格式版本1，租户/记录 ID 为 AAD。外发 user 使用 HMAC 匿名标识。修改供应商根地址或种类会清除原凭据，必须重新录入。管理 POST 必须 Idempotency-Key，同租户同键不同内容409；崩溃后的未完成声明保持阻塞，避免重复签发。五个独立文件秘密由部署工具首次生成并拒绝隐式旋转。

PostgreSQL 独立 bifrost 数据库，迁移 owner 与运行账号/口令分离。表强制租户 RLS。复用 PR #265 前预算预占/结算和用量记录，金额为 USD 整数微单位；RPM 用数据库行锁按 Key 分钟计数。每 Key 可设置1–100并发和1–1,073,741,824在途 token；新成员默认16并发/64Mi token，对外 Key 默认32并发/128Mi token。成员首次默认月预算/RPM来自身份桥配置，后续 Ensure 不覆盖管理员修改。池提供固定价格快照、上下文和输出上限，发出请求前保守预占；明确上游错误、拨号失败和发出前失败按零用量全额释放。断流/超时优先使用确认用量，缺失时以输入保守上界及已收到的输出字节估算 token，释放在途容量；未观察到输出时按输出上限估算。估算记为 estimated，不能当供应商确认用量。后台每30秒扫描安装租户，重启后也自动发现遗留请求；超过10分钟且派发令牌过期8分钟后，未派发 reserved 取消退款，dispatching/unknown 按整笔预占估算结算并释放容量。未知执行不能仅凭超时退款，以免重复消费超预算。用量可按成员或 Key 查询，不保存提示词正文。

部署：compose 直接构建 Dockerfile.bifrost；NAS bundle 增加 bifrost/operator 镜像并按不可变 ID 加载，不临时拉取。operator 仅供既有管理员初始化/应急工具，模型服务没有 Python。内网服务不发布宿主端口。新部署不再需要 relay 域名或 relayAdmin listener。

测试环境不迁移 LiteLLM 数据。更新前由主控保留所需现场证据，重建生成材料和镜像；旧 LiteLLM 数据/秘密/UI发布材料的现场删除另由主控处理，工人不操作 VPS、DNS、NAS 数据或真实 DSH profile。

本机验收入口：设置独立测试 PG 的 AURIGA_AI_TEST_PG_DSN 后，在 infra/ai-gateway 执行 `go test -count=1 ./internal/bifrostbackend ./internal/bifrost ./internal/modelgate ./internal/budget ./internal/store`。其中假供应商对话是真实 Bifrost 数据路径，真实 PG 会记录用量；这不等于真实 MiniMax/DeepSeek、桌面/云端 DSH、公网或 NAS 现场验收。

已知限制：第一步供应商配置仅包含名称/种类/根地址/启停，网络期限和 worker 数量使用服务固定上限。上游当前每请求创建 core（没有跨请求连接池）；不同 fallback 尝试的未知费用不能从最终成功 usage 中推断。现场发布前须审查并补验这些边界。用量 providerId/credentialId 表示派发前预占快照，不是 fallback 实际选中路由；池价是管理员统一向调用方计价，不代表供应商采购成本。跨供应商真实成本与逐尝试归属需另行扩展事件契约，当前不将其冒充供应商账单。管理幂等崩溃记录保留阻塞、503可重放；没有自动删除 in-progress，因为变更可能已提交，盲目超时重试会重复签发。需人工核对后使用新幂等键。密文格式版本不是主密钥轮换功能；轮换需要停写、重加密及令牌换发方案，禁止直接替换材料。

已有 PostgreSQL 卷的升级：先生成并保护新的五份中转站材料、加载审核镜像，确保原 postgres 健康，再运行 `sh deploy/nas/scripts/upgrade-bifrost.sh`（Compose 为 `sh deploy/compose/scripts/upgrade-bifrost.sh`）。脚本仅启动一次性 psql helper，幂等创建隔离角色、数据库和 schema，不清卷、不重启原库、不更改已有角色密码；错误 owner 或特权角色会拒绝升级。成功后再更新 Bifrost/ai-relay/edge。旧测试数据库和秘密不迁移，清理由主控单独批准。
