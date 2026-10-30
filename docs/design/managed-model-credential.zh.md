# 托管 DSH 使用平台中转站

IAM 在托管设备谱系中签发独立模型 Key，绑定 tenant、成员、实例、设备及当前生命周期代次。
它仅用于模型调用，不能换网络票、登录或控制实例；最长30天，每次请求在线核权。
停止、删除、移出成员、吊销和重启轮换沿用托管设备的即时失效路径。Key 格式及错误边界见
[契约](../../contracts/auth/managed-model.md)。同机工具可读文件的隔离边界与托管网络凭据相同。

workspace 使用签发的成员模型 Key 请求平台 `/ai/v1/models`，只接受成功、非空、有界且无重复的池模型列表。
地址从可信安装材料 `modelBaseUrl` 取得，缺省为 `platformOrigin + /ai/v1`；不接受实例 Profile 提供模型地址或凭据。
HTTP 部署保留支持；HTTPS 验证走 JVM 信任库，不跳过证书检查。安装方可令该地址指向实例可达的平台入口，不能指向外部提供商。
安装时一次校验入口；缺失、非法或含查询/身份信息时模型能力进入 `disabled_invalid_installation`，一次明确告警，不按实例反复重试或刷日志。
安装有效时，临时池发现失败仅延迟模型材料，网络启动继续；每30秒按已有协调周期重试，日志只含实例 id。

独立的 `workspace.instance.managed_model_credential` 保存加密 JSON；每次规格生成重新以节点公钥和规格代次封装。
agent 解封后核对 tenant、成员、实例及材料结构，写 `/run/auriga/model-credential.json`（0600，随元数据只读挂载）。
renderer 生成 DSH 公开 `llm-pi-ai` 行的 `auriga-managed` OpenAI Chat Completions 提供方，设置 `compat.supportsStore:false`，中转站仍明确拒绝 `store` 字段。
配置只含 `apiKeyEnv: AURIGA_MANAGED_MODEL_KEY` 引用，DSH 凭据服务从独立0600的 `/run/auriga/model-keys.json` 原生凭据文档取 Key，不使用明文 Authorization 配置或环境变量。
`llm-pi-ai` 与 `credentials` 行不能通过 Profile patch 覆盖；任意 `Profile.model_providers` 仍拒绝。缺材料删除两份私有文件，Key 改变影响渲染摘要并走受管材料重启。
托管实例使用平台专用的隔离 profile，要求标准基线的 `llm-pi-ai` 与 `credentials` 行；渲染层拥有这两行完整配置。不将本层补丁应用到用户桌面或个人自配 profile，不承诺把个人提供方合并到平台 profile。个人 DSH 仍按桌面公开流程配置。
成员在 DSH 原生模型选择器选择池中模型；已有会话的模型选择不被改写。池列表在首次下发和实例重启时读取，运行时池变更的调用权限由 LiteLLM 实时执行。

ai-gateway 通过既有受认证 IAM gRPC 每请求验证模型 Key，继续核验当前成员及模型权限，再用现有专用服务 Key 调 LiteLLM。IAM仍只签最多300秒租约，网关上限检查接受5秒跨主机时钟偏差，过期、身份及在线核权检查不变。
`user=tenantId:accountId` 和可信 metadata 的覆盖规则与桌面一致；调用方提供的 user/metadata 不影响成员用量归属。
模型列表、预算、速率和吊销仍由 LiteLLM 负责，不增加第二套模型池或管理员 Key。

部署变化：更新 platform（IAM/workspace，自动执行 workspace V7 迁移）和 ai-relay、agent 镜像；DSH 镜像需已包含基线的 `@deepseek-ai/dsh-llm-pi-ai`。
无需更新用户桌面 profile、外部模型 Key 或安装新插件依赖。安装材料若指定 `modelBaseUrl`，workspace 和实例必须都能访问该入口并信任其证书。
共享栈真实对话、LiteLLM 成员账单及 NAS 验收须在部署这三项后由授权执行槽验证；组件单测不替代该证据。
