# common 公共库

`services/common`（artifactId `common`）是所有 Java 服务共用的基础设施：租户隔离、outbox 与事件、
审计、错误、资源服务器安全、gRPC、可观测、许可证检查点。服务只要依赖它并填几个配置，自动配置
（`META-INF/spring/...AutoConfiguration.imports`）就会装好。包根是 `io.github.hansjone.auriga.common`。

## 业务服务接入步骤

1. `impl` 模块依赖 `common`（已有）；测试依赖 `test-support`。
2. 配置（单服务部署时；合并部署见文末"合并部署"）：

   ```yaml
   auriga:
     service:
       name: org              # 事件 source = auriga/org，审计主题 auriga.audit.org
       schema: org            # 服务自己的 schema
     security:
       jwt:
         issuer: https://auriga.example.com
         jwk-set-uri: https://iam.internal.example.com/oauth2/jwks   # 非回环地址必须 https
       public-paths: [/actuator/health, /actuator/health/**, /error]   # 默认值；iam 加登录相关路径
     events:
       nats-url: nats://127.0.0.1:4222   # 不填：事件只写 outbox，不转发、不消费
       validate-schemas: true            # 开发 / 测试开启；生产可关
     errors:
       message-bundles: [messages/org-errors]   # 服务自己的错误文案
   spring:
     flyway:
       schemas: org
       locations: classpath:db/org
   ```

3. 数据库角色：每个服务**两个**角色，都**不能是超级用户、不能有 BYPASSRLS**：
   - 迁移角色（如 `org_owner`）：schema 和表的所有者，只给 Flyway 用（`spring.flyway.user/password`，
     common 自己的迁移也走 Flyway 的数据源）；
   - 运行时角色（如 `org_app`）：服务连接用（`spring.datasource.*`），只有 schema 的 `USAGE` 和表 /
     序列的 `SELECT/INSERT/UPDATE/DELETE`（`ALTER DEFAULT PRIVILEGES FOR ROLE org_owner ...`），
     不是表的所有者，不能 DDL、不能关 RLS。
   两者都 `ALTER ROLE ... SET search_path = org`。超级用户会绕过 RLS（测试里专门验证了这一点）。
   测试里用 `PostgresExtension.serviceRolesProperties(...)` 建出同样的两个角色。
4. Flyway 迁移里，每张业务表带 `tenant_id uuid NOT NULL`，建表后启用 RLS：

   ```sql
   CREATE TABLE member (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, ...);
   SELECT auriga_enable_tenant_rls('member');
   ```

   common 自己的迁移（`classpath:auriga/db/common`，历史表 `auriga_common_schema_history`）在服务
   迁移之前自动执行，建出 RLS 辅助函数、`outbox_event`、`processed_event`、`dead_letter_event`。
   服务的 Flyway 以"基线 0"运行，所以 schema 里已有 common 的对象也不影响 V1 起的迁移。
5. MyBatis-Plus 实体不要给 `tenantId` 赋值，插件会自动填；UUID 字段直接用 `java.util.UUID`
   （common 注册了 `UuidTypeHandler`）。没有 `tenant_id` 的业务表列进
   `auriga.persistence.non-tenant-tables`。
6. 所有读写租户数据的代码都放在事务里（`@Transactional`）。
7. 测试参照 `services/common/src/test/java/.../example/ExampleServiceTest`：
   `PostgresExtension.serviceRolesProperties(...)` 以迁移角色跑 Flyway、运行时角色连库，`NatsExtension.properties(...)`
   提供 NATS。

## 1. 租户

- `TenantContext`：当前租户。`require()` 没有租户时抛 `MissingTenantException`（HTTP 500，
  `common.internal`）；`run/call(tenantId, ...)` 显式绑定；`runAsSystem/callAsSystem` 是显式标注的
  系统级作用域。
- 来源：HTTP 由安全过滤器从访问令牌 `tid` 绑定（**请求头不能覆盖**）；gRPC 由服务端拦截器从服务令牌
  和元数据 `auriga-tenant-id` 绑定；事件消费按信封 `tenantid` 绑定；后台作业自己调 `TenantContext.run`。
- **为什么用 ScopedValue**（Java 25 正式特性）：绑定不可变、作用域结束自动解除、不会被线程池 / 虚拟线程
  隐式继承，所以一个请求的租户不可能漏到下一个请求。代价是换线程时必须显式传递（executor、NATS 回调、
  定时器都要自己 `TenantContext.run`）。只有 `StructuredTaskScope` 的子任务会继承。日志 MDC 同理只在
  当前线程有效。
- **MyBatis-Plus 租户插件**（`AurigaTenantLineHandler`）：所有语句自动加 `tenant_id = 当前租户`，
  插入自动补列；没有租户直接报错，不会查全部。例外：
  - `TenantContext.runAsSystem(...)` 里的语句；
  - mapper 方法上的 `@InterceptorIgnore(tenantLine = "true")`；
  - 基础设施表和 `non-tenant-tables`。
  这些例外**仍受 RLS 约束**。如果服务自定义了 `MybatisPlusInterceptor` 却漏掉租户拦截器，启动失败。
  多数据源时 common 的持久化自动配置会退让，但守卫（`AurigaPersistenceGuardAutoConfiguration`）
  仍检查：每个 `SqlSessionFactory` 必须带租户拦截器、每个 JDBC 事务管理器必须是
  `TenantTransactionManager`，否则启动失败（只能用 `auriga.persistence.allow-unguarded=true` 显式关掉）。
- **RLS 兜底**：`TenantTransactionManager`（自动配置为 `transactionManager`）在每个事务开始时执行
  `set_config('auriga.tenant_id', <租户>, true)`（即 `SET LOCAL`，提交 / 回滚后失效，连接池复用也不
  串租户）。策略 `tenant_id = auriga_current_tenant()`，没设置时匹配不到任何行，插入 / 改成别的租户
  直接报错（`42501`）。`FORCE ROW LEVEL SECURITY` 让表的属主（服务角色）也受约束。
  事务外（自动提交）的查询看不到任何租户数据。
- **事务租户**：事务开始时登记当时的租户。事务里再 `TenantContext.run(别的租户 / 系统作用域, ...)`
  然后访问租户数据（租户插件 `getTenantId` / `ignoreTable`、`EventPublisher.publish`）会抛
  `TenantTransactionManager.TenantMismatchException`——数据库里的 `auriga.tenant_id` 还是旧租户。
  **换租户要开新事务**：`@Transactional(propagation = REQUIRES_NEW)`（或 `TransactionTemplate`
  设 `PROPAGATION_REQUIRES_NEW`）放在 `TenantContext.run(新租户, ...)` 里面调用。
- **RLS 的防护边界**：它防的是应用逻辑错误（漏了租户条件、绑错租户、mapper 写错），**不防 SQL 注入**：
  注入的语句能执行 `set_config('auriga.tenant_id', ...)` 换成任意租户。所以 SQL 一律参数化，
  `${}` 拼接只允许白名单标识符。
- **基础设施表**（`outbox_event`、`processed_event`、`dead_letter_event`、两张 Flyway 历史表）只给 common
  用，业务代码**不得查询或修改**；它们不在租户插件范围内，排障用运维账号直接查库。
- 跨租户的后台维护：按租户逐个 `TenantContext.run(tenant, ...)`。RLS 没有"绕过"开关，这是刻意的。

## 2. outbox 与事件

发布（和业务数据同一事务）：

```java
@Transactional
public void disable(UUID accountId) {
  members.markDisabled(accountId);
  events.publish("user.disabled", accountId.toString(), new UserDisabled(...));
}
```

- `EventPublisher.publish(type, subject, data[, dataversion])`：必须在事务里（否则
  `IllegalStateException`），租户取当前绑定（系统作用域用全 0 平台租户）；组装 CloudEvents 信封
  （UUIDv7 id、`source=auriga/<服务>`、`traceparent`），按契约 JSON Schema 严格校验后写 outbox。
  事务回滚则事件不存在。类型 `x.y_z` 对应 `contracts/events/x.schema.json#/$defs/XYZ`；契约里没有的
  类型直接拒绝。
- 主题：`auriga.evt.<type>`；`audit.recorded` 发到 `auriga.audit.<服务>`。启动时（可关）确保流
  `AURIGA_EVENTS`、`AURIGA_AUDIT` 存在。
- 转发器 `OutboxForwarder`：`FOR UPDATE SKIP LOCKED` 领取，多副本不重复；**每个 subject 只取最早
  未发的一条**，所以同一 subject 严格有序（失败时后面的同 subject 事件等待）。JetStream 确认后标记
  `published_at`；失败按 `base * 2^n` 退避（上限 `max-backoff`）无限重试，看积压和失败计数告警。
  提交后会唤醒转发器，平时按 `poll-interval` 轮询；已发事件保留 `retention`（默认 7 天）后清理。
  消息头 `Nats-Msg-Id` = 事件 id。
- 顺序：`publish` 在写 outbox 前取事务级咨询锁 `pg_advisory_xact_lock(<命名空间>, hashtext(subject))`，
  同一 subject 的并发事务在这里排队到前一个提交 / 回滚，所以 outbox 序号顺序就是提交顺序（不同 subject
  不互相阻塞；hash 冲突只会多等一会儿）。代价：同一 subject 的事务从 `publish` 到提交是串行的，
  `publish` 尽量放在事务末尾。

消费：

```java
@Component
class UserDisabledHandler implements EventHandler {
  public String name() { return "user-disabled"; }          // 持久消费者 <服务>-user-disabled
  public Set<String> eventTypes() { return Set.of("user.disabled"); }
  public void handle(ReceivedEvent e) { var d = e.dataAs(UserDisabled.class); ... }
}
```

- 每个 `EventHandler` bean 一个 JetStream 持久拉取消费者。`handle` 在事务里执行，已绑定事件的租户、
  traceparent；同一事务先写 `processed_event (consumer, event_id)`，重复投递直接确认、不再执行。
- 抛异常：回滚（包括去重记录），按退避重投；投递次数达到 `max-deliveries`（默认 5）后写
  `dead_letter_event`、ERROR 日志（含 `ALERT`）、计数 `auriga.events.dead_lettered`，然后确认。
  无法解析的消息直接进死信。
- **来源校验**（消费前，失败直接进死信、计数 `auriga.events.rejected`、不执行 handler）：
  - NATS 主题必须等于 `auriga.evt.<信封 type>`；`type` 必须在该 handler 的 `eventTypes()` 里；
  - `source` 必须是该类型的合法生产方。默认映射取自契约事件表（`user.*`→org、`identity.*`→iam、
    `instance.*`→workspace），契约外的类型默认拒绝；服务自定义类型或调整用
    `auriga.events.producers[widget.created]=example`（键是精确类型或 `前缀.*`，精确优先）；
  - 审计记录（`audit.recorded`）：主题后缀必须等于 `source` 的服务名（`auriga.audit.org` ↔ `auriga/org`）。
- **max-deliveries 的坑**：死信由 common 判断（第 `max-deliveries` 次失败时写死信并确认），JetStream
  消费者的 `max_deliver` 设为它加 5 作余量。如果有人在 NATS 侧把消费者的 `max_deliver` 改小、或 ack
  超时次数先耗尽，JetStream 会**静默丢弃**消息（不进死信表）。所以不要手工改消费者配置；监控
  `auriga.events.max.deliveries`（当前配置值）和 JetStream 的 `MAX_DELIVERIES` advisory
  （`$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.>`）。
- **NATS 连接与 ACL**：`auriga.events.nats-creds-file`（JWT + nkey 的 `.creds`）或
  `auriga.events.nats-nkey-seed-file`；`auriga.events.nats-tls=true` 或 `tls://` URL 启用 TLS
  （JVM 信任库）。部署时**每个服务一个 NATS 用户**，发布权限只给自己的主题，例如 org：
  `publish: ["auriga.evt.user.>", "auriga.audit.org", "$JS.API.>"]`（JetStream API 按需收窄），
  订阅只给 `_INBOX.>` 和自己消费者需要的 `$JS.API.CONSUMER.*` 主题。消费端的来源校验是第二道防线，
  不能替代发布 ACL。
- 宽松解析：未知属性、未知 data 字段都忽略（`ReceivedEvent.dataAs`）。

## 3. 审计

```java
audit.record(AuditEntry.builder("org.member.disable")
    .target("member", id.toString(), name)
    .changes(AuditChanges.create().change("status", "active", "disabled").secret("password"))
    .build());
```

按 `audit.schema.json` 组装，和业务同一事务写 outbox。actor / IP / UA / 会话 / requestId 取自当前
请求，没有请求时是 `system`。机密字段只记字段名（`{"redacted": true}`）；字段名像机密的
（password、secret、token、apiKey、otp、pin……）即使用 `change()` 传入也会自动脱敏，`details` 同样处理。

## 4. 错误

- 抛 `AurigaException(ErrorCode, params)`；通用码在 `CommonErrors`，服务自定义
  `ErrorCode.of("org.last_owner", 409, Status.Code.FAILED_PRECONDITION)`（码格式 `<域>.<snake_case>`）。
- HTTP：`application/problem+json`，字段 `type/title/status/detail/instance/code/params/errors/traceId`。
  `detail` 按 `Accept-Language` 选中文（`zh-CN`，`zh` 也匹配）或英文，默认英文。参数校验错误给出
  `errors[].field`（JSON Pointer）。未预期异常一律 `common.internal`，不泄露内部信息。
- 文案：UTF-8 properties，默认文件英文，`_zh_CN` 中文，占位符用参数名 `{permission}`。服务把自己的
  bundle 加到 `auriga.errors.message-bundles`。
- gRPC：状态码 + 元数据 `auriga-error-code`（参数在 `auriga-error-params-bin`，JSON）。远程适配器用
  `GrpcErrors.call(() -> stub.xxx(...))`，调用方拿到和本地调用一样的 `AurigaException`。

## 5. 安全（资源服务器）

- 访问令牌校验（`AccessTokenDecoders`）：JOSE 头 `typ` 必须是 `at+jwt`；只接受 ES256；JWKS 缓存
  5 分钟，遇到未知 kid 重新拉取（限速，默认 30 秒一次，防止随机 kid 打爆 iam）；`iss` 必须匹配；
  `aud` 必须含 `auriga`（只含第三方客户端 id 或 `auriga.ai-gateway` 的令牌被拒）；必须有 `exp`，
  exp/nbf 允许 30 秒偏差；`sub/tid/ptyp/ctx/jti/iat` 必须存在且合法。未配置 issuer / jwk-set-uri 时
  拒绝所有令牌（只打警告，便于合并部署先启动）。
- HTTP API **拒绝内部服务令牌**：`ptyp=internal_service` 或 `aud` 含 `auriga.internal` 的令牌一律 401。
- `jwk-set-uri` 在非回环地址上必须是 https（否则启动失败），只有隔离网络才用
  `auriga.security.jwt.allow-insecure-jwks-uri=true`。
- 主体 `AurigaPrincipal`：账号、租户、ptyp、ctx、sid、did、角色、权限、auth_time、stepup_time。
  `AuthorizationRules.currentPrincipal()` 取当前主体。
- **默认拒绝**：启动时检查所有 `@RequestMapping` 处理方法（Spring 自己的除外）和所有已实现的 gRPC
  方法，必须带 `@RequiresPermission`、`@AdminOnly`、`@RequiresRecentStepUp`、`@AuthenticatedOnly`
  之一（方法或类上），HTTP 还可以是 `@PublicEndpoint`；漏标注直接启动失败。
  - `@AuthenticatedOnly`：只要求登录，不要求具体权限（如 `/me`）。
  - `@PublicEndpoint`：不需要令牌（只能标在方法上；`/admin` 路径上标了也会启动失败）。gRPC 没有这个
    注解，公开方法（健康检查）列在 `auriga.grpc.server.public-methods`。
- 注解（MVC 控制器和 gRPC 服务实现都生效，由 `AuthorizationAdvisor` 统一执行）：
  - `@RequiresPermission("org.members.write")`：对应 OpenAPI 的 `x-permission`；类和方法上的权限取并集。
  - `@AdminOnly`：对应 `x-admin-only`，只接受 `ctx=admin`。另外过滤器链对所有
    `/api/*/v*/admin/**` 路径强制 `ctx=admin`，漏写注解也不会放行；公开路径不能覆盖管理路径。
  - `@RequiresRecentStepUp`（默认 600 秒）：`stepup_time` 太旧或缺失返回 403 `iam.step_up_required`。
- 过滤器链：只认 Bearer，无会话、无 CSRF（由 edge 负责），401/403 都是 problem+json。

## 6. gRPC

- 服务端 `AurigaServerInterceptor`：**只接受内部服务令牌**（`authorization: Bearer <令牌>`，
  `aud` 只有 `auriga.internal`、`ptyp=internal_service`、`ctx=service`、`sub=svc:<服务名>`、`tid` 为
  平台租户），用 `AccessTokenDecoders.createInternal` 校验；外部 client credentials 令牌（服务账号）、
  用户、设备令牌一律 `UNAUTHENTICATED`。即使误配成平台解码器，拦截器也会再检查主体类型并拒绝。
- 代租户：元数据 `auriga-tenant-id` 只对 `auriga.grpc.server.tenant-delegates`（`svc:<服务名>` 列表）
  里的调用方生效，其他调用方带了就 `PERMISSION_DENIED`；不带则不绑定租户。不能代平台租户。
- 传播 `x-request-id`、`traceparent`；回调在请求 / 租户作用域里执行；异常映射成状态码。
- `auriga.grpc.server.enabled=true` 时启动内嵌服务端（`host` 默认 `127.0.0.1`、`port`，0 为随机端口），
  注册所有 `BindableService` bean，处理器跑在虚拟线程上。**没配 TLS 时拒绝绑定非回环地址**，除非显式
  `allow-insecure-non-loopback=true`；分开部署配 `tls-cert-chain`、`tls-private-key`、
  `tls-trust-cert-collection`（配了 CA 即要求客户端证书，mTLS）。
- 客户端 `AurigaClientInterceptor`：需要一个 `ServiceTokenProvider` bean（由 iam 集成提供），自动带
  服务令牌、当前租户、请求 id、子 traceparent，调用方没设 deadline 时用 `auriga.grpc.client-timeout`
  （默认 5 秒）。
- 本地 / 远程切换（ADR 0003）：

  ```properties
  auriga.remote.org.mode=local          # 默认：org 的实现在同一个 JVM，用它的 Bean
  auriga.remote.org.mode=grpc
  auriga.remote.org.target=org.internal.example.com:9090
  ```

  远程适配器 bean 标 `@ConditionalOnServiceMode(service = "org", mode = GRPC)`，用
  `GrpcChannels.open(GrpcChannels.target(env, "org"), clientInterceptor)` 建通道，实现 org-api 的
  同一个接口。示例见测试 `grpc/TenantDirectoryExample`（契约 `TenantService`，空实现）。

## 7. 可观测

- 结构化 JSON 日志默认开启（`auriga.logging.json=false` 关闭；显式设置
  `logging.structured.format.console` 优先）。每行字段：`@timestamp level logger thread message
  traceId spanId requestId tenantId exception`。消息和堆栈经过 `SensitiveDataMasker`：Bearer / Basic
  凭据、JWT、`ait_` 实例令牌、password / secret / token / cookie 等键值（带引号的值含空格也整段打码）、
  `Cookie` / `Set-Cookie` 的所有键值对、URL 里 `user:密码@` 的密码都会打码。
- W3C `traceparent`：HTTP 入口继续上游的 trace（或新建），gRPC 出入、事件信封都带上。
  `X-Request-Id` 不合法（过长、含控制字符）时重新生成，并回写到响应头。
- Micrometer 指标：`auriga.outbox.backlog`（积压）、`auriga.outbox.forward.latency`（写入到确认）、
  `auriga.outbox.publish.failures`、`auriga.events.consume.failures`、`auriga.events.dead_lettered`、
  `auriga.events.processed`（标签 consumer / outcome）、`auriga.events.rejected`（来源校验失败）、
  `auriga.events.max.deliveries`（配置值，配合 JetStream advisory 告警）。

## 8. 许可证与计量

只留最小接口：`LicenseCheckpoint.allows(feature)`（默认社区版全部允许）、`UsageMeter.record(...)`
（默认丢弃）。企业版模块用自己的 bean 替换。

## 测试基座（test-support）

- `PostgresExtension.serviceRole(testClass, role, schema)` / `serviceRoleProperties(...)`：建一个非超级
  用户、非 BYPASSRLS、拥有 schema 的登录角色，测试 RLS 必须用它。
- `NatsExtension` / `NatsServer`：Java 没有可嵌入的 NATS 服务端，所以下载官方 `nats-server`
  **v2.15.1** 发布包到共享缓存（`-Dauriga.test.cache`、环境变量 `AURIGA_TEST_CACHE`，默认
  `${user.home}/.cache/auriga`），按代码里固定的 SHA-256 校验，作为子进程启动：只听 127.0.0.1、
  随机端口、开 JetStream、数据在临时目录，JVM 内共享一个，测试结束杀掉进程并删目录。离线 CI 可用
  `AURIGA_NATS_SERVER` 指向预装的二进制。

## 合并部署

自动配置面向"一个进程一个服务 schema"。`platform-app` 把多个服务装进同一个 JVM 时，每个服务需要
自己的 DataSource（角色）、`TenantTransactionManager`、`OutboxEventPublisher`、`OutboxForwarder`、
`InboxProcessor`；这些类都是普通构造器，可以在各服务的 `*ModuleConfiguration` 里按 schema 实例化。
具体接线方式由主控在合并部署任务里确定。
