# 共享池节点执行端（R）

契约来源：K/main `f6001a02`；返工合并 main `edcdb32b`。只使用已有 `nodev1.SharedDsh*` 消息，不修改公共契约。当前 app/reconciler/NodeControl 尚未启用这些端口，真实页面未验收，不代表共享模式已交付。

## 成员操作与恢复

- W 通过受信 NodeControl 调用 `Apply` 并消费 RuntimeReceipt；相同 operationId 的完整意图指纹幂等。节点只接受服务器映射的目录，不能传 cwd。
- `prepare-empty` 可在池运行时为新成员建目录，不污染或停止全池。`archive-reset`、`discard-sessions` 仅要求目标成员没有活跃任务，或其完整 binding 已合作取消确认。它们只处理目标成员，其他成员会话和任务继续运行。
- `PoolSessions.Cancel` 必须等合作取消完成后才返回成功；30 秒等待、宿主调用和 Engine Stop 都在状态锁外进行。资源 guardian 可抢占，持久化 Fence 使其之前的迟到 ACK 不能推进操作或清除污染。同一成员并发操作拒绝重入，不同成员可独立处理。
- 取消超时或失败通过 `Trip(cancel-timeout)` 保守停全池；资源真正到期、撤销和崩溃也使用 Trip。显式 `stop-pool` 仅用于 W 已授权的池级编排，不是成员目录操作的前置条件。外部 `Stop` 加 `ResourceProcessStopped` 必须证明整个执行树停止，否则 unconfirmed/blocked。
- `RecoverPool` 确认树停止、清理资源视图，再由 `PoolHistoryFence.MakeHistoryReadOnly` 禁止旧会话继续执行，保留按归属只读历史。恢复不调用全员 Discard。缺此适配、清理失败或恢复期间再次 Trip 都不能解除污染。目标成员的模式切换仍可显式调用 `discard-sessions` 丢弃本人会话。
- `AdmitTask` 是节点附加预算门禁：20 登记、4 活跃、每成员 1 活跃，内存达到 80% 拒绝准入；W 仍是成员、代次、扩池的权威。成员取消后旧 binding 不再接受新任务，直到 W 给出新代次。任务槽持久化，迟到完成不能释放新任务槽。

## 成员释放与权威来源

K 当前没有完整成员列表或快照版本端口，不能把部分 ResourceView 响应当成全量名单。R 使用已有 `SharedDshRevocation`（reason=switch/member-disabled）实现 `RevokeMember`，由 W 在目标成员取消、本人会话和文件处置完成后调用。若初始撤销提前送达而成员仍活跃，则返回未静止错误，W 在处置完成后重试。logout/credential-invalid 不当作移出池。

释放持久化撤销水位，裁剪当前 Binding、活跃和静止记录，释放登记容量；停池影响和启动资源视图只包含当前成员。低于撤销水位的旧绑定不能复活成员；新代次可重新登记；重复旧撤销不会删除新代次。旧的空 workspace 随释放移除并回收配额，未封存的非空目录拒绝释放，不递归删除用户数据。目录或内核清理失败保留待清理绑定与 project reservation，重复撤销可续做。

需转 K/K2：未来若改用权威全量成员集合，须定义完整性、快照修订和运输语义。当前不自造新的 RuntimeIntent action 或 wire 字段；由主控安排已有撤销消息在 W 编排完成阶段的节点适配。

## 配额与封存

安装配置提供 `PoolLayout.Root=/var/lib/auriga/pools`；Root/control/父目录归节点独占，只把最终 workspace 赋予运行用户写权限，不把控制账本或封存挂入 DSH。一个节点同一 Root 只运行一个 SharedPools，复用节点独占锁，每 physicalInstanceId 仅一个容器或主进程，无自动重启。

`XFSProjectQuota{Binary,Mount,IDs:&ProjectIDs{File,First,Last}}` 要求专用、启用 project accounting/enforcement 的 XFS 文件系统和安装预留 ID 区间。无需预配 tenant/member/epoch 路径。ProjectIDs 使用节点私有 bbolt 账本和文件锁，先同步提交 path→ID reservation，再执行内核分配；多对象和进程串行变更，不重复分配。失败或进程崩溃保留 reservation，重启重试复用原 ID，区间耗尽或变更拒绝。

每成员 5368709120 字节/100000 inode，设置后整树与内核限额读回，不能降级软配额。archive 使用独立 ID，避免占用新空目录预算。封存到期由 `SweepArchives` 删除后回收 project；仍有路径、清理失败或内核仍记录用量时不回收，告警和重试由 observer 调用方负责。archive-reset 用旧 binding，随后准备新 epoch；封存标识为 operationId，保留 2592000 秒，不自动挂到新会话。

## 资源与 profile 接线

`PoolResourceVerifier` 必须接已有统一资源的签名、租约、撤销和只读挂载。`CheckPoolViews` 只做结构、绑定、共同授权摘要、有序资源集合和期限验证，不能代替验签。启动以最新 W 数据调用 `AuthorizePoolStart`。observer 使用 `PoolWatch{Pools,Verifier,Validate:runtime.CheckPoolViews}` 周期核验：不完整快照、错绑定或授权集合、不可用 verifier 只拒绝候选准入，分别回报 authorization-mismatch/cleanup-unconfirmed，不假报撤销并停全池。实际到期或撤销须返回 `observer.ErrPoolResourceExpired/Revoked`（可包装），触发全池 Trip；传输错误不得标 revoked。

guardian 影响在 Stop 前写为 stopping，最终停止状态持久化，可用 GuardianImpacts 重传。消费方按 pool+operationId 处理状态更新；stopping 不是停止证据，需重试。容器 PID1 复用 resource_supervisor.mjs，policy 使用全池最早到期时间；它与 Engine 执行树确认共同工作。

`renderer.ValidatePoolProfile` 接收 P 提供的完整展开安装方 profile，不是叠加在 dsh-base/web-app 上的 patch。显式允许表拒绝执行工具、MCP、动态安装和控制器、bundle、可执行配置与别名；平台 skill 根固定 `/run/auriga/skills-active/current` 且关闭默认根。P 仍须证明真实宿主依赖注册没有间接激活禁止能力。

## 验证与已知边界

Go 使用本线 testdata/shared-pool-receipts-v1.json 五种收据金样，无 Node/npm 或跨模块依赖，来源注明 K f6001a02。可另行执行 `node infra/agent/internal/runtime/testdata/validate-shared-pool-receipts.mjs` 用 K oracle 复核金样（需 contracts 锁定依赖），Go workflow 保持原样。

测试覆盖不影响其他成员的加入和切换、只读历史恢复、成员释放和重登水位、释放后的启动和容量、取消等待被 guardian 抢占和晚 ACK、ID 分配、耗尽、回收、失败重试及真实子进程崩溃持久化。

Windows 状态替换对短暂的文件共享冲突做有限重试，包括 MoveFileEx 在目标文件缺少 FILE_SHARE_DELETE 时返回的 ACCESS_DENIED。持续冲突仍返回错误并保留旧状态，不修改 ACL 或所有权；真实持锁文件测试覆盖释放后替换与持续占用失败两种情况。

XFS 命令依据：[xfs_quota 手册](https://man7.org/linux/man-pages/man8/xfs_quota.8.html)；树检查依据：[上游 project.c](https://kernel.googlesource.com/pub/scm/linux/kernel/git/djwong/xfsprogs-dev/+/refs/tags/health-monitoring_2025-09-05/quota/project.c)。project -c 的非空诊断即使退出码为零也拒绝。

W→IAM→E→R→N→P→U→V 的真实 NodeControl、宿主 profile 和历史只读门禁、Linux XFS 写满与执行树、正常页面 Web/WS 和压测仍需主控串行接线收证；工人不改 NAS。

首期接受的原生 owner 旁路、跨成员文件和会话读取、驱动他人会话导致企业身份冒用与审计错归、单进程故障停全池仍存在。上述实现和测试不能表述为已隔离或已保证。
