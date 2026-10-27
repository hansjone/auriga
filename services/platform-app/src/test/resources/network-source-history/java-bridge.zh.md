# T3a Java 隔离桥

此桥只在测试 classpath 中装配生产 IAM、Org、collab/common、生成的 gRPC 与生产 HTTP 控制器。
独立原生 PG，iam/org/collab 各有 owner 迁移角色、runtime DML 角色和生产 FORCE RLS。
没有 Broker/WSS、浏览器登录、真实 DSH 见证、SEND 或发行安装；直接 generated Redeem 只算本桥验收。

## 编译及本机可运行检查

从当前 worktree 根执行，保留派工注入的 Java/Maven 环境：

```powershell
.\mvnw.cmd -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,GrpcTest,AccessTokenValidationTest' '-Dsurefire.failIfNoSpecifiedTests=false'
.\mvnw.cmd -B -pl services/platform-app -am spotless:check
powershell -NoProfile -File services/platform-app/src/test/resources/network-source-history/test-java-bridge-verifier.ps1
```

`NetworkSourceJavaHostTest` 仅用解码公钥 fixture 检验真实 TLS、生产安全链的拒绝及私有文件生命周期，**不计真实登录/成功注册/生产发行器证据**。
测试 CA 只有一天有效期，SAN 为 localhost/127.0.0.1；只交给本次 HttpClient/后续子进程信任。
不会更改系统信任、ACL、DNS、profile 或关闭 TLS 校验。

## T3b 子进程入口

先完成上述非 PG 检查，再在有真实 PG 启动权限的环境执行：

```powershell
powershell -NoProfile -File services/platform-app/src/test/resources/network-source-history/launch-java-bridge.ps1
```

薄启动器从 fresh Surefire XML 读取当前工位的完整 reactor test classpath，调用
`java -cp <classpath> auriga.iam.NetworkSourceJavaHost`。T3b 驱动也可直接用同一 classpath 启动该类，
重定向 stdin/stdout/stderr；只解析 `stage` JSON 行，其他行为生产启动日志。启动失败非零退出。
入口不接受参数，绝不将凭据放 argv。

`ready` 行只含随机回环 HTTPS/IAM gRPC/collab gRPC 地址、CA/配置路径与 PID/创建时间/PG 根谱系。
`bridge.json` 是本次私有临时目录内的配置：业务 tenant、realm、serverAudience、两份设备凭据路径
（`device` 为 MEMBER，`externalDevice` 为 EXTERNAL）、gateway/collab 服务 JWT 文件路径、公开 network 根与 pin、到期时间。
凭据不得复制到 stdout/报告/共享 profile。Windows 仅检查继承 ACL；不满足私有条件便失败，不自行修改 ACL。
本工位 Windows 沙箱继承额外读者，因此非 PG 用例验证拒绝写入并清理；这不证明本机能启动含凭据的完整桥。主控须选已有私有临时目录的正常权限环境运行，不能放宽 ACL 检查或修改 ACL。POSIX 临时目录 0700、文件 0600；文件在同目录原子替换，关闭删除全部本次文件。

stdin 控制命令：

- `refresh`：真实 OAuth refresh 两个设备身份，并经真实 InternalTokenIssuer 重发两个服务 JWT、原子替换私有文件；输出 `refreshed` 阶段行。
- `revoke-member`：真实 OrgService 删除 MEMBER 的角色绑定；仅供隔离验收，输出 `member-revoked`。
- `stop` 或 stdin EOF：先关 HTTPS 入口、mailbox gRPC、内部 channels、IAM gRPC、签名器、连接池/自有 PG，再删除临时文件并检查端口/自有 PG 谱系，输出 `closed` 和关闭顺序。

T3b 每个 RPC 重新读 JWT supplier 文件、设 deadline；在凭据到期前发 `refresh`，等待阶段回复后读取新文件。
桥内部所有 RPC 使用生产 AurigaClientInterceptor，supplier 每次直接调用真实 TokenService.issue。
业务 tenant delegation 和固定 svc 白名单由生产 common/server adapter 核验。
服务 JWT 与设备 access token 均为真实墙钟 300 秒；配置 `expiresAt` 不延长任何 JWT 的签名有效期。
唯一特殊时钟是 SigningKeys 初始发布阶段：发布时间回溯 31 秒，激活前切回真实 UTC；
它只服务初始密钥传播种子，不作用于 TLS、JWT 签发/验签、Session 或业务授权。

只初始化 realm/密钥/身份和角色配置；不预建节点、ingress、连接出处、source、chain/head 或原操作回执。
T3b 必须以设备 bearer 调生产 HTTPS register/tickets，再由真实 Broker callout 调 generated Redeem；
本项 Java 验收直接调用 Redeem，不能复用其结果冒充 Broker callout。
keys/announcement 使用 NetworkKeysController、真实 NetworkKeyOperations 持久发布、OfflineKeySigning 根批准，
业务签名由 NetworkSigningKeys 的生产 signApproved guard 约束。

## 主控真实 PG 与业务反证

Windows restricted-token PG87/3 工人不要重撞、换基座、skip 或改 timeout。
主控在正常权限环境，对同一精确 HEAD 执行：

```powershell
$head = (git rev-parse HEAD).Trim()
powershell -NoProfile -File services/platform-app/src/test/resources/network-source-history/verify-java-bridge.ps1 -ExpectedHead $head -Run
```

工具保留 `target/jb-*/manifest.json`、原始命令/stdout/stderr/XML、archive/source/test SHA256。
Control 的三个 PG 用例必须全绿：HTTPS 注册/票据/公钥、真实设备与发行器、generated Redeem 的 ingress/V12
持久事实、generated mailbox READY/LOCAL_REGISTER/source、MEMBER/EXTERNAL、Org 撤权和新请求拒绝、
错误 svc/tenant 拒绝、JWT supplier 重发以及完整关闭。
撤权负例使用撤权前已真实登记的第二 local，直接 generated IAM Register，避免被本地 revision/fixture 错误遮蔽。

反证只对 `IamNetworkSourceAuthority` 去掉一次当前 Org permission 检查，测试与所有其他源码完全相同。
必须编译成功，并且仅 `httpsRedeemOrgRevocationAndShutdown` 在
`revoked Org permission must not persist another source` 的真实 IAM source 数量断言失败；
SQL、初始化、编译、skip、空测试或其他失败一律不算。
工人仅运行 `-CompileOnly`，它会编译 Control 和单 overlay，**不宣称业务正反证成立**。
原 #222 联合类、37 例索引和两个既有反证工具保持原样；回归由主控按原流程另行核验。

Control 包含真实子进程 ready→refresh→stop/EOF（含两份设备凭据实际续期），并核日志无凭据、关闭后无自有进程/端口/临时目录；公开生命周期证据写入各 archive 的 platform-app/target/java-bridge-lifecycle-*。
此检查与新非作者独审、精确 HEAD CI 全绿完成前，不得将本作者交接标成已验收/已合并。
