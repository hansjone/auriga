# 0016 MCP 鉴权与 DSH 会话上下文

- 状态：已采纳
- 日期：2026-10-19
- 验证版本：DSH 0.2.0-rc.2；仅技术验证，原型不进入主分支

## 背景

ADR 0005 将团队工具交给 ai-gateway 的 MCP；架构第六节同时允许需要 DSH 会话上下文的工具由插件注册并转发。团队任务需要区分同一实例中的并发会话，不能让模型自己声明当前会话，也不能把 MCP 传输会话当作 DSH 会话。

S1 读取指定 DSH 源码，并用本地安装的同版本发布包、官方 MCP SDK 服务器、假 OpenAI 模型完成端到端验证。证据为 `spikes/s1-mcp-client/results.json`，复现方式见该目录 README。

## 决定

1. 不依赖 DSH 会话的团队工具，继续由 ai-gateway 的 `/ai/mcp` 通过原生 mcp-client 提供，使用 `transport: streamable-http`。旧式 SSE 不是本版本支持的传输。
2. 需要当前会话 / 团队任务绑定的工具，由 `auriga-dsh` 在 DSH 的 `ctx.tools` 注册，执行时读取 `exec.agent.session.id`，使用插件自有 MCP 客户端转发给同一个 ai-gateway。业务实现仍在平台；插件只负责进程内上下文与凭据。平台根据已鉴权实例和会话绑定查询任务，不能从工具参数直接相信任意任务 ID。
3. 托管模式统一使用 `Authorization: Bearer <实例令牌>`。原生客户端支持 `headers`，可用 Loader 的 `!!js` 从环境变量或文件读取；其值在配置求值时固定。令牌轮换需触发插件配置重新求值并重建连接，或重启实例，并设计旧新令牌短暂重叠。插件转发采用每请求读取令牌文件，避免依赖实例重启。连接模式的设备令牌刷新另按现有设备协议实施。
4. 提议转发使用 `params._meta['auriga/sessionId']`、`['auriga/toolCallId']`、`['auriga/rootCallId']`。这些值由插件在执行时构造，不由模型填入；缺会话上下文则拒绝执行。平台仍以令牌确定实例、租户和主人，并校验会话与任务绑定。任务 ID、轮次 ID 不能用 MCP Session ID 或 tool-call ID 冒充。
5. 不为每个 DSH 会话建立原生 MCP 连接，不把当前会话 ID 写入共享客户端请求头。共享连接可以承载并发调用，所有业务上下文必须放在各自请求中。
6. 401 视为鉴权失败，工具错误传给模型及会话事件；平台与插件显式管理凭据刷新和重连，不能假设原生客户端会刷新实例令牌。业务写工具不依赖 SDK 自动重放，幂等与重试策略另入契约。

## 依据

下列路径相对任务指定的只读 `DeepSeekHarness` 源码根目录（HEAD `639ed015397290b3745d163aafe02ffee4aa3f84`）：

- `packages/mcp/mcp-client/src/index.ts:119`：配置只接受 stdio 与 Streamable HTTP；HTTP 有 `headers`。
- `packages/mcp/mcp-client/src/transport.ts:31`：每个配置创建传输，HTTP 只设置静态 `requestInit.headers`。
- `packages/mcp/mcp-client/src/tools.ts:138`：`callTool` 只发送名称与参数，执行上下文仅用于 signal / timeout，没有 DSH 会话 `_meta` 注入。
- `packages/mcp/mcp-client/src/index.ts:181`、`packages/mcp/mcp-client/src/connection.ts:258`：一个插件实例持有一条受监督的客户端连接；不是每个 DSH 会话一个。Agent 作用域可通过显式组合另行配置，不是原生默认行为。
- `packages/core/tools/src/index.ts:153`、`:164`、`:327`：有工具执行钩子和含 Agent 的执行上下文，但原生 MCP 桥没有向传输注入 `_meta` 的配置或钩子；通用 around 钩子只允许改变 signal，无法配置原生请求身份。
- `packages/core/agent/src/runtime-types.ts:168`、`packages/core/session/src/index.ts:470`：执行方可读取真实 Session 的 ID。原型已在两个会话中证明此接口可用。
- `vendor/loader/src/index.ts:104`、`vendor/loader/src/config/utils.ts:12`：JS 表达式由 Loader 在配置求值时执行，并非每次网络请求执行。
- `packages/mcp/mcp-client/src/connection.ts:211`：关闭 / 协商失败使用有上限的指数退避；预算耗尽后注销工具，需重载或重启。

实测两个原生会话的请求均有自定义 Bearer 头、无会话 `_meta`，共用一个 `Mcp-Session-Id`，服务端可重叠处理。插件转发请求携带各自 DSH 会话与工具调用 ID，并在文件轮换后使用新令牌。启动 401 重试预算和请求错误传播也有端到端断言。

## 后果

需要主控更新插件协议与实例规格的渲染约定：固定 Bearer 头、令牌文件 / 重载语义、上下文元数据格式、工具路由和会话任务绑定规则。DSH Session ID 是不透明字符串，不按平台 UUIDv7 校验。现有契约未在 S1 中修改。

通用钩子可用于观测和策略；若今后 DSH 提供正式 MCP 请求上下文注入扩展，可以重新评估是否消除插件转发。原型没有生产级工具清单同步、幂等或完整连接恢复；请求 socket 中断的恢复结果也不能推导成长时间断网和服务器丢失会话后的保证。UI 只验证事件与渲染源码，未做视觉验收。
