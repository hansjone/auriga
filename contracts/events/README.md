# 事件契约 v1

- 格式：CloudEvents 1.0 结构化 JSON，见 `envelope.schema.json`。
- 投递：业务数据和事件在同一个数据库事务里写进 outbox 表，转发器投递到 NATS JetStream。消息头 `Nats-Msg-Id` 等于事件 id；消费方按事件 id 落库去重（至少一次投递，处理必须幂等）。
- 主题：业务事件 `auriga.evt.<type>`（流 `AURIGA_EVENTS`）；审计 `auriga.audit.<服务>`（流 `AURIGA_AUDIT`）。
- 顺序：同一个 `subject`（资源 id）的事件按发生顺序投递；不同资源之间不保证顺序。消费方不要依赖跨资源的顺序。
- 版本：`dataversion` 是 data 结构的版本；兼容修改不改版本，不兼容修改发新版本并在一段时间内双发。

## 事件目录

| type | 发布方 | data schema | 主要消费方 |
|---|---|---|---|
| `user.created` | org | `user.schema.json#/$defs/UserCreated` | iam、workspace、audit |
| `user.updated` | org | `user.schema.json#/$defs/UserUpdated` | iam（同步显示名、邮箱）、workspace |
| `user.disabled` | org | `user.schema.json#/$defs/UserDisabled` | iam（吊销会话和设备）、workspace（停实例）、edge |
| `user.enabled` | org | `user.schema.json#/$defs/UserEnabled` | iam |
| `user.offboarded` | org | `user.schema.json#/$defs/UserOffboarded` | iam、workspace、resource（转交）、ops（离职流程） |
| `user.deleted` | org | `user.schema.json#/$defs/UserDeleted` | 所有保存了用户副本的服务 |
| `user.permissions_changed` | org | `user.schema.json#/$defs/UserPermissionsChanged` | iam（刷新访问令牌） |
| `identity.login` | iam | `identity.schema.json#/$defs/IdentityLogin` | audit、collab（应急账号登录通知超管） |
| `identity.login_failed` | iam | `identity.schema.json#/$defs/IdentityLoginFailed` | audit |
| `identity.linked` | iam | `identity.schema.json#/$defs/IdentityLinked` | audit |
| `identity.unlinked` | iam | `identity.schema.json#/$defs/IdentityUnlinked` | audit |
| `identity.mfa_changed` | iam | `identity.schema.json#/$defs/IdentityMfaChanged` | audit、collab（通知本人） |
| `identity.session_revoked` | iam | `identity.schema.json#/$defs/IdentitySessionRevoked` | edge（清内存缓存）、audit |
| `instance.created` | workspace | `instance.schema.json#/$defs/InstanceCreated` | audit |
| `instance.started` | workspace | `instance.schema.json#/$defs/InstanceStarted` | audit、计量 |
| `instance.stopped` | workspace | `instance.schema.json#/$defs/InstanceStopped` | audit、计量、collab |
| `instance.failed` | workspace | `instance.schema.json#/$defs/InstanceFailed` | audit、collab（通知本人和管理员） |
| `instance.deleted` | workspace | `instance.schema.json#/$defs/InstanceDeleted` | audit |
| `audit.recorded` | 所有服务 | `audit.schema.json#/$defs/AuditRecorded` | audit |

以后的里程碑会加 `resource.*`、`knowledge.*`、`usage.*`（计量）等类型，按同样规则登记在这里。
