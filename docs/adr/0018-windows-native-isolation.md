# 0018 Windows 原生隔离验证与能力门槛

- 状态：提议
- 日期：2026-10-19
- 关联：[ADR 0010](0010-windows-native-isolation.md)、[节点代理 §5.4](../architecture/node-agent.zh.md#54-windows-原生进程驱动)

## 背景

ADR 0010 选择本地账号 + shim + Job Object + ACL + WFP + NTFS 用户配额，面向互相信任的团队。这里记录验证方案及判定标准；没有 CI 实测之前不改变已采纳决定，也不把 Windows 原生模式提升为容器级隔离。

原型位于 `spikes/s3-windows-isolation/`，独立 Go 1.27 模块，仅在一次性 GitHub-hosted Windows VM 中运行特权实验。工作流包含 `windows-latest` 和 `windows-2022`；实际 OS build 和 Node 版本写入结果。本机只编译、vet、跑非特权单测。原型本身不合入主分支，主控根据工件补全此 ADR 后决定是否采纳。

## 提议与判定

1. **以 SYSTEM 服务为首选验证身份。** 管理员比较保留为单独工件。账号只直接加入专用本地组，随机口令不落盘，LSA 明确拒绝交互及 RDP，单独允许 batch。通过条件：batch token 能启动正确 SID 的进程，两类交互登录明确被拒，实例令牌不含管理员组。`CreateProcessAsUser` 的调用者特权与 `CreateProcessWithLogonW` 的交互登录要求不同，不能把“提升为管理员”等同于 SYSTEM。后者还不支持 LocalSystem 调用。依据：[CreateProcessAsUser](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessasuserw)、[CreateProcessWithLogonW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createprocesswithlogonw)。预期 SYSTEM + batch 路径可用，CI 待确认。

2. **每种 Job 限额都要求行为证据。** 分别测试提交内存、CPU 硬上限、活跃进程数。以挂起创建→分配 Job→恢复消除初始逃逸窗口；shell、Node、孙进程全部进入同一 Job，不允许 breakaway。通过条件：超限分配失败、整机 CPU 比例符合容差、不能产生第五个活跃进程、终止 Job 后三个层级的原进程句柄均已结束。预期支持；共享 VM 的 CPU 调度噪声需复核，API 设置成功本身不算限额有效。

3. **shim 持有存活句柄。** 实验旧 controller 真正退出，实例账号的 shim 保留命名 Job 和文件日志，新 controller 重开 Job、从旧日志偏移读新增字节、终止负载。通过条件：旧代理退出后实例仍运行，重接管成功；不能由旧代理持有最后一个 kill-on-close 句柄。预期支持；生产接管仍需受保护的持久状态和授权 IPC，实例自报状态不可信。

4. **按 SID 过滤必须覆盖本机连接。** 使用 `ALE_AUTH_CONNECT_V4/V6` 的 `ALE_USER_ID` security-descriptor 条件，只允许受控的一个 IPv4:port，拒绝其余。所有探测目标先验证无规则时可连，之后必须证明同 IP 其他端口、其他回环 IP 同端口、IPv6 回环受阻，实例重启两次仍受限。独立规则安装进程退出后，持久规则必须仍存在且有效，动态会话规则必须消失。预期持久规则支持代理重启；回环裁决须实测，失败不得发布“仅能访问白名单”的能力。BFE/OS 重启、远端路由、UDP/DNS 等覆盖仍需后续实验，当前不能作全面网络安全结论。依据：[FWPM_FILTER0 的持久标志](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter0)。

5. **ACL 隔离实例数据，系统公共目录保持可见。** 实例根目录关闭继承，授予实例 SID、SYSTEM、Administrators；通过条件：自己的 synthetic 文件可读，其他实例明确返回访问拒绝，DACL 有 protected 位。另记录系统公共目录、ProgramData、用户目录的实际可读范围。预期公开的系统文件仍可读，不应在产品文案中表述成“看不到宿主机文件”；ProgramData 的目录名不能替代 ACL。

6. **NTFS 硬配额须实际拒绝写入。** 在临时 VHD 的 NTFS 卷上，先证明 32 MiB可写，设置按账号 4 MiB硬限额后必须得到磁盘/配额相关错误且未写完 32 MiB。不改 CI 系统卷。预期 NTFS 支持，但镜像能否使用 VHD、初始化配额索引待测；不可用输出 unsupported，不能悄悄视为硬配额成功。备选为平台软配额，并明确能力降级；若需要目录级而非全卷用户级语义，须重新选存储方案。

7. **AppContainer 单独验证端口方向与兼容性。** 底层使用实例账号，上层加 AppContainer token；先核验身份、低完整性目录权限，再比较无 capability、三个网络 capability、出站回环豁免、临时入站开发豁免。每一配置独立记录 Node、shell、监听、宿主入站和反连。强隔离选项的门槛是同一可支持的配置同时满足实际 DSH 所需的监听、宿主连接与 shell；只有 `listen` 成功或只有开发豁免成功均不足。预期这是主要不确定项。依据：[Windows IPC 的两种回环豁免](https://learn.microsoft.com/en-us/windows/apps/develop/communication/interprocess-communication)。备选为普通账号 + WFP 模式；外部不可信租户继续使用 Linux + 容器，也可评估 Windows VM 边界。未经实测不启用 AppContainer 默认模式。

8. **清理是验收门槛。** 先登记资源意图再创建；正常路径、总控 finally、外层独立清理分别覆盖。通过条件：账号、组、LSA 权限、命名 Job、WFP GUID、profile、专属回环豁免、VHD、测试目录 ACL 和 SYSTEM 计划任务均无残留。任何失败阻止本轮宣告通过。清理不重置全局防火墙或全局回环豁免列表。

## CI 结果待补

主控在此补两个 runner 的 run URL、实际系统版本、各项证据、差异和最终结论，至少确认：

- `identity-admin.json` 与 SYSTEM 结果有无 1314/1385，调用者实际具备哪些特权。
- Job 限额是行为通过，还是环境过慢/负载未启动造成的假象。
- shim 由实例账号运行且重接管后日志读偏移连续。
- WFP 允许与拒绝各有正向基线，持久性确实跨了安装进程退出。
- ACL 拒绝和路径不存在、文件被占用没有混淆。
- 配额失败是磁盘/配额错误，临时卷真的 NTFS 且已卸载。
- AppContainer 每个 capability/豁免组合的监听和宿主连接分别是什么结果。
- `cleanup-final.json`、`scheduledTaskRemoved` 及全部清理断言通过。

当前尚无特权实测结论。工作流失败也要读取工件；`observed` 或 `unsupported` 不能补写成通过。

## 影响与未覆盖边界

在结果采纳前，ADR 0010 的“隔离弱于容器、只面向可信团队”保持有效。即使所有合成实验通过，也只验证了两个 CI 镜像的具体行为，不等于覆盖 Windows Server 2019、所有桌面版本、真实 DSH 插件、内核漏洞、凭据窃取或完整绕过测试。

原型不实现生产 DPAPI 凭据状态、服务安装、自升级、日志轮转、完整 shim RPC、BFE/OS 重启恢复。以上需要后续生产驱动设计和独立验证，不能直接复用 spike 当实现。
