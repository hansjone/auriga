# 架构决策记录（ADR）

每篇记录一个已定的决定：背景、决定、后果。新决定追加编号，推翻旧决定时写新 ADR 并把旧的标成"已取代"。

| 编号 | 标题 | 状态 |
|---|---|---|
| [0001](0001-rewrite-as-platform.md) | 重写为可商业化的微服务平台 | 已采纳 |
| [0002](0002-tech-stack.md) | 技术栈：Java 业务服务、Go 基础设施、Vue 前端 | 已采纳 |
| [0003](0003-modular-deployment.md) | 服务按模块拆分，部署时可合并 | 已采纳 |
| [0004](0004-platform-controls-dsh.md) | 平台控制 DSH 实例：workspace 决策、节点代理执行 | 已采纳 |
| [0005](0005-dsh-plugin-modes.md) | DSH 插件三种模式、独立发布 | 已采纳 |
| [0006](0006-repositories.md) | 一个公开主仓库 + 一个私有仓库 | 已采纳 |
| [0007](0007-postgresql.md) | PostgreSQL：内置或外置，每服务一个 schema | 已采纳 |
| [0008](0008-nats.md) | NATS JetStream 做事件总线和缓存 | 已采纳 |
| [0009](0009-licensing.md) | 许可证：平台 AGPL-3.0 + 商业双授权，插件 MIT | 已采纳 |
| [0010](0010-windows-native-isolation.md) | Windows 上用原生进程运行实例，隔离弱于容器 | 已采纳 |
| [0011](0011-java-package.md) | Java 包名和 Maven groupId 用 io.github.hansjone.auriga | 已采纳 |
| [0012](0012-identity-sources.md) | 身份源通用架构：平台核心 + 连接器 + 步骤协议 | 已采纳 |
| [0013](0013-external-systems.md) | 外部系统不并入平台，经 API 打通 | 已采纳 |
| [0014](0014-communication-and-tokens.md) | 服务间通信和身份令牌 | 已采纳 |
| [0015](0015-contracts-first.md) | 契约优先 | 已采纳 |
| [0020](0020-instance-origin-isolation.md) | 实例使用独立的源 | 已采纳 |
| [0016](0016-mcp-session-context.md) | MCP 鉴权与 DSH 会话上下文（S1） | 已采纳 |
| [0017](0017-team-panel-loading.md) | 团队面板的加载方式（S2） | 已采纳 |
