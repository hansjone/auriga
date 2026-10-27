# T3b：生产来源链装配驱动

这是隔离集成驱动，不是 Spring/安装器、真实 DSH、浏览器登录或 SEND 验收。
唯一宿主上下文夹具来自插件现有 `host/native/fixtures.js`，不属于 T4。
设备 HTTPS、Org/issuer、根批准公钥、IAM/collab/PG 均来自原 T3a Java 子进程。
插件使用生产 device-provider、connection-runtime、NatsTransport、file-ledger、
typed-local 登记和 source controller，不注入全业务 provider 或假业务响应。

关闭阶段只在自有子进程实际触发 close 后记录，包含原始 exitCode/signal；
非零退出也保留 gateway-closed/java-closed 供诊断，同时保留失败。
验收仍要求 gateway/Java 退出码为 0、无终止信号；仅有关闭阶段不算清理成功。

## 执行

从本工位根目录保留派工环境，先完成非 PG 编译及工具守卫：

```powershell
.\mvnw.cmd -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false'
.\mvnw.cmd -B -pl services/platform-app -am spotless:check
node --test e2e/network/assembly-guards.test.mjs
powershell -NoProfile -File services/platform-app/src/test/resources/network-source-history/test-java-bridge-verifier.ps1
$head = (git rev-parse HEAD).Trim()
powershell -NoProfile -File services/platform-app/src/test/resources/network-source-history/verify-source-assembly.ps1 -ExpectedHead $head -CompileOnly
```

最后一个工具从干净精确 HEAD 制作 archive，各自按锁安装独立 node_modules，
typecheck Control/SignatureBypass，保存 archive/源码/测试 SHA256、命令日志和退出码。
CompileOnly 不启动 PG，不声称业务反证。原 T3a 三项 PG/OrgBypass 工具保留原判据。

主控在能正常启动 PG 的环境执行单次 Control，或原七组、新增五组关键故障及单坏实现：

```powershell
node e2e/network/source-assembly.mjs --run
powershell -NoProfile -File services/platform-app/src/test/resources/network-source-history/verify-source-assembly.ps1 -ExpectedHead $head -Run
```

驱动从本工位新五例 Surefire XML 取完整 classpath，拒绝旧源码/class、其他工位
classpath、失败/skip；直接启动原 `NetworkSourceJavaHost`。缺环境失败，不默认 skip。

## 已写入的七组检查（须主控真实运行）

- 两个插件各经 HTTPS register/tickets、Broker callout→generated Redeem，C1 bootstrap
  READY 后关闭，换真实 C2 normal READY；各有独立私有 ledger/cwd。
- MEMBER typed-local/source 首建，在 collab 已真实返回后丢外层响应，再终止 WSS
  建立下一完整 C，同 op reconcile；核两库原请求/回执字节不变、source 只一份、Get
  后才有插件 observation。这只覆盖 F2 外层丢包，不冒 IAM 保存前的 F1。
- 翻转真实 Get 回包签名位；观察每次实际 file-ledger commit，禁止保存相应 active
  observation，不增 IAM source/原回执；恢复真回包后 Get 正控成功。
- EXTERNAL 通过另一真实设备和独立 ledger 完成生产登记、来源和 Get。
- 按 IAM 已存 expires_at 真实等待（T3a source policy 为 180 秒），自然 EXPIRED/Get
  后创建新 op/后继；核前代、predecessor、revision、原回执字节。
- native 夹具关闭后先耐久 stop，再无版本 Revoke **当前 head**；核当前代 REVOKED、
  旧代仍 EXPIRED。完整 Message/SourceContext 的真实第五 RPC 先 allowed=true 正控，
  再停链后精确 PermissionDenied/common.forbidden；不发送 SEND。
- 原始 NATS 协议探针仍用真实设备票/callout，核消票重放、跨 ingress pub/sub ACL、
  8 subscriptions/65536 payload 账号限额；绕过 SDK 本地限额以测实际 Broker。

## 追加授权2：五组关键故障

Node 驱动仅向本次 Java 子进程传 `AURIGA_SOURCE_FAULTS=1`。
普通 `NetworkSourceJavaHost` 默认关闭钩子，原 T3a 三个 PG 用例/OrgBypass 行为不变。
启用后只装饰原生成服务，保留 @AuthenticatedOnly、原 common 拦截器、svc/tenant
校验及真正生产业务委托；没有独立认证/宿主或成功回包替身。
stdin 的 `fault <JSON>` 仅支持有界 source 方法/阶段/原 op 或 local 选择器、arm/release/
clear/trace；不允许 Redeem 或“允许成功”模式。hold 在业务事务外，服从真正 Context
取消/deadline；关闭释放闸锁。新的内层 trace 保留 generated 原请求/结果字节和委托顺序。

- 活 C 先以真实 Resolve 正控，再真正 Read miss/Fence 拒绝，IAM 五张来源事实表逐字
  零写。另用真实 typed-local 产生未提交 pending，核实际 Read/Fence、精确签名
  outcome_unknown、pending/outbox 保留、Register 不回退且没有迟到发行事实。
- 用正常 gRPC client deadline 和明确取消令牌终止已到达闸锁的 Read；核真实 context
  cancellation、无 source/receipt、来源事实零写；另有旧服务 UNIMPLEMENTED 拒绝。
- 只在实际 INSERT source operation 成功、真正 Connection.commit 返回之后注入
  SQLException/08006，制造数据库提交确认未知；不写假事实、不预塞回执。核 IAM
  已提交原回执、collab 仍 pending，换真实 C 后只纯 Read/Get 恢复，原请求/响应不变。
- 持有真正 ACTIVE IAM Get 回包，允许独立 Revoke 先提交，再释放晚到回包；核准确
  的 signed stopped fact、无 valid_until、collab 许可截止为空、插件 observation 不复活。
  历史 observation 的原响应字节可保留；它不等于活许可。
- 同一真实 typed source 经 generated Get 正控后，调用真实 OrgService 删除 EXTERNAL
  角色绑定，再核完全相同 Get 为 PermissionDenied/common.forbidden 且不泄漏回执。

`critical-inner-rpc.json` 和 `inner-rpc.json` 增加内层调用、闸锁和实际提交确认丢失证据。
新 Go 控制端口只支持 generated Read/Fence/Get 和对应 call-id 取消，故意不提供 Register。
所有上述业务正控/故障结果仍须主控实跑；非 PG hook 单测仅证明机制，不冒业务验收。

## 来源、进程与时钟

`infra/ai-gateway/cmd/network-source-assembly` 启动实际 NATS **2.15.1** 外部二进制，
使用仓库既有 Windows/Linux amd64 SHA256 和版本双重钉死。Go 的 2.12.6 仅用于
生产配置解析/既有模块测试；旧 compose 2.10 不参与，不混报已验版本。
生产 network.StartRuntime 使用同一 Broker 配置。透明 generated relay 保留 JWT、
delegation、deadline/cancel、完整帧，指定故障只丢包/翻签名，不决定业务。
每 RPC 重读真实服务 JWT 文件，180 秒真实刷新设备和服务令牌，不延长 300 秒有效期。
TLS 仅给本次子进程信任临时 CA，不关闭校验或改系统信任，不注入业务时钟。

DB 快照使用本次 PG 的 bridge_iam_app/bridge_collab_app 运行时角色、READ ONLY/
REPEATABLE READ 与 tenant RLS，只读固定来源表，不查凭据表；保留完整 protobuf
字节，以 generated 类型解码，数值保持精确十进制字符串。两个 schema 分别一致，
不声称跨 schema 原子快照。

Java ready 记录 PID/创建时间、PG 根谱系/目录/端口；Broker 记录 PID、Windows 创建
时间或 Linux boot+start-ticks 身份及启动墙钟。关闭顺序插件→gateway/Broker→Java/PG，
按自己的进程身份等待/清理，核 Broker 端口和 T3a 原关闭断言。清理失败不算通过。

`e2e/network/target/assembly-*` 保留 manifest、真实双向帧、generated RPC、两份 ledger/
file-commit observations 及 DB 快照。stdout/logs 保存前检查已知凭据泄漏；CONNECT
不记录，凭据只在 T3a 私有目录，不上 argv/共享 profile，关闭删除本次临时根。

## 单业务坏实现

只让生产 source-control/connection.js 跳过 Get 验签，保留 Register/Revoke 和结构检查，
必须通过相同插件 typecheck，两个执行使用相同测试文件。
唯一接受的反证是 ERR_ASSERTION：`corrupt Get must not persist an active observation`，
并且 Java/gateway 正常关闭。失败也保留真实文件 commit observation/回包/DB 事实。
编译、SQL、初始化、其他断言或清理错误一律不算业务反证。

## 旧头首失与待办

主控旧头 3726763 的 archive Control 首失是 prepare 参数错误和快照假定所有表都有 id，
不是业务反证：现按真实 provider.prepare({signal}) 接线，并为复合主键 network_node
采用完整行稳定排序，新增 API/SQL 白名单守卫。旧归档保持原样，不重写其结论。

按追加授权2“其余待办，不必一次做全”：F5 tenant/owner/device/backing-session 七字段
换值、F6 digest/ref/异体全套、F7 collab SQL 提交确认未知、真正 kill/同 PG 目录重开及
PG 写围栏；P3 双 op 争 CAS、P5 **以 EXPIRED 前代撤销当前后继** 仍待补。
本轮没有新增 PG 停启接口；保留原宿主生命周期及全部清理验证。
原 37/558 例不替代新增链；真实 PG Control/坏实现、新非作者独审和精确头 CI 均由主控完成。
