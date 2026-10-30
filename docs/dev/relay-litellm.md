# 中转站部署与模型接入

中转站随公开 compose / NAS 栈部署。使用 LiteLLM Proxy MIT 核心，模型、池、虚拟 Key、预算和用量均在 LiteLLM 管理；Auriga 管理台“模型”入口打开同一管理员 Origin 的 `/ai/ui/`。网络通信配置独立，不将 LiteLLM Key 用作 Auriga 登录凭据。

## 部署

使用全新的安装目录和 PostgreSQL 数据卷。compose 按 [公开入口部署](compose-access.md)运行 prepare 和 up；NAS 按 [NAS 部署](nas.md)完成镜像准备、init、render、start。旧栈的 AI schema、Key、余额不迁移；不要删除现有盐值或重建其文件来修复启动错误。

LiteLLM 固定为 `ghcr.io/berriai/litellm:v1.106.6@sha256:37e2dafb088a7723beb998590682957386dfc83ffea1848486f8e0e5cfac0b06`。该版本及 digest 来自[官方镜像目录](https://github.com/berriai/litellm/pkgs/container/litellm/versions?filters%5Bversion_type%5D=tagged)，可按[官方发行说明](https://github.com/BerriAI/litellm/releases/tag/v1.106.6)验证签名。NAS 的 start 不下载镜像，需预先在目标 Engine 准备此精确 digest（可在构建机拉取后 save/load），并通过 preflight；原三份 Auriga 制品镜像仍用现有校验流程。

安装器生成四个独立随机秘密：`litellm/master`、`salt`、`team`、`pg_password`，位于 compose `var` 或 NAS `config/materials` 下。不要提交或打印文件内容。数据库为现有 PostgreSQL 内的独立 `litellm` 库，角色 `auriga_litellm` 不能建库或建角色。首次启动的 `litellm-init` 只签发一个 `llm_api` 类型服务虚拟 Key；其完成标记保存在 `litellm-state` 卷。重启不自动恢复被吊销的服务 Key。备份 PostgreSQL、盐值、秘密目录及该状态卷；显式轮换服务 Key 时同时更新 gateway 挂载的文件，不能用 master key 替代它。

LiteLLM 不发布宿主端口。内部健康入口为 `http://litellm:4000/health/liveliness`，compose 健康检查另读 `/health/readiness`；`litellm-init` 正常退出后 ai-relay 才启动。若数据库已有旧卷但没有 LiteLLM 库，首次初始化 hook 不会重新运行，应使用新部署目录验收。

## 接入模型与组池

1. 登录 Auriga 管理台，打开“模型”，进入 `/ai/ui/`；LiteLLM 仍要求其自身管理员登录。由部署管理员安全取用 master 文件作为 LiteLLM 初始管理员凭据，不通过聊天、URL 或剪贴板日志传递。
2. 在 Models 添加提供商，填写供应商模型、API Base、供应商 Key 和公开模型名。至少添加两个提供商；需要组成同一池时为多个 deployment 设置同一公开模型名，配置 LiteLLM 自带路由、限流和故障切换。
3. 用 Playground 验证每个 deployment，再选择池的公开模型名验证调用。费用由 LiteLLM 模型价格表或管理员设置的自定义单价计算；自定义模型要核实价格，不能将未知费用当作预算已生效。
4. 团队 DSH 保持 Auriga 登录和 AI 短票，调用原 `/ai/v1/models`、`/ai/v1/chat/completions`；edge 按凭据类型选择 gateway 或 LiteLLM。gateway 每次校验成员身份，使用服务虚拟 Key，并覆盖 `user=tenantId:accountId` 与可信 metadata。在 LiteLLM 的 End Users / Spend Logs 按该标识及 metadata 查询成员用量；成员预算与服务 Key 总预算在 LiteLLM 配置。

## 给外部调用方发 Key

1. 在 LiteLLM Virtual Keys 创建 Key，设置可用模型/池、预算、预算周期、RPM/TPM、到期时间与别名。将虚拟 Key 安全交付调用方；不交付 master、salt 或团队服务 Key。
2. 客户端 OpenAI base URL 设为门户 Origin 加 `/ai/v1`，Bearer 使用该虚拟 Key，model 使用池的公开模型名。公开数据入口只开放 models 与 chat/completions。管理 UI、登录、Key 管理和静态资源只允许管理员 Origin 且必须有在线有效的 Auriga admin 会话。
3. 用本地假 OpenAI 上游依次验证 models、普通 chat 和流式 chat；再验证无 Key、错误 Key、吊销 Key 和超预算 Key 均拒绝。检查 LiteLLM Spend Logs，再吊销测试 Key。不调用付费模型代替验收。

参考：[虚拟 Key](https://docs.litellm.ai/docs/proxy/virtual_keys)、[用量归属](https://docs.litellm.ai/docs/proxy/cost_tracking)。

## 原生 Beta 调试

Beta 不再创建旧 AI 账本或启动 admin runtime。先准备独立的 LiteLLM 测试实例，使用数字回环 HTTP Origin 或有效 HTTPS，并安全保存仅用于该实例的服务虚拟 Key 文件。停止 Beta 后执行：

```powershell
node scripts/beta-ui/runner.mjs litellm --state D:\auriga-beta-example --origin http://127.0.0.1:4000 --team-key-file D:\auriga-beta-example\litellm-team.key
```

然后沿 Beta 原流程启动。命令只保存 Origin 与秘密文件路径，不取用生产密钥；未配置 LiteLLM 时 Beta 不提供中转站。原生 Beta 的端口发布仅用于本机调试，公开栈无需开放 4000。

????? LiteLLM ?????`node deploy/nas/litellm/verify.mjs`???????? Docker context ????????? project ????????????????????? project????????????? LiteLLM ?????? Auriga ??? gateway ?????
