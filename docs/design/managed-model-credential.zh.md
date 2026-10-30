# 托管 DSH 使用平台中转站

IAM 在托管设备谱系中签发独立模型 Key，绑定 tenant、成员、实例、设备及当前生命周期代次。
它仅用于模型调用，不能换网络票、登录或控制实例；最长30天，每次请求在线核权。
停止、删除、移出成员、吊销和重启轮换沿用托管设备的即时失效路径。Key 格式及错误边界见
[契约](../../contracts/auth/managed-model.md)。同机工具可读文件的隔离边界与托管网络凭据相同。

workspace 使用签发的成员模型 Key 请求平台 `/ai/v1/models`，只接受成功、非空、有界且无重复的池模型列表。
地址从可信安装材料 `modelBaseUrl` 取得，缺省为 `platformOrigin + /ai/v1`；不接受实例 Profile 提供模型地址或凭据。
HTTP 部署保留支持；HTTPS 验证走 JVM 信任库，不跳过证书检查。安装方可令该地址指向实例可达的平台入口，不能指向外部提供商。
安装时一次校验入口；缺失、非法或含查询/身份信息时模型能力进入 `disabled_invalid_installation`，一次明确告警，不按实例反复重试或刷日志。
禁用后的协调会清除已有加密模型材料、更新规格并沿受管材料重启路径移除实例内的旧文件；重复协调不增加规格代次。
安装有效时，临时池发现失败仅延迟模型材料，网络启动继续；每30秒按已有协调周期重试，日志只含实例 id。

独立的 `workspace.instance.managed_model_credential` 保存加密 JSON；每次规格生成重新以节点公钥和规格代次封装。
agent 解封后核对 tenant、成员、实例及材料结构，写 `/run/auriga/model-credential.json`（0600，随元数据只读挂载）。
renderer 生成 DSH 公开 `llm-pi-ai` 行的 `auriga-managed` OpenAI Chat Completions 提供方，设置 `compat.supportsStore:false`，中转站仍明确拒绝 `store` 字段。
配置使用 `!!js` 文件表达式生成 Authorization 头，Loader 求值时读取唯一的 `/run/auriga/model-credential.json`；公开 YAML 和环境变量不包含 Key。旧版 `model-keys.json` 副本会被删除。
渲染层不覆盖原生 `credentials` 行或其存储位置；用户已有个人 Key、`credentials/set` 和 OAuth record 更新继续使用用户可写的原生凭据文件。
`llm-pi-ai` 与凭据路径不能通过实例 Profile patch 覆盖；任意 `Profile.model_providers` 仍拒绝。缺材料删除私有模型文件，Key 改变影响渲染摘要并走受管材料重启。
托管实例使用平台专用的隔离 profile，要求标准基线的 `llm-pi-ai` 行；渲染层管理该行的提供方配置。不将本层补丁应用到用户桌面或已有个人 profile，不承诺合并个人提供方；这不影响云端原生凭据存储的读写。
**已知并接受**：仅实例主人经 `settings/describe` 可见 `llm-pi-ai` 命名空间的 `value.providers.auriga-managed.headers.Authorization`，属 MANAGED-N1 同机边界，跟踪 backlog **B-MM-DESCRIBE**。探针仅对此精确位置给出警告；其他任何位置出现 Key 仍判失败，profile 仍不得保存 Key 明文。
成员在 DSH 原生模型选择器选择池中模型；已有会话的模型选择不被改写。池列表在首次下发和实例重启时读取，运行时池变更的调用权限由 LiteLLM 实时执行。

ai-gateway 通过既有受认证 IAM gRPC 每请求验证模型 Key，继续核验当前成员及模型权限，再用现有专用服务 Key 调 LiteLLM。IAM仍只签最多300秒租约，网关上限检查接受5秒跨主机时钟偏差，过期、身份及在线核权检查不变。
`user=tenantId:accountId` 和可信 metadata 的覆盖规则与桌面一致；调用方提供的 user/metadata 不影响成员用量归属。
模型列表、预算、速率和吊销仍由 LiteLLM 负责，不增加第二套模型池或管理员 Key。

部署变化：更新 platform（IAM/workspace，自动执行 workspace V7 迁移）和 ai-relay、agent 镜像；DSH 镜像需已包含基线的 `@deepseek-ai/dsh-llm-pi-ai`。
无需更新用户桌面 profile、外部模型 Key 或安装新插件依赖。安装材料若指定 `modelBaseUrl`，workspace 和实例必须都能访问该入口并信任其证书。
共享栈真实对话、LiteLLM 成员账单及 NAS 验收须在部署这三项后由授权执行槽验证；组件单测不替代该证据。
