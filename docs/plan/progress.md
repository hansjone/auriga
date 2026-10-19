# 进度

> 只记任务编号、状态和结论。计划见 [roadmap.zh.md](roadmap.zh.md)。

## M0 立项

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| P1–P6 开工准备 | 完成 | 工具链 JDK 25.0.4、Maven 3.9.16、Go 1.27；公开仓库 + 私有仓库；脱敏文档和 15 篇 ADR 入库；开发环境探测：依赖可在线下载，构建缓存放共享目录 |
| C1 契约第一版 | 完成 | OpenAPI（iam、org、workspace、远程连接器）、protobuf（入网、NodeControl、iam / org 内部接口）、事件、身份源步骤协议、插件协议、令牌和 KV；buf lint、redocly lint、Ajv 编译通过；Go / Java / TS 类型生成并编译通过；Opus 安全评审 25 条已全部处理（实例改用独立子域，ADR 0020） |
| K1 仓库骨架 | 未开始 | |
| K2 common 公共库 | 未开始 | |
| S1 DSH mcp-client | 未开始 | |
| S2 面板 Web Component | 未开始 | |
| S3 Windows 原生隔离 | 未开始 | 在 GitHub Actions 的 Windows 虚拟机里跑，不动开发机 |
| S4 DSH 运行时制品 | 未开始 | |
