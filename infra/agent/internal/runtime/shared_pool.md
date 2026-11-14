# 共享池节点执行端（R）

基线：K / main `f6001a02`。使用生成的 `nodev1.SharedDsh*` 消息，不修改公共契约。
这是供联合接线使用的节点执行端；当前 app/reconciler/NodeControl 尚未启用这些入口，不能据此宣布共享模式可用。

## 端口与调用顺序

1. 安装配置提供 `storage.PoolLayout{Root: /var/lib/auriga/pools, Quota: ...}`。Root、host、control、members 的父目录必须由节点独占；只把最终 workspace 赋予运行用户写权限。不得把 control 或封存根挂进容器。
2. 一个节点只能有一个 `SharedPools` 实例，复用节点数据独占锁；`Supervisor` 接进程外 Docker（现有 `Docker.Stop` / `ResourceProcessStopped` 已满足接口）。所有动作按 physicalInstanceId 指向同一个池容器，不能按 member 启动容器。Engine 自动重启必须保持关闭。
3. W 的受信 NodeControl 消费方调用 `Apply`，回传 RuntimeReceipt。相同 operationId 的不同 action/binding 拒绝；动作前先落盘，缺停止证明、配额或目录处置证明均 unconfirmed。W 仍须执行 CAS/epoch、超时/晚 ACK 判定及正常页面探测。
4. P 提供 `PoolSessions.Cancel/Discard`：仅操作完整 binding 的公开宿主 API；Cancel 必须合作等待结束才返回，不能以“已发送取消”确认。节点在 30 秒 deadline 外等待结果，宿主阻塞不阻塞超时判定；超时停止整个池。旧调用迟到结果不再改变收据。Discard 返回 nil 必须已经删除相应会话，不能恢复污染任务。
5. 停止后先 `RecoverPool`：确认树停止、清理资源视图并丢弃全池已登记会话，然后才能解除污染。目录动作也会标污染，准备完后执行此步骤。目录动作未确认仍禁止启动。W 对未确认收据继续显示 blocked，恢复不伪造原操作成功。
6. 资源适配方实现 `PoolResourceVerifier`，复用统一资源的签名/撤销/租约校验和只读挂载；每次启动必须以最新 W 快照调用 `AuthorizePoolStart`。`CheckPoolViews` 检查全池完整绑定、相同授权摘要和相同有序资源集合，source 只能 platform，期限边界即拒绝。此函数自身不验签，不能用它代替 `Verifier`。
7. 节点 observer 配置 `PoolWatch{Pools, Verifier, Validate: runtime.CheckPoolViews}` 并周期调用 `Check`，回传 ResourceReport/GuardianImpact。故障监测调用 `Trip(...,"crash")`；资源到期/撤销调用 Trip。全池影响持久化，可用 `GuardianImpacts` 重发，消费方按 pool+operationId 处理状态更新。`stopping` 表示中断期间没有最终停止证据，必须重试 Trip，不能视为 stopped。
8. 每次可信任务登记调用 `AdmitTask`，结束调用 `CompleteTask`。本地门禁为 4 活跃/每成员 1，节点内存达到 80% 拒绝新任务；20 登记在 Apply 检查。W 负责权威成员状态、扩池及注销编排，不能仅凭本地预算当授权。

## 配额、封存和 profile

`XFSProjectQuota` 要求独立、启用 project accounting/enforcement 的 XFS 文件系统。安装配置预留唯一 project ID，逐 workspace 和 archive 分配，拒绝缺失/重复 ID；执行 `project -s`、设置 5GiB 和 100000 inode 硬限，再读回内核配额报告。没有软配额回退。重命名封存后先切换 archive project，再创建原位置空 workspace，避免封存占用新目录预算。此实现尚需 Linux 真盘写满验证；宿主非 XFS 不应启用共享。

`archive-reset` 使用 operationId 作为节点私有封存标识，支持重命名后崩溃重试；到期为原观测时间加 2592000 秒。observer 定期调用 `SweepArchives`，失败必须上报告警并重试。新会话只取得 Workspace 映射，永不取得封存路径。归档动作应以旧目录的 binding 执行，再准备新 epoch；不能传入客户端 cwd。

`renderer.ValidatePoolProfile` 校验完整展开的安装方 profile（不接受基于 dsh-base/web-app 的叠加 patch）。明确允许表之外全部拒绝，包含 shell/pwsh/terminal/jobs/subagent、MCP、动态插件/设置/目录修改控制器和 bundles；固定 skill 扫描根 `/run/auriga/skills-active/current`，关闭默认根，拒绝可执行配置/别名/合并和多文档。该视图必须节点只读挂载。P 仍需提供可启动的完整公开宿主 profile、工具 guard 和真实注册证据；允许表通过不证明依赖注入没有间接启动其他控制器。

现有 `resource_supervisor.mjs` 在宿主进程之外运行，使用租约与单调时间约束，并在到期/时钟回退停止子进程。共享启动时应写入全池最早资源到期时间、继承已有只读 policy 挂载和 PID1 容器规则。父进程观察加 Engine 树停止确认共同构成证明；插件 ACK 或端口消失不够。

## 验证边界

Go 测试覆盖目录映射、硬配额缺失拒绝、封存及重启幂等、跨租户/旧代次、全池影响与恢复门禁、资源期限和授权不一致、本地任务预算；五种 RuntimeReceipt 通过 K `ports.mjs confirmReceipt`。测试用替身无监听端口、不占真实 PG/DSH 槽。

XFS 命令依据：[xfs_quota 手册](https://man7.org/linux/man-pages/man8/xfs_quota.8.html)；树检查依据：[上游 project.c](https://kernel.googlesource.com/pub/scm/linux/kernel/git/djwong/xfsprogs-dev/+/refs/tags/health-monitoring_2025-09-05/quota/project.c)。特别处理 `project -c` 输出错误但退出码仍为零的情形，要求只有预期检查头与单路径摘要，拒绝额外诊断。

真实 NodeControl 接线、完整宿主 profile、XFS 写满、Linux 执行树/阻塞/崩溃、真实门户 Web/WS 和压力测试由主控按 W→IAM→E→R→N→P→U→V 顺序收证。工人不改 NAS。

首期仍接受原生 owner 旁路、跨成员文件/会话读取、驱动他人会话造成企业身份冒用及审计错归、单进程故障停全池。上述实现和单元测试不能被表述为已隔离或已保证。
