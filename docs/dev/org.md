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
