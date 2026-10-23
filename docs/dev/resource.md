# 资源只读服务（R1A）

## 已实现的接口

沿用 `contracts/openapi/resource.yaml` 的四个只读GET，响应使用该契约生成的Java模型：

- `/api/resource/v1/packages`：当前主体可见插件包，参数只有 `limit`、`cursor`。
- `/api/resource/v1/packages/{packageId}`：包元数据与revision ETag。
- `/api/resource/v1/packages/{packageId}/versions`：包内可见版本，使用同样的分页参数。
- `/api/resource/v1/packages/{packageId}/versions/{versionId}`：精确包/版本元数据与revision ETag。

成功响应均为 `Cache-Control: no-store`。这四个接口只提供目录与版本元数据；没有上传、下载、签名、安装或分发操作，不返回伪造签名。可选 `resourceEnvelope` 暂不提供，需后续可信发布流程登记真实签名后再实现；`releaseSequence` 只在数据库确有值时返回。

接口先检查令牌权限，再通过受信 `ResourceAccessInputs` 在线读取Org成员、组、有效权限和workspace个人插件策略，旧令牌中的成员类型、组与权限不会替代实时检查。租户来自现有认证过滤器及 `TenantContext`，不从查询参数或客户端租户头读取。当前实现面向有有效Org成员身份的user/device主体；没有成员映射的服务账号拒绝，后续如需服务账号目录访问须先明确其细粒度ACL主体规则。

## 可见性与数据库

resource只访问自身schema，使用独立的迁移角色与运行角色。所有业务表都带 `tenant_id` 和UUID主键，启用并强制RLS；读取事务使用 `TenantTransactionManager` 设置事务本地租户。包组关系和包内版本通过复合租户外键约束，默认版本也必须属于同租户同包。

- `tenant`包含本租户external；`members`仅正式成员；`groups`必须匹配当前实际组；`private`仅包owner。owner或管理员身份不绕过其他可见范围。
- L0–L3沿既有可见范围读取；L4同时要求private、本人和 `allowPersonalPlugins=true`。数据库拒绝L4共享可见范围。
- `enabled`表示总部包可用状态，禁用资源的目录信息仍对已授权主体可见；不表示实例插件是否运行。
- 未发布状态（draft、pending_review、approved、rejected）的版本只对该版本author可见；published、deprecated、withdrawn在包ACL内可读。撤回版本仅展示历史元数据，不因此获得下载或新安装权限。默认版本若对当前主体不可见，返回 `defaultVersionId:null`，不泄露私有草稿ID。
- 包/版本不可见、跨租户和版本不属于路径包均返回 `404 common.not_found`。主体缺权限/已停用/离职/不存在返回403；依赖服务或策略输入不可用时返回503，不默认公开。

本批没有生产演示seed和生产写API。后续发布模块必须在自己的任务里实现制品验证、版本不可变内容、审核/签名/撤销状态机、审计及outbox，不能把本批元数据表视为这些流程已经实现。

## 分页

`limit`为1–200，默认50。先执行ACL过滤，再按不可变UUID升序keyset读取 `limit+1`；首请求固定当前可见ID上界，后续请求不会插入更高的新ID。只在确有下一项时返回非空 `nextCursor`，终页省略；输入空游标与未提供游标一样表示重新开始。

游标为服务端HMAC-SHA256认证的不透明值，绑定租户、账号、接口/包、limit、末项、上界与15分钟期限；长度上限2048，严格base64url编码，不信任客户端修改的ID或授权信息。非法、篡改、过期、跨主体/租户/包/接口或更改limit均400 `common.validation_failed`。每页重新检查当前成员、权限与ACL；撤权立即影响下一页，不将第一页的授权存入游标。若目录或ACL在页间变化，应刷新列表获取新视图；这不是跨请求数据库快照或离线缓存承诺。

所有副本使用同一外部配置的游标签名密钥，轮换密钥使旧游标失效，客户端重新读取第一页。密钥只在服务端使用，不是成员访问凭据。

## combined装配

`PlatformResourceConfiguration`是独立装配类；resource模块不依赖org.impl，combined边界适配器通过既有 `OrgService` 读取信息，不跨schema直查。默认仍关闭resource；显式启用时须配置下列环境变量：

```text
AURIGA_RESOURCE_ENABLED=true
AURIGA_RESOURCE_JDBC_URL=jdbc:postgresql://db.example.com:5432/auriga
AURIGA_RESOURCE_MIGRATION_USER=auriga_resource_owner
AURIGA_RESOURCE_MIGRATION_PASSWORD=<通过部署秘密传入>
AURIGA_RESOURCE_DB_USER=auriga_resource_app
AURIGA_RESOURCE_DB_PASSWORD=<通过部署秘密传入>
AURIGA_RESOURCE_CURSOR_SIGNING_KEY=<至少32字节随机密钥的标准base64>
```

先由部署操作者创建非superuser、非BYPASSRLS的owner/runtime角色：owner拥有resource schema并运行Flyway；runtime只有resource schema USAGE与必要表权限，不能拥有业务表、访问其他schema或关闭RLS。为owner创建表设置默认权限，按现有 `docs/dev/common.md` 的双角色流程操作。本批不自动创建数据库角色，也不修改任何部署环境。Flyway先迁移resource内common对象，再执行独立 `db/resource/V1__resource.sql`。

非combined进程只有在 `auriga.service.schema=resource` 且提供受信 `ResourceAccessInputs` 时才注册运行组件；本批未提供独立部署远程Org适配器，缺输入时启动失败，不退回匿名或JWT组快照。combined已有适配可直接使用，无需改IAM、edge或成员API契约。

## 验证与集成限制

相关测试使用本机嵌入式PostgreSQL、随机loopback HTTP端口、本次生成ES256密钥和生产JWT验证器；Org与resource均执行真实SQL，采用独立非超级用户运行角色。测试fixture仅由测试写入，生产没有模拟后端或演示数据。

```sh
./mvnw -B -pl services/platform-app -am test -Dtest=Resource*Test -Dsurefire.failIfNoSpecifiedTests=false
./mvnw -B -pl services/resource/api,services/resource/impl,services/platform-app spotless:check
```

Windows使用 `mvnw.cmd`，按现有Auriga开发环境注入工具路径。测试结束关闭HTTP、连接池及数据库进程。

R1A不新增DSH managed成员API桥接，不改变设备令牌audience。PLUGIN1的市场UI已有消费者，但在P3/受审访问通道接好前仍应显示能力不可用。真实安装管理闭环还依赖分发、workspace复核、节点下载/验签/实际应用、权威安装清单与配置契约；不能以本批四个GET通过代替该闭环。
