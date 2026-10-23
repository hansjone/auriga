# 资源目录与元数据服务（R1A / R1META1）

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

生产没有演示seed。后续发布模块必须在自己的任务里实现制品验证、版本不可变内容、审核/签名/撤销状态机、审计及outbox，不能把本批元数据表视为这些流程已经实现。

## 创建与修改包元数据

`POST /api/resource/v1/packages`接收正式`PackageInput`字段：必填`name`、`kind:"plugin"`、`visibility`，以及可选`groupIds`。`private`创建本人L4个人包，要求实时`allowPersonalPlugins=true`；其他可见范围创建本人L3待审包。tenant和owner来自认证主体，新包固定`enabled:false`、`defaultVersionId:null`、`revision:1`，不会产生版本、签名或业务事件。响应使用生成的Package模型，状态201、ETag`"1"`及`Cache-Control: no-store`。

`PATCH /api/resource/v1/packages/{packageId}`只接受`PackagePatch`中的`name`、`visibility`、`groupIds`，至少一个字段。只允许当前本人可见的L3/L4包；L0–L2返回`resource.level_locked`。可见但属于其他owner返回403，不可见或跨租户返回404。L4不可改为共享，策略撤销后按原读取规则不可见。L3改成private仍是L3，不获得个人包等级。owner、tenant、kind、level、enabled、defaultVersionId、revision等额外字段一律400，不能通过普通端点改变包等级、可用状态或默认版本。

两项写操作均要求JWT和实时Org有效权限包含`resource.packages.upload`，并沿用`@RequiresRecentStepUp`检查600秒内的二次验证；缺少或过期返回`iam.step_up_required`。仅portal/admin上下文的user主体可写，仍须实时有效成员；管理员无owner或ACL绕过。设备/实例凭据、服务账号和AI令牌不能写入。写操作不额外要求`resource.packages.read`；读取仍单独检查该权限。

组可见范围必须提供1–200个不重复UUID，且全部属于当前主体实时、同租户的Org所属组。非groups范围不接受非空groupIds；从groups改为其他范围且省略groupIds时自动清除旧ACL。PATCH省略字段保持原值；保留groups范围时，旧ACL也必须仍全部属于当前主体。external不能创建或改为本人不可见的members范围。权限、成员、所属组及策略依赖失败均封闭拒绝。

请求只接受JSON对象，限制为32 KiB（包括无Content-Length的流式请求）；拒绝额外字段、重复JSON字段、尾随JSON、null、错误类型、无效UUID和重复组。name按Unicode码点校验1–214字符，不接受NUL。`If-Match`必须是单个、强ETag并精确等于当前revision（例如`"1"`）；缺失、旧值、weak、`*`、多值等均412。事务内先锁包行，再重新检查成员/权限/ACL，原子提交元数据、组ACL及revision+1；达到9007199254740991后返回`resource.state_conflict`，不溢出或丢更新。

### 创建幂等

POST的`Idempotency-Key`按现有契约可选；缺省时每次普通创建不同包，不生成或保存伪客户端键，也不承诺去重。提供时必须是单个1–128个不含空格的ASCII可见字符，空白、重复头或超长等拒绝。沿正式24小时语义，有键操作的作用域为`(tenant,actor,POST,/api/resource/v1/packages,key)`；规范化指纹包含name、kind、visibility和排序后的groupIds。字段顺序、UUID字母大小写、组顺序，以及省略与显式空groupIds不改变创建语义。同键异体409；同体返回原操作的201、Package和ETag，即使该包后来被PATCH，也不重写现包或返回其新revision。

`V2__package_creation_idempotency.sql`新增resource自身的`resource_package_creation`表，带tenant RLS。事务级advisory lock按作用域序列化请求，唯一约束精确区分作用域；记录与新包、ACL在同一事务提交。过期键在锁内可创建新包并替换记录，未命中的过期记录暂无后台清理器。每次重放在锁后重新检查实时成员、上传权限、个人策略及请求中的组授权，不以缓存结果绕过撤权；缓存仅存包元数据，无凭据或制品内容。

可验收演示为真实`POST创建 → GET读取 → 带ETag的PATCH修改 → GET复读`，包括对其他成员的可见范围变化。它不提供版本上传、发布、启停、安装或配置，不代表完整资源发布/插件安装闭环。

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

先由部署操作者创建非superuser、非BYPASSRLS的owner/runtime角色：owner拥有resource schema并运行Flyway；runtime只有resource schema USAGE与必要表权限，不能拥有业务表、访问其他schema或关闭RLS。为owner创建表设置默认权限，按现有 `docs/dev/common.md` 的双角色流程操作。本批不自动创建数据库角色，也不修改任何部署环境。Flyway先迁移resource内common对象，再执行独立`db/resource/V1__resource.sql`和`V2__package_creation_idempotency.sql`；V1保持不变。

非combined进程只有在 `auriga.service.schema=resource` 且提供受信 `ResourceAccessInputs` 时才注册运行组件；本批未提供独立部署远程Org适配器，缺输入时启动失败，不退回匿名或JWT组快照。combined已有适配可直接使用，无需改IAM、edge或成员API契约。

## 验证与集成限制

相关测试使用本机嵌入式PostgreSQL、随机loopback HTTP端口、本次生成ES256密钥和生产JWT验证器；Org与resource均执行真实SQL，采用独立非超级用户运行角色。测试fixture仅由测试写入，生产没有模拟后端或演示数据。

```sh
./mvnw -B -pl services/platform-app -am test -Dtest=Resource*Test -Dsurefire.failIfNoSpecifiedTests=false
./mvnw -B -pl services/resource/api,services/resource/impl,services/platform-app spotless:check
```

Windows使用 `mvnw.cmd`，按现有Auriga开发环境注入工具路径。测试结束关闭HTTP、连接池及数据库进程。

R1A不新增DSH managed成员API桥接，不改变设备令牌audience。PLUGIN1的市场UI已有消费者，但在P3/受审访问通道接好前仍应显示能力不可用。真实安装管理闭环还依赖分发、workspace复核、节点下载/验签/实际应用、权威安装清单与配置契约；不能以本批四个GET通过代替该闭环。
