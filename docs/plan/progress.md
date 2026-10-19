# 进度

> 只记任务编号、状态和结论。计划见 [roadmap.zh.md](roadmap.zh.md)。

## M0 立项

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| P1–P6 开工准备 | 完成 | 工具链 JDK 25.0.4、Maven 3.9.16、Go 1.27；公开仓库 + 私有仓库；脱敏文档和 15 篇 ADR 入库；开发环境探测：依赖可在线下载，构建缓存放共享目录 |
| C1 契约第一版 | 完成 | OpenAPI（iam、org、workspace、远程连接器）、protobuf（入网、NodeControl、iam / org 内部接口）、事件、身份源步骤协议、插件协议、令牌和 KV；buf lint、redocly lint、Ajv 编译通过；Go / Java / TS 类型生成并编译通过；Opus 安全评审 25 条已全部处理（实例改用独立子域，ADR 0020） |
| K1 仓库骨架 | 完成（Codex，拆成 K1a、K1b） | K1a：Maven 22 个模块、契约代码生成、ArchUnit + Enforcer（违规依赖构建失败已验证）、嵌入式 PostgreSQL 测试基座、Spotless、gitleaks、CLA 草稿和检查；K1b：go.work 6 个模块、ctl dev（嵌入式 PostgreSQL 15433 + NATS 14222）、4 个 Vue 包、插件骨架、CI |
| K2 common 公共库 | 未开始 | |
| S1 DSH mcp-client | 完成（Codex） | 原生 mcp-client 支持 Streamable HTTP 和自定义请求头，但不透传 DSH 会话；会话相关的团队工具由插件注册后转发（ADR 0016）。原型在本地分支 spike/s1-mcp-client |
| S2 面板 Web Component | 完成（Sonnet） | 托管和连接两种模式都在真实 DSH 0.2.0-rc.2 里挂载成功，样式隔离自检 6/6；连接模式由插件验签后经本机路由提供，设备令牌不进页面（ADR 0017）。原型在本地分支 spike/s2-panel |
| S3 Windows 原生隔离 | 进行中（Codex 返工） | 第一轮 CI：账号、Job 内存 / CPU、进程树、垫片接管、WFP 按 SID 放行 / 拒绝、ACL 通过；审查发现 Job 可被实例撤销、根目录联接点等设计问题，正在返工 |
| S4 DSH 运行时制品 | 完成（Codex） | Node + DSH + 锁定依赖打成 tar.gz（约 180 MB 压缩）；Windows / Linux 断网、只读目录启动通过；可在 Windows 交叉打 Linux 包且可复现（ADR 0019）。原型在 spike/s4-dsh-runtime 分支 |
