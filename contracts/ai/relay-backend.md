# 中转站内网边界

公开管理契约见 openapi/ai.yaml 的 Relay 类型与 /api/ai/v1/admin/relay；数据入口保持 /ai/v1。

以下只供可信服务，不经过浏览器：
- POST /internal/members/ensure：Basic auriga + 文件服务口令，RelayMemberEnsure → RelayKeyIssued。租户/成员来自身份桥的实时权威结果。重复确保只返回原 Key，不覆盖策略或复活吊销。
- GET /internal/keys/check：X-Bf-Vk，返回 name/active/source；仅用于 edge 每次判断 external/member 来源，不能代替数据路径再次验证。
- /v1/models、/v1/chat/completions：external 用 Bearer；member 用 X-Bf-Vk 且必须服务 Basic。拒绝 Cookie、Origin、路径编码、供应商路由覆盖参数。
- /api/providers、/api/pools、/api/virtual-keys、/api/logs：Basic + 唯一 X-Auriga-Tenant。POST 必须 Idempotency-Key。请求/响应类型与公开管理契约相同；edge 剥除浏览器身份、Cookie，替换为服务认证及权威租户。

日志 GET 只允许 accountId 或 keyId 二选一，配合 limit/cursor。秘密只在签发 Key 的响应出现；列表不返回供应商凭据。管理用量权限为 ai.usage.admin，凭据更新为 ai.credentials.rotate，其余配置读写用 ai.providers.read/write。
