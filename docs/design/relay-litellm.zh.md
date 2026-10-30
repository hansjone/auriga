# LiteLLM 中转站

外部 OpenAI 客户端 → 门户 edge `/ai/v1/{models,chat/completions}` → LiteLLM；团队 DSH → 同一入口（IAM AI 短票）→ ai-gateway → LiteLLM。Team MCP 与网络通信运行时保持独立。LiteLLM 使用固定 digest 的 MIT 核心，不配置 enterprise、SSO 或付费功能。

外部虚拟 Key 的模型池、预算、限流和吊销由 LiteLLM 管理。gateway 每次在线校验 IAM 设备、原会话及当前成员权限，使用专用服务虚拟 Key，覆盖 `user=tenantId:accountId` 和可信 tenant/account/device metadata。Auriga 不再提供成员模型配额，改用 LiteLLM Customers / End Users 预算与限速。首次开放模型前必须收紧服务 Key 的模型、总预算和限速，不使用 master key 代团队调用。

LiteLLM 只加入内部控制网络及模型出站网络，不发布其 4000 端口；通过现有 PostgreSQL 独立 `litellm` 库和 `auriga_litellm` 角色持久化。初始化先提交 `auriga_provisioning.service_key_state` claim，再创建服务 Key；已有 ready 标记绝不自动重建已吊销 Key，中断需人工恢复。数据库、固定 salt 和四份秘密文件一起备份，不依赖命名状态卷。镜像固定为官方 v1.106.6 digest；健康检查含存活与数据库就绪。

UI 使用第四个独立 edge Origin，默认管理员 scheme/host 的 8086 端口，域名模式可配独立子域。管理台 `/ai/ui/` 校验在线 admin 会话与 `ai.providers.write`，通过限时单次 POST code 交接；code 不进 URL。UI 使用独立 HttpOnly host-only cookie，每次在线检查权限，只代理 `/ai/*`，不提供 Auriga API。配置、证书 SAN 与安装 receipt 绑定 UI Origin。LiteLLM 自身仍需登录，master key 不注入页面。

数据仅开放两个端点。UI 和数据响应均剥离 `Access-Control-*`。只向 LiteLLM UI 转发 `token`、`litellm_cp_return_to`、`litellm_oauth_state` 三个 cookie，所有 Auriga cookie 均剥离；响应 cookie 同样限白名单、host-only 和 `/ai`。部署使用 `SERVER_ROOT_PATH=/ai`，edge 保留此前缀，gateway 走根数据接口。

删除清单：旧 admin/device runtime、internal/admin/budget/store/relay/relayhttp/relaysupervisor、modelgate/gate；旧管理台中转站页面与模型配额设置；旧 Beta 账本和进程。org 的 ModelQuota/Quota.model 仅保留 deprecated 协议声明以保持兼容，服务拒绝写入且不读取。旧 AI 管理契约及 me/usage 路由尚待单独清理，不能作为可用 API；网络声明保持独立。

验收分三层：gateway/edge 正负鉴权与归属测试；compose/NAS 配置和秘密文件测试；本机独立 Docker project 上真实 LiteLLM + PostgreSQL + 假 OpenAI 上游，验证 healthy、两种调用、无/错/吊销 Key、预算拒绝及成员用量。假服务测试不替代最后一层。
