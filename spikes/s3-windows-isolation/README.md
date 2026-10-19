# S3：Windows 原生进程隔离验证

这是独立技术原型，不接入生产驱动，不进主分支。真实实验结果由 GitHub Actions 的一次性 Windows VM 产生；本机的编译通过不代表隔离通过。

## 第二轮修复（2026-10-19）

第一轮已在 Server 2025（build 26100）和 Server 2022（build 20348）运行，Node 均为 v22.23.3。以下改动针对第一轮证据，修复后的特权行为仍需主控重新跑 CI：

- WFP 将返回值收窄为 DWORD，兼容 `0xffffffff80320003` 的符号扩展；该码表示过滤器不存在。删除后立即再查过滤器，总清理还枚举本次显示名前缀的子层/提供者。原型使用内置默认子层且不创建私有提供者，不删除共享对象。
- 进程数保留原控制台行为的诊断对照，再用 `CREATE_NO_WINDOW` 负载作严格判定。证据包含每个 PID 的镜像名、是否存活、查询错误、Job 活动计数及创建失败码。原始列表长度不等于活动进程数，活着的 conhost 没有名称豁免。第一轮只有 PID 数字，无法事后确认额外进程的身份；第二轮的对照证据用于区分 conhost 和已终止的拒绝创建进程。
- RDP 禁止策略改查账号 LSA 权限。`LogonUser` 只用支持的交互类型 2 作拒绝验证，不再传入类型 10；策略存在不能替代真实 RDP 会话的端到端测试。
- 配额 ACL 通过卷 GUID 作用于真实卷根及专用数据目录，避免只修改宿主挂载点的重解析项。写入基线必须成功才配置配额；文件已不存在时清理成功。
- AppContainer 使用 `CreateEnvironmentBlock(..., FALSE)` 提取必要系统/profile 字段，过滤任意父进程环境及凭据，补全 `LOCALAPPDATA`，排序并验证 Unicode 双零环境块。profile 由 SYSTEM 创建，其基础路径来自 SYSTEM 原生环境；底层实例 token 不变。每种配置附带环境键及启动阶段诊断，仍须 CI 确认该组合可正常创建 LowBox。

第一轮 Server 2022 的内存实验还出现一次超时（已触碰 44 MiB、峰值提交约 96.9 MiB），不能称为两个 OS 的内存测试都通过。第二轮保留原内存负载及判据，须一并复核，不因其他修复掩盖该结果。

## 本机允许的操作

在本 worktree 中进入本目录，仅运行：

```powershell
go build -o s3probe.exe .
go vet ./...
go test -v ./...
```

需要 Go 1.27、Windows amd64；唯一外部 Go 依赖是 `golang.org/x/sys v0.49.0`。单元测试只检查内存中的 Windows SDK 布局、SID/SDDL 编码、结果聚合和运行限制，不创建账号、Job、监听器或防火墙规则。`Run-All.ps1` 为兼容 Windows PowerShell 5 使用 UTF-8 BOM、CRLF。

## 由主控运行 CI

1. 将这里的 `ci.yml` 复制到 `.github/workflows/s3-windows-isolation.yml`，只推到 spike 分支，手动触发 `workflow_dispatch`。不需要仓库密钥。
2. 工作流分别使用 `windows-latest` 和 `windows-2022`，记录实际 OS build、Node 版本。不能用自托管 runner。
3. 工作流构建、vet、跑非特权单测，再执行 `./Run-All.ps1 -CIDisposableVM`。
4. 总控先用 runner 的管理员令牌运行 `identity` 比较，然后通过临时 SYSTEM 计划任务运行全部实验。结束、失败或超时都会尝试清理计划任务并做第二次清理审计。
5. 下载 `s3-windows-isolation-<runner>` 工件：重点看 `report.json`、`summary.md`、`identity-admin.json`、`cleanup-final.json`。出错时同时看相应 `*.stderr.txt`、`*.stdout.json`、`bootstrap-error.json`。

某一实验失败会继续跑下一项。单项通常有 75 秒上限，清理 120 秒；任务及工作流另有外层超时。`report.json.status` 为 `fail/error/unsupported` 时工作流报失败，但仍上传已得到的证据。`unsupported` 不是通过。

仅在上述 CI VM 中，单项也可这样运行（通常直接使用总控）：

```powershell
.\s3probe.exe identity --ci-disposable-vm --id <总控生成的10位hex> --root "$env:ProgramData\auriga-s3-<同一id>" --node <总控复制的node.exe>
```

子命令为 `identity`、`job`、`shim`、`wfp`、`acl`、`quota`、`appcontainer`、`cleanup`。总控先把公开可执行文件复制到运行目录的 `tools`，不能跳过。每个特权入口同时要求显式开关、GitHub Actions 环境、`github-hosted` 标记、提升后的令牌和固定格式的运行目录。这是防误操作约束，不是对恶意操作者的安全边界；不要伪造环境变量在本机绕过。

## 结果格式

每项 stdout 输出一个 JSON 对象，含 `experiment`、`status`、`checks`、`error`、`durationMs`；每条检查含 `name`、`status`、`evidence`。总控补充退出码，汇总环境及所有项。

- `pass`：对应断言满足；`fail`：测得行为不满足断言。
- `observed`：原始测量/比较，没有把它解释为能力通过。
- `unsupported`：相关 API、镜像能力或探测前提不可用。
- `error`：设置、执行或清理出现错误，不能作出能力结论。

预期的负向检查（禁止登录、拒绝连接、拒绝越额写入）只有在拒绝行为得到证明时才通过。WFP 的每个目标先有连通基线；目录的拒绝要求 `ERROR_ACCESS_DENIED`，不把文件不存在算成隔离。

## 每项如何判定

### 1. 账号与启动 API

每项使用随机运行前缀、本地账号、单独的专用本地组。口令含 32 字节随机熵，仅存在进程内存和匿名 stdin 管道；不写文件、参数、环境或日志。LSA 对账号 SID 授予 `SeBatchLogonRight`，明确拒绝交互和 RDP 登录。

必须证明：batch `LogonUser` 成功，交互类型 2 返回 1385，LSA 明确列出 `SeBatchLogonRight`、`SeDenyInteractiveLogonRight`、`SeDenyRemoteInteractiveLogonRight`；`CreateProcessAsUser` 子进程实际 SID 正确，令牌无 Administrators、直接加入的本地组只有专用组。保留 `CreateProcessWithLogonW` 在同样拒绝策略下的比较结果和调用者特权列表。提升后的管理员缺少服务特权属于比较证据，不直接推断 SYSTEM 也不可用。`LogonUser` 不支持远程交互类型 10，详见[支持的登录类型](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-logonuserw)。本轮未增加网络登录权策略。

服务方案优先验证 `LogonUser(LOGON32_LOGON_BATCH)` + `CreateProcessAsUser`。后者通常要求调用者持有 `SeIncreaseQuotaPrivilege`，还可能要求 `SeAssignPrimaryTokenPrivilege`；SYSTEM 服务持有这些权限。`CreateProcessWithLogonW` 不要求这些调用者特权，但账号需要本地交互登录权限，且不能从 LocalSystem 调用，和本实验的禁止交互策略冲突。见 [CreateProcessAsUser](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessasuserw)、[CreateProcessWithLogonW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createprocesswithlogonw)。

### 2. Job Object

四类行为用独立 Job 分别测量，另加原控制台行为的诊断对照；均先挂起创建、加入 Job、再恢复，禁止 breakaway：

- Job 总提交内存限制 96 MiB，Go 负载尝试分配并触碰 192 MiB，要求确实启动后非零退出；结合证据排查其他崩溃原因。
- CPU 硬上限 1000/10000，所有逻辑 CPU 跑忙循环，读取 Job 用户态+内核态累计 CPU 时间，除以墙钟和逻辑核数。当前容差要求整机占比在 1.5%–18%，原始值全部保留；共享 VM 的调度噪声需要人工复核。
- 活跃进程限制 4，先记录原控制台模式的 PID 诊断，再明确用无控制台方式启动 3 个存活子进程；第四个创建须返回 1816。Job 读回上限及活动计数须为 4、四个负载 PID 均在 Job 中且存活，不得出现额外活进程。原始列表可短暂带有已终止进程，需结合逐 PID 状态判断；活着的 conhost 同样计入。
- `cmd → Node → Go` 三层 PID 都必须出现在 Job 中，`TerminateJobObject` 后等待预先打开的三个进程句柄全部进入结束状态，避免 PID 重用误判。

参见 [Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)、[CPU rate control](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_cpu_rate_control_information)。

### 3. 垫片与代理退出

一次性 controller（模拟旧代理）通过内存口令以实例账号启动 shim，随后真正退出。shim 自己持有有名 Job 的最后一个句柄，并将负载放进 Job；shim 本身在负载 Job 之外。独立的新 controller 用 `OpenJobObject` 找回 Job、确认负载 PID，并从上次偏移重新打开日志文件读取新增字节，再终止整个负载 Job。

必须看到旧 controller 退出后 shim 及负载仍活着，新 controller 能接管 Job 和日志。使用文件日志，未实现命名管道协议。为防 CI 卡住，shim 有 60 秒实验租约；这不是生产实例的生命周期方案。命名 Job DACL 只授予 SYSTEM/Administrators，不能以不可信 shim 自报的 PID、路径或日志作为生产权威状态。

### 4. WFP

直接调用 `fwpuclnt.dll`。`ALE_AUTH_CONNECT_V4/V6` 上以 `ALE_USER_ID` 的 security-descriptor blob（`FWP_SECURITY_DESCRIPTOR_TYPE`，匹配权限 1）匹配实例 SID。默认 sublayer 中，低权重拒绝该 SID 的所有连接，高权重允许一个 IPv4:port。规则不匹配其他账号，不改全局默认策略。参见 [条件标识](https://learn.microsoft.com/en-us/windows/win32/fwp/filtering-condition-identifiers-)、[过滤器结构](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter0)。

受控 TCP listener 先证明连通，再分别验证：允许 `127.0.0.1:<白名单端口>`；拒绝同 IP 的另一端口、`127.0.0.2:<同端口>`、IPv6 回环。另起两次实例进程重复连接。持久过滤器由独立安装进程创建，该进程退出且未显式关闭 engine；新管理进程再查规则，实例重新连接仍必须受限。另一个独立进程创建动态会话过滤器，退出后它必须消失。

限制：没有重启 BFE/OS，没有覆盖远端路由、DNS、UDP、原始套接字或应用层代理隧道。当前原型只能证明记录中的受控目标，不能据此声称所有网络绕过都已排除。持久规则的 BFE 重启语义来自 [FWPM_FILTER0 文档](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter0)，不是本轮实测结论。

### 5. 目录 ACL

实例目录设置 protected DACL，只给实例 SID、SYSTEM、Administrators；实际读取自己的 sentinel 成功、其他实例 sentinel 必须返回访问拒绝。额外读取 Windows、System32、Program Files、ProgramData、Users、Public、调用者用户目录及公共文件，结果只记录可读与否、字节数/目录项数量，不上传文件内容或目录项名字。

同在 ProgramData 下但显式公开的 sentinel 应可读，用来证明“放进 ProgramData”本身不能保护数据。实际系统路径不存在、被占用和访问拒绝分别保留错误码；不要把观察表里所有失败都解释为 ACL 拒绝。文档中的“能读系统公共目录”需用两个镜像的实际结果补充边界。

### 6. NTFS 按用户配额

用 diskpart 创建 128 MiB 临时可扩展 VHD，挂载到本次运行的子目录，格式化 NTFS。通过卷 GUID 对卷根及 `probe-data` 设置 ACL，并记录数据目录实际 SDDL。先由实例账号成功写入 32 MiB并删除，再在该临时卷执行 `fsutil quota enforce/modify`，为该账号设置 4 MiB 阈值与硬上限；基线失败即停止，不能继续配置配额并作结论。再次以该账号写入，必须在 32 MiB之前得到磁盘满/配额相关 Win32 错误；权限拒绝不算配额有效。保留 quota query 输出。

镜像不能创建/挂载 VHD 或配置配额时报告 `unsupported`。最后从独立存储查询确认 VHD 已卸载，再删除其文件；卷内配额索引随卷销毁。不修改系统卷。参见 [fsutil quota](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/fsutil-quota)。

### 7. AppContainer

底层仍用实例 batch token，叠加 `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES`，实查子进程的用户 SID 和 `TokenIsAppContainer`。只对该 profile SID 授予专用 scratch 目录访问，并设置可继承 low integrity label，复制公开 Node 可执行文件。启动前验证 Node 绝对路径存在；原生环境块先复制再 `DestroyEnvironmentBlock`，仅保留必要字段，包含 profile 解析所需的 `LOCALAPPDATA`/`USERPROFILE`。输出环境键及 Unicode/双零标记，不输出凭据或整个环境。参见 [CreateEnvironmentBlock](https://learn.microsoft.com/en-us/windows/win32/api/userenv/nf-userenv-createenvironmentblock)、[AppContainer 的 profile 环境](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer)。

依次比较：无 capability；`internetClient` + `internetClientServer` + `privateNetworkClientServer`；增加 `CheckNetIsolation -a` 出站回环豁免；如果镜像支持，再在有界 Job 内启动 `CheckNetIsolation -is` 临时入站开发豁免。逐项测 Node 启动、`cmd /c` shell、`listen(0,'127.0.0.1')`、宿主连接和 AppContainer 反连宿主。监听与连接是两个独立观察；出站 `-a` 不等于宿主能连接入站监听。参见 [IPC 回环方向](https://learn.microsoft.com/en-us/windows/apps/develop/communication/interprocess-communication)、[SECURITY_CAPABILITIES](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-security_capabilities)。

各配置的功能断言可能失败，这是待解释的实验结果；需要按配置查看，不能只读总状态。即便开发豁免成功，也不等于已经有可支持的生产托管方案；还必须验证真实 DSH、插件和最小 capability 集。

### 8. 清理

每项先落不含凭据的资源意图日志，再创建资源；日志通过同目录临时文件替换，避免半写入。正常返回会释放进程/Job、移除该账号 WFP 规则和 LSA 权限、删除账号及组，AppContainer 恢复专属回环豁免并删除 profile，卸载 VHD。

总清理只接受精确的本次命名空间和目录，重试上述操作，并独立查询账号/组不存在、LSA 权限不残留、所有登记的 Job 不存在、每条 WFP GUID 不存在、profile 及回环豁免不存在。确认后删除实例目录、VHD、工具副本和整个运行根目录。计划任务由外层 finally 注销并再次核验。任意清理失败都会保留失败证据且最终状态不为 pass。

VM 被平台强制销毁/工作流取消时，finally 不是保证，因此限定 GitHub-hosted 一次性 VM。原型无生产级崩溃恢复、DPAPI 状态存储、日志轮转或 shim RPC。
