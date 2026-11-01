# 中转站：Bifrost 核心与 Auriga 身份

本次交付第一步后端。浏览器管理页面在第二步复原为中文页面；不发布 Bifrost 或 LiteLLM 自带 UI。

成员调用仍走门户 `/ai/v1`，经 edge 和 ai-gateway 在线校验 IAM、组织权限和设备/托管身份，再按租户与成员确保唯一虚拟 Key。每次确保都读当前状态，禁止自动复活被禁用的 Key，也不覆盖管理员已设预算。成员虚拟 Key 不能单独从公网使用：edge 仅接受 external 来源，后端对 member 来源还要求身份桥的服务认证。

外部调用使用管理员签发的 `sk-bf-` Key，经门户相同 `/ai/v1/chat/completions`、`/ai/v1/models` 入口。edge 每次在线查 Key 来源和状态，没有允许缓存；调用方不能传入供应商地址、供应商密钥或自动切换目标。归属信息由服务覆盖。

管理接口位于管理员来源 `/api/ai/v1/admin/relay`，edge 每次重新内省平台会话并检查权限；写入必须同源、CSRF、10分钟内二次验证，跨成员用量查询也须近期二次验证。管理上游仅在 Docker 私网，浏览器不能直接访问。内网服务认证与租户头只由可信 edge 写入。

后端是 `infra/ai-gateway/cmd/relay-backend`，只借 Bifrost core/providers，不含 transports/framework/plugins。每次请求加载不可变路由快照，使用上游加权选择器、worker、fallback、SSE 解析；禁用供应商和密钥变更在后续请求生效。请求与结算都有期限；下游断开后取消并等待 worker 结束。

虚拟 Key 由主密钥派生 HMAC，数据库不存明文；供应商凭据和管理幂等响应 AES-GCM 加密，租户/记录 ID 作为 AAD。管理 POST 必须 Idempotency-Key，同租户同键不同内容409；崩溃后的未完成声明保持阻塞，避免重复签发。四个独立文件秘密由部署工具首次生成并拒绝隐式旋转。

PostgreSQL 独立 bifrost 数据库，迁移 owner 与运行账号/口令分离。表强制租户 RLS。复用 PR #265 前预算预占/结算和用量记录，金额为 USD 整数微单位；RPM 用数据库行锁按 Key 分钟计数。池提供固定价格快照、上下文和输出上限，发出请求前保守预占；最终 usage 缺失则冻结预占供后续核对。用量可按成员或 Key 查询，不保存提示词正文。

部署：compose 直接构建 Dockerfile.bifrost；NAS bundle 增加 bifrost/operator 镜像并按不可变 ID 加载，不临时拉取。operator 仅供既有管理员初始化/应急工具，模型服务没有 Python。内网服务不发布宿主端口。新部署不再需要 relay 域名或 relayAdmin listener。

测试环境不迁移 LiteLLM 数据。更新前由主控保留所需现场证据，重建生成材料和镜像；旧 LiteLLM 数据/秘密/UI发布材料的现场删除另由主控处理，工人不操作 VPS、DNS、NAS 数据或真实 DSH profile。

本机验收入口：设置独立测试 PG 的 AURIGA_AI_TEST_PG_DSN 后，在 infra/ai-gateway 执行 `go test -count=1 ./internal/bifrostbackend ./internal/bifrost ./internal/modelgate ./internal/budget ./internal/store`。其中假供应商对话是真实 Bifrost 数据路径，真实 PG 会记录用量；这不等于真实 MiniMax/DeepSeek、桌面/云端 DSH、公网或 NAS 现场验收。

已知第一步限制：第一步供应商配置仅包含名称/种类/根地址/启停，网络期限和 worker 数量使用服务固定上限。上游当前每请求创建 core（没有跨请求连接池）；不同 fallback 尝试的未知费用不能从最终成功 usage 中推断。现场发布前须审查并补验这些边界。旧 phase1 中 LiteLLM 专用验收器不得作为新后端证据，应在第二步配合页面与真实 DSH 重新接线。
