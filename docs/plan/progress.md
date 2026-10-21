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
| W2V / W2Vb 验收补强 | #31 已合并，返工 PR #42 CI 全绿、独审通过 | 补齐实际停止的 phase/容器/确切 edge 503 三证、预热与一次性计划消费、吊销前原连接探活、A/B 会话对照和深层日志脱敏；真实 DSH 整轮证据另由 W3R 收集 |
| W3 真实 DSH 冒烟 | 已合并 #37 | 配置表达式绕过与活动未知误判经三轮独审修复；固定版本真实页面、健康和门锁已实测。完整生命周期由后续 W3V/HB1/W3R 补齐 |
| W2H 底座加固 | 已合并 #36 | Host、socket、种子与闹钟预算加固；临近编辑保护受固定硬上限约束；真实 PG 回归、双平台 CI 及三轮独审通过后，按用户授权合并 |
| W3V 实例网络与就绪 | PR #40 最终 CI 全绿、第 2 轮独审通过 | 受信 edge 内部入口、实际健康就绪及停止计划观察已实现。补修代理重启端点、待重启旧代和实际监听端口恢复，以及快速重启 CPU 窗口；四包 Windows/原生 Linux 回归通过，本机未改 ingress 用例 TLS 失败留证，最终双平台 CI 和新会话复审通过 |
| HB1 / TERM1 真实心跳 | PR #41 CI 全绿、第 2 轮独审通过 | 计数允许显式未知，缺失/非法值拒绝；未知或不健康保守阻止回收且不伪造用户活动。补采真实浏览器与模型终端来源；本机真实原始心跳的四项零计数、双静默窗口和 HTTP 交互推进已通过，未实际创建 PTY |
| W3R 真实生命周期 | 最终集成 PR #43，CI/独审/真实终验通过，待合并 | 精确源码 f6b5d90 重建全部镜像；原始心跳双静默/交互、闲置回收、预热/到期消费、双会话 HTTP/原 WS 吊销均实测并独立核证通过。额外实际代理重启保留原 DSH 容器/启动时间，入口约 1.3 秒恢复、持续稳定并恢复计划观察；实际镜像与构建导出一致。保留先前失败和适用范围，未自行合并安全关键新改动 |
| NASHB NAS 内部入口 | PR #44 CI 全绿、独审通过 | 补齐 edge 稳定名字/角色与 agent 内部心跳 URL、实例父域；默认及定制域名配置、原生 Linux 脚本及篡改负向验证通过，仅本机源码准备，未部署 NAS |
| D0 NAS 部署包 | 已合并 #35、#39 | 四轮审查后合并准备包；专用打包发现 Linux 执行位依赖，补丁经独立审查及 CI 全绿后合并。固定提交的专用构建与上传通过，实际工件 SHA、save/load、标记、镜像 ID 和原生 0644 脚本/CI 二进制均验证通过；完整现场包须在 W2H/W3 整合后重建，未部署 NAS |
| A2F 制品与 GC | 已合并 #34 | 独立审查通过后合并；遗留以审查报告和主控手册为准 |
| CF1 / RL1 | 已合并 #32 / #38 | common 偶发测试与 Windows renderer lint 修复，经独立审查 |

## M3 AI 与资源

| 编号 | 状态 | 结论 / 备注 |
|---|---|---|
| C2 M3 契约草案 | 已合并 #33 | 三轮独立审查；D1–D7 用户决定已定，正式契约按草案落地 |
| C3 正式契约 | 任务与 worktree 已准备，主体尚无交付 | G1/R1 与实例 AI 令牌仍待正式契约；未知活动心跳子契约已由 HB1 单独实现并送审，不等于完整 C3 |
| G1/G2/R1/N1/X1/F1/P2 | 待契约 | 尚未派实现；开工顺序与验收缺口见 [m3-start.zh.md](m3-start.zh.md) |
