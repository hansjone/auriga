# workspace 服务

## 活动心跳与未知状态

`HeartbeatRequest.activity` 的四个计数必需，显式 `null` 表示未知；缺字段、负数、非整数无效。
契约生成的 Java `Integer` 不能区分缺字段与显式 null，因此仅心跳请求的 `RequestBodyAdvice`
在绑定前验证原始 JSON 的字段存在性和整数类型，随后仍走生成模型校验、实例令牌鉴权和通用错误映射。
其他接口不受此补充检查影响，原有全整数客户端继续有效。
心跳是小报文，其原始 JSON 限 64 KiB；按实际流最多读取上限加一字节，
超限返回 `400 common.validation_failed`，无论是否提供 `Content-Length`，也包括 chunked 请求。
这只限制心跳接口，不修改全局请求限制。

每次合法心跳刷新 `plugin_seen_at`。任一未知、真实正计数或 `health.ok=false` 都将已有
`plugin_active` 设为回收保护，但只有真实正计数及上报的用户交互时间推进 `last_activity_at`；
未知或健康故障不冒充用户活动。后续四项已知为 0 且健康的心跳会清除保护，
不会永久锁存，也不重新开始闲置计时。只有插件和 CPU 观测均新鲜空闲，且原闲置期限已到时才回收。
持续未知期间不猜测任务已经结束；恢复后按原期限正常回收。不需要新增数据库字段或迁移。

## 闹钟与闲置回收的上限

记 `截止 = last_activity_at + idleMinutes + 4 分钟`。临近闹钟（落在 `(当前时间, 当前时间 + 4 分钟]`）
在截止前保护运行中的实例；截止后，**在截止前已上报**（`wake_reported_at` 早于截止）且不晚于
`截止 + 4 分钟` 的闹钟继续保护到它触发为止。截止后接受的近期编辑，如果闹钟在未来
150 秒内（30 秒最短停止期 + 2 分钟预热），也继续保护运行，因为此时停机再预热可能错过触发。
两种保护都要求闹钟不晚于 `截止 + 4 分钟`，到达该绝对上限即失效；上报时间不能移动它。
因此同一闲置期最长运行到 `last_activity_at + idleMinutes + 8 分钟`，不断移动的闹钟无法延长，
近期编辑不会通过先停机再启动重置活动时间。闹钟触发后，正常闲置回收继续生效。
4 分钟等于两倍预热提前量（2 分钟）。只有实际活动或已有生命周期操作更新 `last_activity_at`。

截止后因空闲停机时，若 `next_wake_at` 仍在未来，取消该实例未完成的唤醒作业并重建唯一一个，
到期时间为 `max(next_wake_at - 2 分钟, 当前时间 + 30 秒)`：它的预热作业可能已在运行期间被消耗，
不重建会丢闹钟；至少 30 秒保证停机不会在同一轮决策循环里被撤销。重新唤醒照常经过
`wakeOnSchedule`、成员状态、配额和准入检查，并开始新的闲置期。待处理的尾部上报不会被清除，
之后应用时照常替换该作业。

上报的 `next_wake_at` 只接受 `[当前时间 - 2 分钟, 当前时间 + 400 天]`（容忍时钟偏差和上报延迟，
覆盖按年计划）；越界的上报直接忽略（记 debug 日志，不写库、不建作业）。

每个实例最多每 30 秒应用一次变更并重建闹钟作业。超频上报只覆盖单个持久化的
待处理值（包含取消），窗口结束后由每 5 秒的决策循环应用最终值，不断开节点会话。
迁移 V3 添加 `wake_reported_at`、`wake_report_pending` 和 `pending_wake_at`，
在实例行锁内读写，使多副本和服务重启共用限制；节点归属检查仍先执行。
相同上报不写库，已消费的相同作业不复活。取舍：窗口内的合法修改可能延迟约 30–35 秒生效，
但最后一次编辑或取消无需 agent 再次修改文件或重报。

已完成的唤醒作业（含被取消的）保留 1 天后由决策循环删除；按 UUIDv7 主键（即创建时间）判断，
远期但已取消的作业也能及时清理。V3 为此加部分索引 `workspace_job_done_wake`。
重启、删除作业不清理（重启作业按 generation 去重会读历史行）。

## combined 开发种子保护

`DevBootstrap` 同时要求 `auriga.platform.combined=true`、`auriga.platform.seed=true`
以及显式启用 `dev` 或 `e2e` profile。缺少该 profile 时只记录 WARN 并立即返回，
不会读取或写入种子数据、创建账号、提升 owner 权限或缩短闲置策略。
真正执行种子时也记录 WARN。生产误开 seed 不会绕过 profile 条件。
现有 CI Compose 的 `SPRING_PROFILES_ACTIVE=combined,dev` 和 dev 配置的 `seed: true`
继续有效，无需增加环境变量；`e2e` profile 本身不隐式打开 seed。
保护只比较 profile 名：`dev` profile 还会放开不安全 JWKS、cookie `secure=false`，
**生产环境禁止启用 `dev` 或 `e2e` profile**。

workspace 负责规格、容量预留、准入和生命周期决策。节点执行规格；控制面失联不会被解释成实例已停止。对外路径和数据模型来自 `contracts/openapi/workspace.yaml`，控制通道使用生成的 `auriga.node.v1` 消息。

## 装配

combined 中设置 `AURIGA_WORKSPACE_ENABLED=true` 后启用。默认关闭，避免在尚未提供 CA 和独立数据库角色时启动半配置的服务。

必须提供以下配置（Spring 环境变量将点号和短横线替换成下划线并转大写）：

- `auriga.platform.workspace.jdbc-url`、`username`、`password`：运行时角色，只授予 workspace schema 的 USAGE 和 DML。
- `auriga.platform.workspace.migration-username`、`migration-password`：独立迁移角色；不得给运行时角色 DDL、超级用户或 BYPASSRLS。
- `auriga.workspace.encryption-key`：环境变量中的 32 字节 Base64 主密钥，用于 AES-GCM 加密实例令牌和幂等响应。重启必须保留同一把密钥。
- `auriga.workspace.ca-certificate-file`、`ca-private-key-file`：持久的内置 CA，私钥为未加密 PKCS#8 EC P-256 PEM，只能由服务账号读取。不要放进仓库、模板或配置文件。服务启动不自动生成或替换 CA。
- `auriga.workspace.tls-certificate-file`、`tls-private-key-file`：节点监听器的服务端证书链和私钥。证书由上述 CA 签发，包含 URI SAN `auriga://workspace`，并包含客户端连接地址对应的 DNS/IP SAN 和 serverAuth EKU。
- `auriga.workspace.control-endpoint`：节点可访问的 HTTPS 地址，如 `https://platform.example.com:7443`；入网和续期均使用该节点监听器。
- `auriga.workspace.node-server.host`、`port`：默认 `127.0.0.1` 和 `0`。实际部署需要将端口与上述公开地址配置一致。测试只用随机端口。

workspace 数据源默认池大小为 20。节点会话使用数据库 node_session 的 60 秒租约，获取、续租和释放均为短事务，不长期占用连接。持有者 UUID 防止过期会话继续写入；租约与管理操作统一先取租户锁，再取租约行锁。同进程重连替换旧流，其他副本上的未过期租约返回 common.conflict。

迁移路径为 `classpath:db/workspace`，运行 common 迁移后再运行 workspace Flyway。独立部署时也必须设置该迁移路径、schema 和迁移角色。所有 workspace 表启用 FORCE RLS；运行时操作通过 `TenantTransactionManager` 绑定当前租户。`workspace_registry` 的平台租户记录仅用于从不透明令牌或节点证书定位租户，不包含令牌密钥。

combined 通过 `WorkspaceInputs` 调用 org；独立部署通过生成的 org gRPC 存根和 common 服务令牌拦截器调用，另需配置 `auriga.remote.org.target`、`auriga.workspace.org-client-certificate-file`、`org-client-key-file`、`org-ca-file`，以及 common 的 `ServiceTokenProvider`。org 必须在代租户白名单中允许 `svc:workspace`。

## 模板与信任材料

`auriga.workspace.templates-directory` 中每个 `.pb` 文件是契约 `InstanceSpec` 的二进制编码，提供已签名的 DSH/插件制品、运行时类型、资源默认值、网络/存储/配置及身份头公钥。没有可用模板时，创建实例返回 `common.unavailable`。制品生产与签名由资源/安装流水线负责；workspace 不在运行时下载 npm 包或代替资源中心签名。

不同 OS/arch 的模板用 DSH 签名声明中的平台字段区分。模板标签作为节点选择约束，与管理员管理的节点标签匹配。实例资源按有效配额裁剪；规格变化递增代数，数据库保留每一代规格，实例令牌按目标节点封装公钥使用契约规定的 HPKE suite 加密。

`auriga.workspace.artifact-keys-directory` 中的 `.pb` 是 `ArtifactSigningKey`，入网和续期时返回；这些公钥必须由独立的制品签名流程管理，不能由控制通道随意替换。首把信任根和后续 endorsement 均由该流程提供。

`auriga.workspace.connect-manifest-file` 可指向按 `ConnectManifest` 契约生成的 JSON 清单（团队模型/MCP、面板地址和签名公钥）。服务每次按设备访问令牌替换 account 字段；未配置面板清单时，该接口返回 `common.unavailable`。

## 节点与生命周期

一次性入网令牌默认 60 分钟，有效期范围为 5–1440 分钟。库内只存摘要；`Idempotency-Key` 相同且请求相同的创建请求在 24 小时内返回同一份加密保存的响应，换请求体返回 `common.conflict`。CA 指纹是 SPKI DER 的 SHA-256，小写十六进制。

节点监听器启用 TLS，允许无客户端证书的 Join，其他 RPC 必须通过本 CA、节点 URI SAN、有效期、数据库吊销状态和当前证书序列号校验。它独立于 common 的内部服务 JWT 监听器，节点服务不作为 `BindableService` bean 暴露给后者。续期证书有效期为 30 天，且不超过 CA 到期时间；旧证书序列号立即失效，现有会话会断开并用新证书重连。

代理先发 Hello，再发 Inventory；收到 Welcome 后下发规格。重复规格按代数去重，重连重发全量。状态只接受该节点拥有的实例、且已观察代数等于当前代数的报告。未知实例报告被忽略并告警。restart 命令按持久 job id 去重，DeleteInstance 按实例代数确认。结果、尝试次数和错误落库；每次最多等待两分钟，失败后按 5、10、20、40、80 秒退避，最多五次。发送队列最多 128 帧或 4 MiB，不读超过 30 秒会断开；未确认命令重连后仍受持久重试预算约束。

每 5 秒逐租户运行决策。闲置回收同时要求插件四项计数和节点 CPU 活动均为空闲，而且两路观测仍新鲜；缺失观测不触发回收。常驻实例不回收。闹钟持久化到作业表，用 `FOR UPDATE SKIP LOCKED` 领取，提前两分钟改变期望状态；准入满保留作业等待重试。节点超过 60 秒未上报标记离线并将实例标为待迁移，不伪造停止状态。资源/版本改变产生待重启规格，实例空闲时发送持久化的 apply_pending 重启命令。

短暂离线后，同一分配节点通过受认证且租约有效的控制会话上报当前规格代数的 `InstanceStatus` 时，可清除离线遗留的 `migration_pending`。重连全量 Inventory 中的状态也走这条路径，容器状态不变仍会重新对账。清除还要求节点心跳在 60 秒内、未撤销、未 draining、允许调度，实例未删除，且 `requested_node_id` 为空或等于当前节点。运行中但未 ready、已停止或失败的当前代数报告也能解除离线阻断，最终路由仍按各自实际阶段和 ready 投影；不改变实例分配、期望状态、规格或代数。

Hello、节点用量心跳、实例插件心跳和旧代数 `pending_restart` 都不单独清除该标记。指定其他节点的迁移、drain、强制撤销仍被保留；跨租户、错节点和失效会话报告无法恢复它。`schedulable=false` 原本只阻止新调度，不会主动打断已有无待迁移标记的实例；若此时已有离线待迁移标记，则保守保留，需恢复可调度后再次上报当前代数状态或由管理员明确处理。自动清除不执行数据迁移，后续仍由正式路由发布器扫描已提交状态收敛。

重连 Inventory 与普通 Status 分别处理：Inventory 会重新检查缓存的 ready，可在节点 `updated_at` 相同的情况下把 ready 降为 false；它是当前控制会话的权威快照。V4 为实例增加 `status_watermark`，与 `observed_generation`、实际状态及待迁移标记在同一实例行锁内原子写入。同一已观察代数的普通 Status 必须携带严格晚于水位的有效 `updated_at`；更旧、等时（包括冲突内容）或缺失时间的帧整条忽略，不发布状态事件、不清标记。按 PostgreSQL 微秒精度比较，纳秒舍入也不能让等时旧 ready 帧覆盖 Inventory。新规格代数的首次无时间状态仍可初始化实际观察，但不能凭它清除离线待迁移标记；旧代数仅保留原有 `pending_restart` 语义，不能据此提升 ready 或清除标记。

节点时间只用于同一实例、同一观察代数内的保守顺序，不能证明跨节点全序。允许节点时钟最多领先服务端 60 秒；普通 Status 的零值、非法时间或超过此上限的未来值被忽略且不推进水位。Inventory 即使时间缺失或异常仍可完成重新检查，改用服务端接收时刻加 60 秒作为有限水位，挡住带正时钟偏差的旧队列帧，原始状态报文保持不变。正常的轻微正偏可持续推进；节点时钟回拨后，需要时间重新超过最后接受水位，或由下一份受租约保护的 Inventory 重新对账；突然从 +60 秒校正回服务端时间最多等待相应偏差。持续超过允许正偏的普通状态不会被接受，仍需校时或 Inventory。任意远未来时间不会造成永久水位锁。升级时既有非空状态尚无水位，首次普通报告保守回退到服务端 `updated_at`，Inventory 可立即建立新水位。缺失或异常 Inventory 时间建立的屏障可能保守忽略这 60 秒窗口内的合法普通状态；需要后续新 Status 越过水位，或下一份 Inventory 立即重新检查。没有承诺节点时钟回拨或缺失时间时每条普通状态仍有活性。

本人接口每次检查主人；只有带 `workspace.instances.admin` 的管理台主体可以代操作，其他人返回 404。SSE 返回初始状态，每 5 秒轮询数据库；每用户最多 4 条、每进程最多 128 条连接，60 秒到期后客户端重新鉴权连接。插件实例令牌只用于 `/instance/self` 和 `/instance/heartbeat`，不能用来调用用户或管理接口。

## 验证与范围外接线

```powershell
.\mvnw.cmd -pl services/workspace/impl -am '-Dtest=WorkspaceUnitTest,WorkspaceSurfaceTest,WorkspaceIntegrationTest' '-Dsurefire.failIfNoSpecifiedTests=false' test
.\mvnw.cmd -pl services/architecture-tests,services/platform-app -am '-Dtest=WorkspaceUnitTest,WorkspaceSurfaceTest,WorkspaceHardeningTest,ServiceBoundariesTest' '-Dsurefire.failIfNoSpecifiedTests=false' verify
```

集成测试使用 test-support 的嵌入式 PostgreSQL、独立迁移/运行时角色，以及随机端口的真实 TLS gRPC 假节点。需要允许 PostgreSQL 在本机启动子进程。当前 Codex Windows 沙箱中 `pg_ctl` 创建受限令牌失败，需由主控在可启动嵌入式 PostgreSQL 的环境重跑，不能把启动失败视为用例通过。

当前 A1 `infra/agent/internal/conn/session.go` 的 Hello 尚未填 `capacity`。服务允许入网/会话建立，但容量未知时保持实例 scheduling；主控需在代理侧补齐契约已有的容量上报，才能完成真实 A1 的实例分配验证。A1 reconciler 的删除 ABSENT 和失败报告还没有填 observed_generation，需要补齐已有字段才能完成对应代数的里程碑确认；服务不接受未确认代数的删除结果来释放容量。真实运行时、签名制品、面板签名清单和 IAM 身份公钥由相应生产流程配置，不在本任务内生成。

迁移执行本身属于后续工作；这里仅保存待迁移标记和目标节点请求，不直接改节点分配或搬动实例数据。实例路由 KV 的完整发布/更新与 edge 对接，以及 ai-gateway 专用令牌签发仍需对应模块接线。

## 第三轮安全加固

`workspace_registry` 只保存平台路由元数据，通过显式平台租户的独立事务访问，继续受 FORCE RLS 保护。实例使用 `instance` 类型，入网令牌使用 `token` 类型。路由提交与业务提交分开，业务回滚可能留下不含凭据的孤儿路由；认证仍验证租户业务记录，失败关闭。

Join 按传输层来源 IP 使用令牌桶（每来源突发 10、每秒补 1；每进程突发 100、每秒补 20），连续失败五次冷却 30 秒。来源缓存最多 4096 项；令牌格式和长度检查先于数据库与密码计算。监控计数器为 `auriga.workspace.join.accepted/rejected/failures`，不使用 IP 标签。数据库不可用返回 UNAVAILABLE。

CA 私钥加载时拒绝符号链接、非普通文件及共享权限：POSIX 不允许 group/others 权限；Windows ACL 只信任所有者、SYSTEM 和 Administrators。证书序列号 DER 编码不超过 20 字节。

普通吊销要求节点无实例。管理接口 `DELETE /admin/nodes/{nodeId}?force=true` 支持强制吊销（默认 false），要求管理台身份和 `workspace.nodes.admin` 权限：事务内禁用节点、删除会话租约、标记实例待迁移并写 outbox 审计，提交后断开本进程流；跨副本后续操作由租约拒绝。

## 连接池与部署注意

路由登记必须在业务事务开始前完成；实例创建先短事务查询已有实例，再登记候选路由，最后在租户锁内复查并写业务记录。节点入网在事务外登记候选路由，消耗令牌时仍加锁复查，令牌消费与节点写入保持原子性。幂等入网令牌也先准备路由、再在租户锁内复查幂等记录并落库。并发竞争或业务失败可能留下无凭据的孤儿路由，后续认证仍失败关闭。禁止在活跃事务内调用 `register`，避免持有连接再申请同池连接。提交后的规格通知由单个合并任务异步处理，提交线程不等待另一条连接。

Join 限流只认传输层来源地址，不信任转发头。前面有不保留源地址的代理时，代理后的节点会共用一个 IP 桶；部署应直连节点监听口或使用保留真实源地址的转发方式。本监听器没有新增 PROXY protocol 解析能力，不能仅发送该协议头来恢复来源。

CA 私钥检查拒绝符号链接。Kubernetes Secret 卷默认的符号链接和共享读取权限会被拒绝；应设置 `defaultMode: 0400`，使用 `subPath` 挂载到普通文件，或复制到仅服务用户可读的普通文件。通过复制或 subPath 使用密钥时，需由部署流程处理密钥更新与服务重启。

单条命令两分钟无确认只记录该次失败并按退避重试，保持节点会话；心跳超时、发送队列阻塞和认证失败仍会断流。删除命令耗尽五次预算时提交后记录 ERROR 告警，包含租户、节点、实例和作业 ID；未收到 ABSENT 确认仍保留容量预留，避免节点实际未删时过量分配。部署日志告警应匹配 `Node deletion exhausted retries` 并安排人工核查。

## E2：实例路由 KV 发布

启用配置 `auriga.workspace.routes.nats-url`（环境变量 `AURIGA_WORKSPACE_ROUTES_NATS_URL`）后，workspace 装配独立的路由发布器。认证可配 `auriga.workspace.routes.nats-creds-file` / `nats-nkey-seed-file`，`nats-tls=true` 或 TLS URL 使用 JVM 信任库。合并部署和独立部署均使用同一个 workspace 装配入口，无需跨服务读取数据库。NATS ACL 必须只允许 workspace 写 `$KV.auriga-routes.>`，允许查询／创建 `auriga-routes` bucket 和相应 JetStream API；edge 仅有读取权限。

数据库迁移 V2 给 node 添加 `ingress_endpoint`，保留 V1 不变。经过节点 mTLS、序列号及会话租约检查的 Hello 记录该地址；空表示没有入口，非法 scheme／凭据／查询串／HTTPS 路径等返回 common.validation_failed。`hostname` 不被当成入口。地址只用于定位，edge 最终验证平台 CA 和精确节点 URI SAN。

路由发布器启动时立即全量扫描，之后每秒重扫已提交的实例／节点状态，并按 `contracts/kv/route.schema.json` 更新 NATS KV。发布器不运行在业务事务或 afterCommit 调用线程上。实例调度、状态变化、节点入口变化、已落库的迁移分配都由下一轮扫描反映；实例删除、节点吊销或入口无效删除 KV 项。迁移待处理或节点心跳超过 60 秒标为 failed，阻止 edge 继续转发。尚未分配节点的实例不发布；host 为 null，edge 仍按其域名配置识别实例。应用 ready 的 running/pending_restart 映射 running，未 ready 映射 starting，停止／失败据实际阶段映射。

PostgreSQL 是恢复依据：NATS 故障不回滚业务操作，恢复或 workspace 重启后重发最新快照；不会重放排队的旧入口。数据库事务只负责读取 ID 和最新快照，提交并归还连接后才进行 NATS 操作，不持有数据库事务或 advisory lock 等待 NATS。为防多个副本的旧快照覆盖新值，先在事务外读取各路由当前 KV revision（包括删除标记），再读取新的数据库快照，最后在事务外按先前 revision 做 CAS update/delete；revision 冲突则放弃该次写入，下一轮重新读取最新数据库状态。删除标记也参与 CAS，避免旧快照复活已删除路由。扫描 ID 后新建的实例留到下一轮。所有数据库读取保持租户事务与 RLS；相同字节不重复写 revision。仍有每秒全量扫描与 KV 历史读取的开销，规模扩大时应评估增量发布；节点迁移执行本身仍属于 W1 后续任务。

节点证书由 `NodeCa.issue` 同时签发 clientAuth 与 serverAuth EKU，唯一 URI SAN 仍为 `auriga://node/<id>`，不接受 CSR 覆盖平台身份。入网和续期共用该签发逻辑。edge 保持 serverAuth 验证，旧证书需续期／换证。

测试：`WorkspaceRoutesTest` 检查契约形状与入口／状态；`WorkspaceRoutesNatsTest` 用 test-support NATS 检查幂等写入、更新、删除与恢复；`WorkspaceIntegrationTest.ingressHelloAndRouteRecoveryMigrationDeletion` 检查 Hello 持久化、真实数据库提交／回滚、故障补发、重启、迁移与删除，另有非法 Hello 入口负向测试。集成测试需要能启动嵌入式 PostgreSQL 的环境；沙箱中的启动失败不能算通过。
