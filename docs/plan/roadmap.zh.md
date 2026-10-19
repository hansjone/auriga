# 路线图（v1，2026-10-19）

> 架构见 [platform.zh.md](../architecture/platform.zh.md)。任务编号前缀：S 技术验证、C 契约、K 骨架与公共库、B 底座、W 工作空间、A 节点代理、E 接入网关、P 插件、G AI 网关、R 资源、N 知识、X 出网、F 文件、L 协作与运维、D 交付、V 商业化。
> 进度见 [progress.md](progress.md)。

## M0 立项：仓库、契约、骨架、技术验证

| 编号 | 任务 | 依赖 | 验收要点 |
|---|---|---|---|
| C1 | 契约第一版：`contracts/openapi`（iam、org、workspace 对外接口）、`contracts/proto`（NodeControl、iam / org 内部接口）、`contracts/events`（user.*、instance.*、identity.*）、身份源步骤协议 JSON Schema、插件协议（身份头格式和 kid、心跳、设备登录、面板加载） | — | 能生成 Java / Go / TS 类型 |
| K1 | 仓库骨架：Maven 多模块、go.work、pnpm workspace、`clients/dsh-plugin`；ArchUnit；格式化和 lint；gitleaks；CLA 检查；GitHub Actions；嵌入式 PostgreSQL 测试基座；`ctl dev` | — | CI 全绿；违反"只能依赖 api"时构建失败 |
| K2 | common 公共库：租户上下文 + 租户插件 + RLS；outbox + NATS 发布 / 消费 + 去重；审计事件接口；错误码；JWT 资源服务器；gRPC 配置；指标和日志 | K1、C1 | 每项有测试；示例服务能发、收事件 |
| S1 | 验证 DSH mcp-client：Streamable HTTP + 自定义请求头；工具调用能否拿到 DSH 会话 id | — | 结论写进 ADR |
| S2 | 验证团队面板 Web Component：托管模式（同源）和连接模式（跨源加载签名包）都能挂载、样式隔离 | — | 两种模式的原型截图 |
| S3 | 验证 Windows 原生隔离：本地账号 + `CreateProcessAsUser`、Job Object、WFP 按 SID 拦出站、NTFS 配额；AppContainer 里能否跑 Node | — | 结论写进 ADR |
| S4 | 验证 DSH 运行时制品：Node + 指定版本 DSH 打成离线包，Windows / Linux 各一份，断网能启动 | — | 打包脚本 + 断网启动记录 |

## M1 底座：能登录、能管理

| 编号 | 任务 | 依赖 |
|---|---|---|
| B1 | ctl 和本机内置组件：`ctl dev / start / stop / status`；内置 PostgreSQL（端口 15433，每个服务一个角色和 schema）；NATS；Windows 和 Linux 都能跑 | K1 |
| B2 | iam 核心 + 身份源框架：账号、身份、登录事务、步骤协议、连接器接口和加载、远程连接器客户端、会话、OIDC 提供方、JWKS；内置应急账号 | K2、C1 |
| B3 | 内置"团队账号"连接器：邀请注册、口令 + TOTP、Passkey、手机扫码确认、设备管理 | B2 |
| B4 | 通用 OIDC / OAuth 2.0 连接器；身份源配置接口（按 JSON Schema 校验） | B2 |
| B5 | 第一个企业定制连接器（私有仓库） | B2 |
| B6 | org：租户、成员、组、角色和权限集合、策略、配额、邀请；发 `user.*` 事件 | K2、C1 |
| B7 | edge：路由表、会话校验、管理台独立源、限流、静态文件、CertMagic、可信代理；身份头签名 | K1、C1 |
| B8 | 门户和管理台的壳（Vue）：登录页按步骤类型通用渲染、邀请页、手机确认页；管理台框架、中英文、液态玻璃黑蓝主题 | K1、C1 |
| P0 | 插件离线版：`clients/dsh-plugin` 的 standalone 模式（应急账号 + JS 连接器框架 + 通用 OIDC）；单独发布 npm `auriga-dsh` | C1 |
| B9 | M1 集成：单机模式跑通各种登录和管理台基本操作 | B1–B8 |

## M2 工作空间：能给每人起 DSH

| 编号 | 任务 | 依赖 |
|---|---|---|
| W1 | workspace：实例模型、规格和代数、调度、生命周期决策（闲置、闹钟唤醒、常驻、准入）、节点管理和入网 CA、NodeControl 服务端、作业表 | B6、C1 |
| A1 | 节点代理骨架：入网、gRPC 会话、本地状态、对账器、假驱动 | C1 |
| A2 | Docker 驱动、制品管理、配置渲染、目录迁移 | A1、S4 |
| A3 | ingress（反代、文件、日志）、观测（活动、定时任务、资源） | A1 |
| E2 | edge 反代实例：HTTP / WebSocket 转到节点代理 ingress，加签名身份头 | B7、A3 |
| P1 | 插件托管模式：门锁、守卫、活动探针、面板外壳、`ctx.get('auriga')` SDK；DSH 兼容性测试 | C1、S1、S2 |
| W2 | M2 集成：测试服务器上用 Compose 起一套，验证登录 → 冷启动 → DSH 正常 → 闲置回收 → 闹钟唤醒 | 以上 |

## M3 AI 与资源

G1 模型网关、G2 MCP 网关、R1 资源中心、N1 团队知识、X1 出网代理、F1 文件、P2 团队面板，最后集成验证。

## M4 Windows 与安装包

A4 Windows 原生驱动、A5 Linux 原生驱动、D1 Windows 安装包、D2 Linux 安装包、D3 Compose 和容器镜像、D4 多系统冒烟、P3 插件连接模式。

## M5 协作、运维与切换

L1 团队 agent 总线和通知、L2 审计、L3 运维（备份、离职、导出删除、系统设置）、A6 节点代理备份 / 归档 / 自升级 / 自治、原型数据导入、测试环境切换。

## M6 商业化

许可证文件与功能开关、多租户、计量与账单导出、Helm 和高可用、SAML / LDAP / SCIM、预置身份源模板、开源发布准备。

## 关键路径

```
C1 → K1 → K2 → B2 → B3/B4/B5 → B9（M1 完成）
          └→ B6 → W1 ─┐
       C1 → A1 → A2/A3 → E2 → W2（M2 完成）
       S1、S2 ──────→ P1 ┘
```

## 完成标准（每个任务）

- 新功能带测试，全量测试通过、数量不减；
- 契约没被私自修改，有改动的通过兼容性检查；
- ArchUnit、gitleaks、lint 通过；中英文文案齐全；
- 仓库里没有内部信息；
- 相关文档（README、ADR、契约说明）同步更新。
