# 开发规范

所有人（包括 AI 工人）写代码前先读这份。各语言的构建细节见同目录 `java.md`、`go.md`、`web.md`、`common.md`、`ci.md`；已定的架构决定见 `docs/adr/`，**不要违背**；要改先提 ADR。

能自动检查的都已放进 CI（格式、lint、类型、ArchUnit、Maven Enforcer、gitleaks、契约 lint 和兼容性检查）。CI 不绿不合并。

## 1. 总原则
- **契约优先**：跨服务、跨进程、前后端之间的接口先改 `contracts/`（OpenAPI、protobuf、JSON Schema），再生成类型；不手写契约里已有的类型，不在代码里发明契约外的字段或错误码。
- **只做当前任务要的**：不为"以后可能需要"加抽象、开关或配置项；代码不写兼容旧行为的分支（项目未发布）。契约改动要过 CI 的兼容性检查（`buf breaking`、OpenAPI diff），确需破坏性修改由主控决定。
- **一处定义**：常量、错误码、权限 key、事件类型都只在一处定义（契约或 common），别处引用。
- **安全默认拒绝**：权限检查在服务端，接口必须显式声明权限（common 的注解，漏标注启动失败）；默认值要安全（只绑回环、不开调试）。
- **公开仓库**：代码、测试、文档、默认配置里不出现真实域名、IP、账号、公司名，用 `example.com`、`192.0.2.x`、`alice`；必须支持只有 IP、只有 HTTP 的部署。

## 2. 目录与模块
- Java 服务：`services/<服务>/api`（对外接口：gRPC 生成类型的包装、Java 接口、DTO）+ `services/<服务>/impl`（实现）。服务之间只依赖对方的 `api`（ArchUnit 检查）。
- `impl` 内分包：`web`（HTTP 控制器）、`grpc`（gRPC 服务端）、`app`（用例 / 事务边界）、`domain`（实体和规则，不依赖 Spring Web）、`persistence`（MyBatis-Plus mapper 和仓储）、`events`（事件消费）、`config`（装配）。控制器只做参数转换和调用 `app`，不写业务逻辑、不直接用 mapper。
- Go：每个组件一个模块 `infra/<组件>`，`cmd/<程序>/main.go` 只做装配，逻辑放 `internal/`；生成代码只在 `infra/gen`。
- 前端：`web/shared` 放跨 app 复用的组件、API 客户端、主题、i18n；app 里按页面分 `views/`、组件 `components/`、状态 `stores/`（Pinia）。
- 文档：架构 `docs/architecture/`，决定 `docs/adr/`，开发说明 `docs/dev/`，计划和进度 `docs/plan/`。

## 3. 命名
- Java 包根 `auriga.<服务>`；Go 模块 `github.com/hansjone/auriga/infra/<组件>`；npm 包 `@auriga/<名字>`。
- 数据库：表和列 `snake_case`，表名单数（`account`、`login_transaction`）；每个服务一个 schema（与服务同名）。
- 错误码 `<服务>.<原因>`（如 `iam.invalid_credentials`）；权限 key `<服务>.<资源>.<动作>`（如 `org.members.write`）；事件类型按 `contracts/events`。
- REST 路径小写复数名词、`kebab-case`；管理类操作放 `/admin/**`。
- 配置项前缀 `auriga.<服务>.`；环境变量 `AURIGA_<服务>_<名字>`。

## 4. 数据库
- 迁移用 Flyway：`impl/src/main/resources/db/migration/<服务>/V<序号>__<说明>.sql`，已合并的迁移**不再修改**，只加新的。
- 业务表必须有 `tenant_id` 并启用 RLS（用 common 的模板）；主键 UUIDv7；时间 `timestamptz`（UTC）；金额和配额用整数最小单位。
- 迁移角色（表所有者）和运行时角色（只有 DML）分开，见 `common.md`。
- 事件经 outbox 同事务发出；不要在事务里直接调 NATS 或外部 HTTP。

## 5. 错误、日志、配置
- 业务错误抛 common 的 `AurigaException`（带契约错误码），由 common 统一映射为 problem+json / gRPC 状态；不要在控制器里自己拼错误响应。
- 日志用 SLF4J，结构化字段；不打印令牌、口令、cookie、个人敏感信息（common 会打码，但不要依赖它）；`INFO` 记业务里程碑，`DEBUG` 记细节，不用 `System.out` / `fmt.Println`。
- 用户可见文案全部走 i18n（中、英）；后端错误文案在消息资源文件里，按 `Accept-Language` 选。
- 机密从环境变量或密钥文件读，不进配置文件、不进仓库；测试密钥在测试里现生成。

## 6. 测试
- 新功能必须带测试，测试数只增不减；修 bug 先写能复现的测试。
- 安全相关必须有**负向测试**（越权、跨租户、过期、伪造、重放被拒），拒绝类断言具体错误码。
- 数据库测试用嵌入式 PostgreSQL（test-support），不 mock 数据库；外部依赖（连接器、模型等）用本地假服务。
- 测试只用随机端口，结束时关掉所有进程；不 `sleep` 等待，用轮询加超时。
- 前端用 Vitest + Vue Test Utils 做组件测试；不写截图类测试。

## 7. 依赖
- 新增依赖要有理由（写进 PR 说明），优先用已有的；版本统一在根 `pom.xml` 的 dependencyManagement、`go.work` 各模块 `go.mod`、pnpm workspace 里管理，不在子模块里写死另一套版本。
- 许可证须与 AGPL-3.0 兼容；不引入 GPL 不兼容或"非商业"许可的包。

## 8. 提交与合并
- 分支：工人分支 `agent/<工人>/<任务>`，spike 分支 `spike/<编号>-<名字>`；`main` 只通过 PR 合并（主控的文档、契约小改可直接提交）。
- 提交信息英文，格式 `<范围>: <做了什么>`（范围如 `iam`、`common`、`contracts`、`web`、`edge`、`docs`、`ci`），正文写为什么；AI 参与的提交末尾加 `Co-Authored-By`。
- 一个 PR 一个任务；PR 说明写：做了什么、怎么验证的、对契约的影响、遗留问题。
- 合并条件：CI 全绿；安全关键模块（iam、令牌、edge、节点代理、隔离、common 安全部分）另有一次不是作者的审查。

## 9. 评审清单（提 PR 前自查）
- [ ] 只改了任务范围内的路径；没有调试代码、注释掉的代码、TODO 无主
- [ ] 类型来自契约生成；用到的每个错误码 / 权限 key / 事件类型都能在 `contracts/` 里搜到（前端 mock 和文案也一样），搜不到就先提契约修改，不自己发明
- [ ] 没有为了本地能跑而改构建配置（alias、paths、插件开关）
- [ ] 业务表有 tenant_id + RLS；跨租户负向测试有
- [ ] 接口声明了权限；默认拒绝
- [ ] 日志无敏感信息；文案有中英文
- [ ] 没有真实地址、账号、密钥
- [ ] 测试通过、数量没减少；文档（`docs/dev/`）同步更新
