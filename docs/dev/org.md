# org 组织与权限

## 装配

`OrgModuleConfiguration` 导入 `OrgRuntimeConfiguration`；后者在
`auriga.service.schema=org` 时启用（见 `platform-app` 的 `org` profile：
`application-org.yaml`）。运行时角色与迁移角色分离，见 [common.md](common.md)。

```yaml
auriga:
  service:
    name: org
    schema: org
  errors:
    message-bundles: [messages/org-errors]
spring:
  flyway:
    schemas: org
    locations: classpath:db/org
```

合并部署时 org 与 iam 各用自己的 DataSource / schema / outbox；同进程本地装配时，
org 注册的 `PermissionResolver` 与 `IdentityPolicyHooks` 会替换 iam 的
`@ConditionalOnMissingBean` 默认空实现，令牌签发与 `invite_only` 登录走真实权限与邀请兑换。

## 表（schema `org`）

| 表 | 说明 |
|---|---|
| `tenant` | 租户；私有部署种子行 slug=`default`，id=`00000000-0000-0000-0000-000000000001` |
| `member` | 成员，id = iam 账号 id |
| `group` / `group_member` | 组与组员（可挂 parent） |
| `permission` | 权限目录（当前种子为 org 域） |
| `role` / `role_binding` | 角色（权限 key 集合）与绑定（member/group/service_account） |
| `policy` | 策略键 `workspace` / `egress` / `login` |
| `quota` | 默认 / 组 / 成员覆盖 |
| `invitation` / `invitation_redemption` | 邀请与兑换记录（幂等键 invitation+account） |

业务表均 `tenant_id` + FORCE RLS。登录前按 host 解析租户使用
`resolve_tenant_by_host` / `default_tenant`（SECURITY DEFINER，内部绑定默认租户以满足 RLS）。

## 权限模型

有效权限 = 账号直接角色权限 ∪ 所属组（含祖先组）上角色权限。

内置角色（不可改删）：`owner`、`admin`、`member`、`external`。
`owner` 含全部 `org.*` 管理权限；`admin` 不含 `org.tenant.write`。

超管上限：授予角色、加人进组、改组上级时，目标有效权限必须 ⊆ 操作者有效权限，
否则 `403 org.grant_exceeds_own`。撤销/停用最后一个 `owner` 绑定 → `409 org.last_owner`。

## 分组父链完整性

创建或更新分组指定父组时，服务端在当前租户内检查父组存在，并遍历完整祖先链。
自指、挂到自己的后代、已有循环链返回 `409 common.conflict`；不存在或其他租户的
父组，以及祖先链断裂，统一返回 `404 common.not_found`。查询带显式租户条件并受
FORCE RLS 约束，不探测其他租户。祖先查询使用递归 `UNION` 去重，坏链也会终止，
不依赖前端过滤或数据库超时来判环。

`createGroup`、`updateGroup`、`deleteGroup` 在读取结构和进行权限检查前，先在同一
事务内以 `UPDATE tenant SET id=id WHERE tenant_id=? AND id=?` 锁住当前租户行，直到提交或回滚。
同值写入保留租户字段不变，并使 PostgreSQL 建立新的行版本。PostgreSQL 默认
`READ COMMITTED` 下，等待者取得锁后的查询能看到上一笔已提交的结构；同租户并发
互挂只能有一笔成功，创建/重挂也不能与删除父组交错留下悬空引用。锁不会锁住其他
租户行。外层 `REPEATABLE READ` 事务持有旧快照时，同值写入产生的并发写冲突会
回滚为 `409 common.conflict`，不进行内部重试。当前迁移未对租户表安装业务触发器；
该写入不调用租户更新事件。保留原有授予权限上限和事件行为；本变更不新增映射组
规则或数据库迁移。

`parentId` 为 `null` 或省略时仍保持现有父组；本批不增加清除父组语义，该契约缺口
需另行处理。合法重挂、仅改名称/描述和创建无父组分组继续可用。

`OrgGroupHierarchyTest` 使用真实嵌入式 PostgreSQL 和非表所有者运行时角色验证三层
成环、自环、并发互挂、创建/删除竞争、跨租户与缺失父组、已有坏链及权限上限。
竞争测试用闩锁控制事务提交并检查数据库锁等待；坏链用例设置事务级
`statement_timeout`，防止回归使测试无限等待。

## 配额读写完整性

组和成员配额的 GET/PUT/DELETE 都在事务内核对当前租户目标。不存在、其他租户
以及只有历史孤儿覆盖而没有目标的情况，统一返回 `404 common.not_found`，不会
通过管理员连接或跨 schema 查询探测其他租户。目标已验证存在但未设置覆盖时，
GET/DELETE 保持既有 `404 org.not_found`，与缺失目标的错误码区分；默认覆盖缺省
返回 `{}`，默认 DELETE 保持既有 `409 org.conflict`。

PUT 和 DELETE 先取得上述租户结构锁，再校验目标及读写覆盖，与正式组删除采用
相同锁顺序。首次并发写同一覆盖会依序完成，避免 UPDATE→INSERT 唯一键竞争；
组删除与配额写入也不能交错留下孤儿覆盖。PUT 仍是没有 ETag 的整层替换，后提交
的合法写入可以覆盖前一层内容，不提供多管理员编辑冲突检测。

已知字段按正式 Quota schema 校验：根、`instance` 和 `model` 是 object；
`cpuMillis`、`memoryMb`、`diskGb` 是不小于 100、256、1 的整数，不能为 null；
`maxInstances`、`monthlyBudgetMicros`、`dailyTokens` 是非负整数或 null；
`alwaysOnAllowed` 是 boolean；`currency` 匹配三位大写字母。拒绝数值字符串与浮点
节点，不做截断或取整。省略、null、0、false 各自保留。schema 没有禁止额外属性，
根和 section 的未知字段原样往返，不使用全字段白名单。
多组间未知字段没有契约定义的放宽排序；混合类型值保留原节点，不尝试强转成
boolean 或整数，也不以转换失败产生 0/false。

HTTP 使用 JSON 树和 PostgreSQL jsonb 存取整数，不套用生成 DTO 的 Integer 范围，
也不套用 JavaScript 的 `2^53-1` 上限；大于 int64 的可存储整数仍能精确读写及比较。
多组有效配额按原数值节点作精确大小比较，保留成员→多组最宽松→默认的逐字段优先级，
null 表示不限并优先于组内具体数值。gRPC 的已知数值字段是 optional int64，超出
int64 的有效值明确返回 `common.validation_failed`，不会截断或当作不限，HTTP 数据
保持原值。历史非法根、section 或已知字段在读取时同样安全返回该校验错误，不改写
或清理原数据。

本服务没有可信的 gateway 部署币种输入，因此只核对 `currency` 形状；部署币种
一致性仍需后续接线，不新增跨服务调用或配置，也不在本批增加模型预算执行。

`OrgQuotaIntegrityTest` 使用真实 PostgreSQL、非表所有者运行角色及 tenant RLS，
覆盖目标隔离、合法整层替换、类型/最小值/null/0/false/未知字段、大整数与 gRPC
边界、有效合并、首次并发写入、组删除竞争及事务回滚。并发测试以闩锁与数据库锁
等待观察控制交错，不使用 sleep。

## 事件

经 common `EventPublisher`（outbox）：`user.created` / `user.updated` /
`user.disabled` / `user.enabled` / `user.offboarded` / `user.permissions_changed`，
payload 见 `contracts/events/user.schema.json`。

## 与 iam 接线

| Bean | 说明 |
|---|---|
| `OrgPermissionResolver` | 实现 `iam.api.PermissionResolver`；签发访问令牌时写入真实 `perms` |
| `OrgIdentityPolicyHooks` | 实现 `consumeInvitation`；优先读 `PendingAccount` 或 `profile.accountId` |

当前 iam `LoginManager` 调用 hook 时未传入 pending account id。临时桥：

1. `org.api.PendingAccount.call(accountId, …)`（测试 / 将来 iam 包装），或  
2. 在调用前把 `accountId` 写进 ExternalProfile（建议主控改 iam 一行）。

gRPC：`TenantService`、`MemberService`、`AuthorizationService`、`InvitationService`、
`QuotaService`、`PolicyService`（契约 `contracts/proto/auriga/org/v1/org.proto`）。

HTTP：`/api/org/v1/**`，`/admin/**` 使用 `@AdminOnly` + `@RequiresPermission`；
`GET /invitations/preview/{code}` 为 `@PublicEndpoint`。

## 权限 key 清单（均在 OpenAPI）

`org.tenant.write`、`org.members.read|write|offboard`、`org.groups.read|write`、
`org.roles.read|write|grant`、`org.policies.read|write`、`org.quotas.read|write`、
`org.invitations.read|write`。
