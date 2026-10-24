# 邀请目标的事务完整性

邀请中的 `role_ids`、`group_ids` 是 UUID 数组，数据库没有为数组元素建立外键。
RLS 只约束当前行的租户，不能替代对数组引用目标的存在性检查。

## 范围与顺序

本批只处理邀请创建和兑换中的角色、组引用，不改变目标删除策略、邀请者离职或撤权规则、
角色权限变更后的授权规则。`provider_ids` 仍由 IAM 权威管理；Org 没有查询这些目标的权威接口，
因此保留已有 provider 限制行为，不跨 schema 自行验证。

生产修复应复用 `OrgStore.lockGroupHierarchy()` 的租户同值 UPDATE，且必须在读取结构状态前加锁。
它与组删除、ROLESAFE1 的角色删除使用同一租户行，持锁到外层事务结束；RR 外层旧快照遇到
并发结构写入时返回现有 `common.conflict`，不能在旧快照里重试。

创建顺序：先锁租户行，再保留原有授权上限与可信 DeploymentAccess 校验的次序；之后检查每个
角色、组属于当前租户且存在，最后保存 hash 和邀请。不存在与外租户目标统一使用 `org.not_found`。

兑换顺序：先锁租户行，再刷新邀请状态、锁邀请行、查询原账号兑换记录。已兑换账号直接返回
当前成员，保持邀请耗尽或目标后来删除后的幂等。首次兑换继续已有状态、期限、邮箱、provider
限制检查，然后复核每个角色、组目标，最后才允许写成员、绑定、组成员、兑换记录、使用次数和
Outbox。目标失效必须整体回滚，不能消耗名额或留下成员、关系、事件。

## 真实 PostgreSQL 验证

专属 `OrgInvitationIntegrityTest` 使用 test-support 嵌入式 PG、`org_owner` 迁移和非所有者
`org_app` 运行角色，显式确认运行角色不是 superuser / BYPASSRLS，且没有 tenant 谓词的查询也
读不到外租户目标。事件通过真实 `OutboxEventPublisher` 写入同一数据库事务并验证事件契约。

竞争用闩锁、数据库 `pg_stat_activity` 锁等待证据和有限超时驱动，不用 sleep。两顺序分别是：
删除先提交时兑换失败且没有新关系；兑换先提交时删除清除已提交关系。RC/RR 旧快照用已开始的
外层事务先读，再由另一事务提交删除，之后继续创建或兑换。

基线 `f0210c91bd6efa231f72d7172d5813864d8acbd7` 的原生产源码可由 git 对象精确恢复。
该基线尚未包含 ROLESAFE1 的角色删除锁；角色竞争结果需与该前置依赖分开解释。
实际修复基于已合入 ROLESAFE1 的 `17e7d59fcd2ee0529fc171712f91820051271268`。

先红核证（生产源码未改）：

- 首轮 `OrgInvitationIntegrityTest`：21 例，18 失败、0 错误、0 跳过。
  缺失/跨租户目标创建 4 例未抛异常；删除后兑换 2 例仍成功；删除/兑换两顺序 4 例均提交；
  RC/RR 外层旧快照 8 例均提交。RLS、合法兑换与删除后幂等、调用者回滚 3 例通过。
- 补充只跑删除/兑换竞争：4 例，4 失败、0 错误、0 跳过。
  每种目标的两顺序都没有等待，并实际各留下 1 条悬空 `role_binding` 或 `group_member`。
  这证明问题不只是缺少显式存在性检查或锁等待的理论推断。
- 新增边界断言定向运行：7 例，6 失败、0 错误、0 跳过。每个创建引用都必须存在、历史
  外租户数组不得兑换、既有成员遇到失效目标不能继续消耗次数，各 2 例都复现成功提交；
  补充可信 URL 与 code hash 断言的合法兑换/删除后幂等用例通过。
- 首失和原源码保留在工位 `services/org/impl/target/invitation-evidence/`：
  `first-red.log`、`first-red.xml`、`OrgService.f021.java.txt`，补充为 `orphan-red.log/xml`。
  新边界为 `extended-red.log/xml`。原源码也可通过上述基线 git 对象复原。生成证据不进入提交。

红测命令（Windows 先加载工作区的 `auriga-env.ps1`）：

```powershell
.\mvnw.cmd -B -pl services/org/impl -am test '-Dtest=OrgInvitationIntegrityTest' '-Dsurefire.failIfNoSpecifiedTests=false' '-Dspotless.skip=true'
```

修复后在 `17e7d59` 基线上一次集中验证：110 例，0 失败、0 错误、0 跳过，构建成功。
其中邀请专属 27、角色完整性 40、原 OrgCore 21、组层级 8、配额完整性 14；未重跑全仓。
四个删除/兑换竞争均确实等待首事务后完成，目标失效的首次兑换无成员、关系、次数或 Outbox
残留，目标删除后的原账号重试仍幂等。`spotless:check` 与生产及新增文件的 diff 空白检查通过。
证据为同目录的 `final-tests.log`、`final-TEST-*.xml`、`final-format.log`。

```powershell
.\mvnw.cmd -B -pl services/org/impl -am test '-Dtest=OrgInvitationIntegrityTest,OrgRoleIntegrityTest,OrgCoreTest,OrgGroupHierarchyTest,OrgQuotaIntegrityTest' '-Dsurefire.failIfNoSpecifiedTests=false'
.\mvnw.cmd -B -pl services/org/impl spotless:check
```

本批依赖 ROLESAFE1 的角色删除锁。租户同值 UPDATE 会把同租户邀请写入与结构变更串行化，
这是本次最小修复的并发代价；未新增隔离级别或公共事务层重试。
