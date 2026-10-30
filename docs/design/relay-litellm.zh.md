# LiteLLM 中转站

公开入口沿用部署配置中的门户与管理员 Origin：外部 OpenAI 客户端 → edge `/ai/v1/{models,chat/completions}` → LiteLLM；团队 DSH → edge `/ai/v1/{models,chat/completions}`?AI ??? → ai-gateway → LiteLLM。Team MCP 与网络通信运行时保持独立。LiteLLM 使用 MIT 核心，不配置 enterprise、SSO 或付费功能。

外部调用只接受 LiteLLM 虚拟 Key，密钥有效性、预算、限流、吊销和模型池由 LiteLLM 管理。团队调用携带 IAM AI 短票；gateway 每次向 IAM 校验设备与原会话，并向 org 校验当前成员及模型调用权限。gateway 用专用 LiteLLM 服务虚拟 Key，覆盖请求 `user` 为 `tenantId:accountId`，覆盖 metadata 为可信 tenant/account/device，禁止调用方伪造归属。LiteLLM 持有唯一用量与预算账本；服务 Key 由管理员签发并限制模型/预算，不使用 master key 代团队调用。

LiteLLM 只加入内部控制网络及模型出站网络，不发布宿主端口；通过现有 PostgreSQL 独立 `litellm` 数据库和 `auriga_litellm` 账号持久化。master key、salt key、数据库口令和团队服务 Key 均来自安装目录秘密文件；salt 固定保存，备份随数据库走。镜像固定为官方 v1.106.6 digest。健康检查使用 `/health/liveliness`，就绪还需数据库检查。

edge 将 `/ai/v1/*` 限定为上述两个数据端点，剥离平台 cookie/身份头；其余 `/ai/*`（UI、登录和管理 API、静态资源）只在管理 Origin、在线有效的 admin 会话下转发，写操作要求同源。LiteLLM 使用 `SERVER_ROOT_PATH=/ai`，edge 去掉 `/ai` 再转发；管理员还需 LiteLLM 自身登录。master key 不注入浏览器。非管理员入口及未知路径默认拒绝。

删除清单：旧 ai-gateway admin_runtime、D7 device_runtime/synthetic、internal/admin、budget、store、relay、relayhttp、relaysupervisor 及仅服务旧账本的 modelgate/gate；旧管理台 models 页面和 modelProvidersApi/modelUsageApi；旧 beta-ui 中转站进程/配置。模型协议和上游 transport 如仅依赖旧 relay 提交对象，随旧运行时删除，由 LiteLLM 承担 OpenAI 转换。contracts 中旧中转站管理/预算声明及 services 中对应 API/配置需随调用点移除；网络相关声明不动。管理台模型入口直接打开 `/ai/ui/`。

验收分三层：gateway/edge 正负鉴权与归属测试；compose/NAS 配置和秘密文件测试；本机独立 Docker project 上真实 LiteLLM + PostgreSQL + 假 OpenAI 上游，验证 healthy、两种调用、无/错/吊销 Key、预算拒绝及成员用量。假服务测试不替代最后一层。
