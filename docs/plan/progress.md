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
| B7 edge 接入网关 | 完成（#15） | Cursor；会话/CSRF/Host 隔离/身份头/`/open` 骨架；安全审查通过（`auriga-B7-review.md`）；gitleaks+lint 返工后 CI 全绿；E2 接真节点 |
| B6 org | 完成（#16） | Cursor；FORCE RLS、超管上限、邀请幂等、PermissionResolver/hooks、user.*；OrgCoreTest 8/0；CI 全绿；审查 `auriga-B6-review.md`；接线：iam consumeInvitation 传 accountId、合并部署多 DataSource |
| B3 团队账号连接器 | 完成（#17） | Cursor；team-account：邀请/口令+TOTP/Passkey/扫码、`/me/**`；测试 34；审查 `auriga-B3-review.md`；接线：建 Provider、Passkey 联调、composite hooks |
| B4 通用 OIDC 连接器 | 完成（#18） | Cursor；Discovery+码+PKCE、claimMapping、Provider GET/PATCH；43 测；审查 `auriga-B4-review.md`；**生产前补 id_token JWKS 验签** |
| B5 企业连接器 | 等待 | 私有仓，需用户提供公司登录信息 |
| B9 M1 集成 | 完成（#22） | Cursor；platform 合并装配（iam+org 双 schema）、种子应急账号、默认安全配置、NATS 会话吊销接线；两轮独立审查；文档 `docs/dev/m1.md` |
| 补审修复 | 完成（#20、#21） | Cursor 主控期间自审合并的 B3/B4/B7 经独立补审共 19 条问题（含高危），edge 由 Cursor、iam 由 Codex 修复，各两轮复审 |
| 包名整改 | 完成（#19） | Java 根包和 groupId 改为 `auriga`（ADR 0011 修订） |

**M1 完成（2026-10-20）。**

## M2 工作空间（提前开工）

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| A1 节点代理骨架 | 完成（#23） | Cursor 实现，Codex 修复续期竞争等；三轮独立审查；线上 Linux Go 测试打开 `-race` 后又抓到一处真实数据竞争并修复 |
| P0 插件离线版 | 完成（#24） | Cursor 实现，Codex 修复真实网络路径和 IP 字面量绕过；三轮审查，复审用真实网络实测 |
| W1 workspace 服务 | 完成（#26） | Codex 实现（中途额度用完，恢复后续做）；实例、规格代数、调度、生命周期、入网 CA、NodeControl、强制吊销（契约加 `force`）；线上 11 个数据库集成测试全绿；三轮审查 |
| A2 Docker 驱动 | 完成（#25） | Cursor；容器安全参数写死、节点本地策略（白名单、资源上限）、制品校验与 GC、防 YAML/环境变量注入；三轮审查 |
| A3 实例入口与观测 | 完成（#27） | Cursor 实现、Codex 修复；mTLS 只认 edge、身份头失败关闭、文件操作防逃逸（含 Windows 联接）、cron 解析、CPU 增量；两轮审查 |
| P1 插件托管模式 | 完成（#28） | Cursor；三轮审查；真实宿主的交互事件与完整端到端仍待 W3 验证 |
| E2 edge 反代实例 | 完成（#29） | HTTP / WebSocket 经节点 ingress；节点身份取自证书 URI SAN，节点证书含 clientAuth + serverAuth |
| W2 M2 集成基线 | 完成（#30） | Compose + 假 DSH；main c02245e 七组 CI 全绿，E2E M2 于 2026-10-20 17:39（北京时间）通过。真实 DSH 和严格会话吊销验证仍待补齐 |
| W2V 验收补强 | 已备 worktree（Cursor，待发送） | 按源隔离 cookie；以实际 phase/容器/edge 响应验收启停；会话吊销断开已有 WebSocket |
| W3 真实 DSH 冒烟 | 排队 | W2V 后串行；接真实 DSH 与托管插件，验证真实交互、闲置回收和唤醒 |

## M3 AI 与资源

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| C2 M3 契约草案 | 任务已准备，待派 | 只写草案，可与 W2V 并行；优先 G1/R1，正式契约经主控评审后再派实现 |
| G1/G2/R1/N1/X1/F1/P2 | 待契约 | 尚未派实现；开工顺序与验收缺口见 [m3-start.zh.md](m3-start.zh.md) |
