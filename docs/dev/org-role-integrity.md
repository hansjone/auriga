# 角色绑定目标与最后所有者完整性

## 不变量与范围

`OrgStore.countOwners()` 是本次修复的唯一所有者计数口径：同租户，
`role_binding.subject_type='member'`，关联角色 `key='owner'`，关联成员
`status='active'`，按 `subject_id` 去重。组继承的 owner 和
`service_account` 绑定不计入；不额外按 `member_type` 增加条件。

当租户原本至少有一个活跃直接 owner 时，通过成员停用、离职、直接角色撤销的正常并发调用，
不能让最后一个活跃直接 owner 消失。本修复不负责修复已有零 owner 数据，也不新增所有者政策。

创建 `member` 或 `group` 角色绑定时，角色与目标必须在当前租户真实存在。
缺失目标与其他租户目标均返回既有 `org.not_found`，不暴露目标所属租户。
目标存在但要删除的绑定不存在仍为 `org.not_found`；原有 `org.last_owner`、
`org.role_builtin`、权限上限、敏感授权路径和事件保留。

## 锁顺序与事务

复用 `OrgStore.lockGroupHierarchy()` 的现有租户结构锁：

```sql
UPDATE tenant SET id=id WHERE tenant_id=? AND id=?;
```

方法名保留以减少变更；该锁已经用于组层级和配额写入，现在也协调角色绑定与成员状态写入。
顺序统一为：先取得租户锁，再读取目标/成员/绑定和计数，随后写入业务行，最后写入事务 Outbox。
锁持有到事务提交或回滚，不能先读所有者计数再加锁。

覆盖的生产路径：

- `disableMember`、`offboardMember`：锁后读取成员、判断最后 owner，再改状态。
- `deleteRoleBinding`：锁后读取绑定、判断最后 owner，再撤销。
- `updateMember`、`enableMember`：锁后读取成员，防止整行保存把并发状态变更覆盖成旧状态。
- `createRoleBinding`：锁后读取角色并确认当前租户的 member/group 目标，再执行原有授权检查和插入。
- `deleteRole`：与绑定创建同序加锁，避免删完角色后另一事务插入悬空角色引用；内置角色保护保持原样。
- 正式 `deleteGroup` 已持同一把锁，绑定目标检查与组删除因此不能交错留下悬空绑定。

`updateRole` 不修改角色 key，且原有规则拒绝修改内置 owner；`deleteRole` 也拒绝删除内置角色。
正式组删除只清理 group 绑定，不减少上述直接 owner 计数。邀请路径只新增成员/绑定，
不减少该计数，本次没有修改邀请逻辑、构造器或运行配置。

在 READ COMMITTED 下，等待者取得锁后重新读取已提交状态，第二个移除最后 owner 的调用得到
`org.last_owner`。在 REPEATABLE READ 下，若外层事务已有旧快照，租户同值 UPDATE 会触发
PostgreSQL 序列化冲突；现有锁实现转成 `common.conflict`，由外层事务回滚，不在旧快照中重试。
测试还确认失败事务在调用服务前写入的其他成员字段一起回滚。

tenant 字段、tenant 更新事件、事务隔离级别和数据库迁移均未改变。

## PostgreSQL 验证

专属测试为 `services/org/impl/src/test/java/auriga/org/OrgRoleIntegrityTest.java`。
使用 `PostgresExtension.serviceRoles` 创建独立测试数据库；业务调用通过非表所有者
`org_app` 执行，确认无 superuser/BYPASSRLS、无租户上下文时不可见业务行。
管理员观察连接只用于观测 PostgreSQL 锁等待，不执行被测业务变更。

竞争测试先让第一个事务完成业务变更但暂不提交，再开启第二个事务并固定可见的旧状态。
通过 `CountDownLatch` 和 `pg_stat_activity.wait_event_type='Lock'` 确认交错后释放第一个事务。
不用 sleep；SQL 超时为 10 秒，闩锁/任务等待均有有限超时。

2026-10-24 在基线 `afeb018` 上先执行了 24 项红测：

- 缺失/跨租户 member、group 四种目标均被成功绑定。
- revoke、disable、offboard 的九种有序组合，在 RC 和 RR 下共 18 种竞争均出现两个事务提交，
  最终 `countOwners()=0`，事务事件探针记录两条事件。
- 先删组再绑定、先绑定再删组，两种交错均留下悬空绑定。

修复后的专属测试共 40 项，使用正式 `OutboxEventPublisher` 和 `EventSchemas`，覆盖：

- 上述目标隔离与 18 种竞争；竞争的发起者为另一个真实管理员成员，避免自停用/自离职限制干扰。
- 最终至少一个活跃直接 owner、被拒绝的成员/绑定不变、失败事务的先行写入回滚、仅提交成功方事件。
- 最后 owner 的串行拒绝、合法成员/组绑定及撤销、权限上限、内置角色保护、组继承 owner 不计入直接 owner。
- 普通成员停用/启用/元数据更新/离职、版本与 If-Match、成员状态与元数据竞争。
- 调用者主动回滚时业务行与正式 Outbox 一起回滚；租户字段不变。
- 组删除、角色删除与绑定创建的两种顺序竞争。

复现命令（先点源工作区环境；首次构建需要项目既有契约生成步骤）：

```powershell
. D:\project\chatgpt\.agents\auriga-env.ps1
mvn -pl services/org/impl -am '-Dtest=OrgRoleIntegrityTest' '-Dsurefire.failIfNoSpecifiedTests=false' test
```

生成依赖已就绪后可用 `-Dskip.installnodenpm=true -Dskip.npm=true` 跳过重复 npm 初始化。
集中回归还包含 `OrgCoreTest`、`OrgGroupHierarchyTest`、`OrgQuotaIntegrityTest`、
`OrgIdentityPolicyHooksTest`，无需修改这些测试。
2026-10-24 集中回归共 77 项（专属 40、Core 14、Hierarchy 8、Quota 14、Hooks 1），
失败/错误/跳过均为 0；Org 模块 Spotless 检查和 `git diff --check` 通过。

## 保留的边界

`service_account` 的权威目标存在性查询不在当前 Org API 内，本次保留其现有绑定行为，
不伪造校验、不跨 schema 查询，也不新建所有者规则。此项需要后续明确服务账号归属和权威接口。
已有 `isOwner` 对非活跃成员的保守最后 owner 拒绝语义保留；本次没有重新定义它。
角色字段校验与重复绑定仍走既有逻辑和数据库约束，不在本次扩展。

同一租户的相关写入现在串行执行；不同租户使用不同 tenant 行。
测试为隔离的临时 PostgreSQL 数据库验证，不涉及现场变更、服务部署或 PR 合并。
