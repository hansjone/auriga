# workspace 服务

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
