# 进度

> 只记任务编号、状态和结论。计划见 [roadmap.zh.md](roadmap.zh.md)。

## M0 立项

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| P1–P6 开工准备 | 完成 | 工具链 JDK 25.0.4、Maven 3.9.16、Go 1.27；公开仓库 + 私有仓库；脱敏文档和 15 篇 ADR 入库；开发环境探测：依赖可在线下载，构建缓存放共享目录 |
| C1 契约第一版 | 完成 | OpenAPI（iam、org、workspace、远程连接器）、protobuf（入网、NodeControl、iam / org 内部接口）、事件、身份源步骤协议、插件协议、令牌和 KV；buf lint、redocly lint、Ajv 编译通过；Go / Java / TS 类型生成并编译通过；Opus 安全评审 25 条已全部处理（实例改用独立子域，ADR 0020） |
| K1 仓库骨架 | 完成（Codex，拆成 K1a、K1b） | K1a：Maven 22 个模块、契约代码生成、ArchUnit + Enforcer（违规依赖构建失败已验证）、嵌入式 PostgreSQL 测试基座、Spotless、gitleaks、CLA 草稿和检查；K1b：go.work 6 个模块、ctl dev（嵌入式 PostgreSQL 15433 + NATS 14222）、4 个 Vue 包、插件骨架、CI |
| K2 common 公共库 | 完成（#11） | 测试 7→128；独立审查 2 高 3 中 7 低已修；契约补内部服务令牌（6d1cfe3） |
| S1 DSH mcp-client | 完成（Codex） | 原生 mcp-client 支持 Streamable HTTP 和自定义请求头，但不透传 DSH 会话；会话相关的团队工具由插件注册后转发（ADR 0016）。原型在本地分支 spike/s1-mcp-client |
| S2 面板 Web Component | 完成（Sonnet） | 托管和连接两种模式都在真实 DSH 0.2.0-rc.2 里挂载成功，样式隔离自检 6/6；连接模式由插件验签后经本机路由提供，设备令牌不进页面（ADR 0017）。原型在本地分支 spike/s2-panel |
| S3 Windows 原生隔离 | 暂停（M4 再继续） | 第二轮修好 WFP 清理、登录权限、配额；第三轮（审查意见）shim 改 SYSTEM 并通过存活实验，但负载启动回归、清理时进程未结束；第四轮修进程数 / AppContainer / 内存，第五轮修回归（任务说明已写好） |
| S4 DSH 运行时制品 | 完成（Codex） | Node + DSH + 锁定依赖打成 tar.gz（约 180 MB 压缩）；Windows / Linux 断网、只读目录启动通过；可在 Windows 交叉打 Linux 包且可复现（ADR 0019）。原型在 spike/s4-dsh-runtime 分支 |

## M1 平台底座

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| B2 iam 核心 | 完成（#13） | Codex 实现；CI 全绿；Cursor 主控只读安全审查通过（`.agents/runs/auriga-B2-review.md`），无阻断项；接线留给 B6/B7/安装器 |
| B8 门户/管理台壳 | 完成（#12） | Cursor；shared API+步骤渲染+mock；portal/admin 登录与框架；第二轮去掉本地 alias、错误码对齐契约 |
| B1 ctl 本机内置 | 完成（#14） | Cursor；`dev/start/stop/status`；每服务 schema/角色；文档 `docs/dev/ctl.md`；lint errcheck 返工后 CI 全绿 |
| B7 edge / B6 org | 待派 | 按序给 Cursor；任务说明未写 |
| B3 / B4 | 等待 | 依赖 B2，现已可派 |
| B5 企业连接器 | 等待 | 私有仓，需用户提供公司登录信息 |
| B9 M1 集成 | 等待 | B1–B8 齐后做 |
