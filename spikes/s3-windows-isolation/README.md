# S3 Windows 原生隔离安全验证原型

这是独立 spike，不是生产驱动，不合入主分支。已有两次 CI 证据，第三轮安全改动和第四轮修复仍需主控在一次性 Windows VM 复测。即使兼容性通过，也不能直接宣称“只能访问白名单”或具备容器级隔离。

## 本机与 CI

本机只在本目录执行 `go build -o s3probe.exe .`、`go vet ./...`、`go test -count=1 ./...`。Go 1.27 / Windows amd64，依赖仍只有 `golang.org/x/sys v0.49.0`。单测处理 SDK 布局、SDDL、状态码、路径/环境编码和普通临时文件，不启动特权实验。

主控沿用 spike 分支，将 `ci.yml` 放入 `.github/workflows/` 后手动触发。矩阵为 `windows-latest`、`windows-2022`，先管理员比较，再由临时 SYSTEM 计划任务执行 `Run-All.ps1 -CIDisposableVM`。脚本兼容 Windows PowerShell 5（UTF-8 BOM / CRLF）。禁止自托管 runner 和开发机运行；不能伪造 CI 环境绕过防误操作限制。

总控依次执行 `identity`、`job`、`escape`、`shim`、`wfp`、`acl`、`quota`、`appcontainer`，finally 清理，再由外层注销总控计划任务、独立审计。普通项 75 秒，job/escape 120 秒，wfp/cleanup 180 秒；SYSTEM 任务上限 21 分钟，工作流上限 30 分钟。失败仍执行其余项并上传工件。

工件重点看 `report.json`、`summary.md`、`identity-admin.json`、`cleanup-final.json`、各项 stderr/stdout。每项 JSON 包含 experiment/status/checks/error/durationMs。`pass` 为断言满足，`fail` 为不满足，`observed` 仅记录测量，`unsupported` 表示缺少能力或受控验证，`error` 表示执行错误。未完成的发布门槛明确输出 unsupported，因此目前工作流报红可以是预期结果；不得把它改成 pass。

## 已有证据与本轮范围

第一轮系统为 Server 2025 build 26100、Server 2022 build 20348，Node v22.23.3。SYSTEM 启动、CPU、进程树、旧 shim 重接管、受控 TCP 白名单/回环拒绝、ACL 等有部分正向证据。Server 2025 内存通过；Server 2022 曾在触碰 44 MiB后超时，不能称双平台全部通过。

第二轮修复的 DWORD 符号扩展、登录权限核对、临时卷 ACL 已在 CI run `39222617442` 的两种镜像通过，WFP 移除与清理、NTFS 配额恢复正常。AppContainer 环境错误消失，但四种配置均等待 JSON 超时；普通账号 Node 基线通过。进程实验中父+三个子进程之外还有四个活 conhost，活动记账为 8，N+1 负载创建返回 1816。Server 2022 内存仍停在 44 MiB并等待超时，Server 2025 通过。第三轮根据安全审查改变了权限和策略模型，旧证据不能代替新模型复测。

第四轮仅处理上述三项残留：比较继承控制台、CREATE_NO_WINDOW 和 DETACHED_PROCESS；分离 AppContainer 程序/输出权限并捕获进程诊断；内存改用逐块原生提交及不限额对照。本机 build/vet 和 24 个顶层测试、47 个子测试通过；这不是管理员实验结果。

## 身份、口令与环境

每项使用独立本地账号和专用组；随机口令不落盘、不进命令行或环境。仍用 batch LogonUser 创建负载 token，同时设置并核对：SeBatchLogonRight，以及 SeDenyInteractiveLogonRight、SeDenyRemoteInteractiveLogonRight、SeDenyNetworkLogonRight、SeDenyServiceLogonRight。类型 2/3/5 必须返回 1385，batch 成功是同一口令的正向对照。RDP 改查 LSA 权限，不调用不支持的 LogonUser 类型 10，也不声称做过真实 RDP 会话。

shim 不再使用实例 token。SYSTEM shim 从 SYSTEM 测试控制器复制令牌句柄，只有负载使用实例 token，没有传递或序列化口令。句柄数值本身不能代替 OpenProcess/PROCESS_DUP_HANDLE 授权。随机口令仍短暂存在 Go 字符串中，不能承诺完全擦除。

S4U 尚未实现。LsaLogonUser/MSV1_0 的认证包、令牌类型、SeTcbPrivilege 和登录权需要独立验证；免口令不等于可以移除 batch 权，也不自动阻止任务计划程序/WMI 代建进程。该项保留发布门槛，不能靠撤销拒绝登录策略获得通过。参见 [LsaLogonUser](https://learn.microsoft.com/en-us/windows/win32/api/ntsecapi/nf-ntsecapi-lsalogonuser)。

负载环境只有 SYSTEMROOT、WINDIR、PATH、TEMP、TMP，排序、Unicode 双零编码，不携带 GITHUB_ACTIONS、RUNNER_ENVIRONMENT 或 runner 凭据。内部 Go 负载使用受保护运行目录中的 guard 防误启动；这不是对恶意操作者的安全边界。

## 目录、所有者与重解析点

运行根、实例根和 Job 显式采用 SYSTEM 所有者及 OWNER RIGHTS ACE。全局运行根不授予 AU 读执行，只给已登记实例 SID 遍历，tools 按 SID 授予只读执行。实例根只授予遍历/读取，不给 WRITE_DAC、WRITE_OWNER、DELETE 或根目录写入；work 根只给创建文件/子目录所需的有限权限，子项用 OICIIO 继承修改权限。管理日志、service 元数据与负载可写区分开。

特权文件操作采用 OPEN_REPARSE_POINT 检查每个路径组件，持有不共享写入/删除的句柄，拒绝任何 reparse tag；删除通过对象句柄，不递归跟随 junction。并发写入/替换导致共享冲突或非空目录时失败并保留证据。JSON 使用同目录随机独占临时文件替换，读取也验证路径。此实现仍需 CI 的重解析点和并发场景验证，不能称生产路径处理已审计完备。

负向探测实际尝试 FSCTL_SET_REPARSE_POINT；先在实例自建空目录上建立/移除指向 synthetic 目录的 junction 作正向对照，再要求实例根操作返回 5。另查根目录 WRITE_DAC/DELETE 被拒，自己的 work 可写，实例根/tools/ProgramData/Public/Windows Temp 写入应返回 5。标准 VM 的公共目录可能允许普通用户写入：这应报告失败，不能改全局 ACL 后掩盖它。生产需要受限令牌、AppContainer、VM 或专用机器权限方案。

## Job 限额与逃逸

每个负载均挂起创建、加入特权方创建的 Job、再恢复；Job DACL 不授予实例 SID，显式 SYSTEM owner、OWNER RIGHTS、KILL_ON_JOB_CLOSE、DIE_ON_UNHANDLED_EXCEPTION、UI 限制，不启用 breakaway 标志。

- 内存：先不限额用 VirtualAlloc 提交并触碰 192 个 1 MiB块作正向对照，再测 96 MiB Job。每次分配前将已提交量/下一次尝试同步到 JSONL（嵌入结果的 blockProgress）；释放原生块后写最终 JSON，避免 Go 堆在限额附近的分配/回收路径影响结果。要求 finished=true、退出码 0、实际拒绝码 8/1455/1816、提交量未达 192 MiB和 Job 峰值证据；退出 0 仅表示负载捕获错误并完成报告，超时不算通过。旧工件没有栈/原生调用跟踪，不能断言 Server 2022 停滞的具体根因已查明。
- CPU：先不限额跑完作为对照，再测 10% 整机硬上限；两者均要求退出码 0、finished=true，兼看绝对占比和相对下降。未充分占满 CPU 的对照不能作为通过依据。
- 进程数：保留父 CREATE_NO_WINDOW/子继承，以及每层 CREATE_NO_WINDOW 两种诊断；正式测试父和所有 Go 子进程显式 DETACHED_PROCESS，不混用 CREATE_NO_WINDOW。要求上限和活动计数 4、父+三个子进程存活、再创建返回 1816，活 conhost 不豁免。逐 PID 记录镜像、状态、私有提交和工作集，以及 Job 峰值；查询失败也保留。生产后台负载建议无控制台启动、日志用显式管道/文件；真实负载的派生进程也要遵循此策略，是否严格满足仍以新 CI 为准。
- 进程树：cmd→Node→Go 都必须属于 Job，终止后原进程句柄结束。
- escape：CREATE_BREAKAWAY_FROM_JOB 必须返回 5。尝试 WMI Win32_Process.Create 与自身 S4U 计划任务；先验证服务接口可用，再要求明确拒绝 5 或新 PID 仍在原 Job。成功逃出应让该检查失败；随后按 SID 清理。

进程外 COM 没有受控测试服务，任务/WMI 的权限收敛还没有生产实现。不能用某个现成 COM 类不存在作为“没有逃逸”。这些门槛保持未完成；需要受控 COM fixture、服务/namespace ACL 方案或 VM 边界。

第二次 CI 已证明 conhost 进入活动进程记账，却没有使 N+1 负载更早被拒；不能将 ActiveProcessLimit 当作所有系统代建进程的严格总数限制。它自身的内存也须预算：本轮记录每个 conhost 的 privateCommitBytes/workingSetBytes；JobMemoryLimit 是提交内存而非工作集，PeakJobMemoryUsed 按文档包含相关进程。conhost 特殊创建路径的实际内存限额执行仍需额外压力实测，不能由进程数结果推断。DETACHED_PROCESS 只是启动策略，不能阻止程序随后 AllocConsole 或另选控制台标志；ConPTY 未测试。参见 [控制台创建](https://learn.microsoft.com/en-us/windows/console/creation-of-a-console)、[创建标志](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)、[Job 内存字段](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_extended_limit_information)。

## SYSTEM shim 与实际节点服务停止

实验在 SCM 注册两个临时 LocalSystem 服务：节点代理服务启动独立 shim 服务，shim 持有 Job、日志和复制的负载 token。测试实际发送 SCM Stop 停止节点代理服务，再检查 shim 服务及实例仍活着、新控制器重开 Job、读取旧偏移后的日志、终止负载。shim 服务有 90 秒实验租约，结束时注销两个服务；不是生产服务安装方案。

SYSTEM 能打开控制对象作正向对照；实例自建普通 Job 作 API 正向对照。实例必须无法打开受管 Job、更改其 DACL、以 terminate/dup-handle/vm-write 权限打开 SYSTEM shim，错误码必须为 5。Job owner 必须实查为 SYSTEM。参见 [Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)。

## WFP、监听与系统服务旁路

采用独立持久 provider 和权重 0xFFFF 子层；安装用事务，拒绝过滤器设置 CLEAR_ACTION_RIGHT，白名单允许不硬覆盖其他安全策略。ALE_AUTH_CONNECT_V4/V6 按用户 SD 条件过滤，ALE_AUTH_LISTEN_V4/V6 只允许 127.0.0.1 + 随机分配端口。安装子进程退出后新管理进程验证持久规则；动态会话退出后规则须不存在。DWORD/HRESULT 均按 32 位处理，不把符号扩展的 0x80320003 当错误。

TCP 各目标先证明可连，拒绝要求 WSAEACCES(10013)，不能把拒绝连接/超时算隔离；每轮拒绝后宿主再次连到监听器证明它活着。监听先证明可绑定，再要求 wildcard、其他端口、IPv6 回环返回 10013，指定地址端口可用。UDP echo 有无规则对照。DnsQuery_W 使用 bypass-cache，先无规则查询 example.com，再查看按 SID 规则是否仍允许系统服务代发；若仍能解析，检查失败，说明旁路风险。

尚无受控的外部 UNC/SMB、WebDAV、BITS、WPAD 端点，也未验证 UDP resource-assignment 绑定限制、BFE/OS 重启。相关能力不能发布。普通 SID ALE 规则不能证明隔绝系统服务代发；生产需全机层面限制 SMB/DNS/代理、按需关闭 WebClient/BITS，并在专用节点上验证。spike 不擅自修改全机服务和出站默认策略。参见 [WFP 过滤器](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter0)。

清理按 provider 枚举过滤器，回收意图日志遗漏的孤儿规则，再删除专属子层和 provider，最后按 key 和 namespace 再查；不删除 Windows 内置对象。

## NTFS 与 AppContainer

配额只在本次 128 MiB临时 VHD 上操作，独立验证卷 GUID 不同于系统/宿主卷。通过 GUID 设置卷根和数据目录 ACL，实例创建 32 MiB基线文件，核对文件 owner 是实例 SID，删除后才启用 4 MiB用户配额；再写必须出现磁盘/配额错误，访问拒绝不算。销毁整个测试卷同时销毁该卷配额索引，不改系统卷。先删挂载点、再卸载 VHD并查询确认；不存在文件清理幂等。生产必须评估整卷配额和由 SYSTEM 创建文件的计费归属。参见 [fsutil quota](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/fsutil-quota)。

AppContainer 继续使用实例 token。原生 CreateEnvironmentBlock(FALSE) 提取必要 profile 字段、过滤任意父环境，保留 SYSTEM profile 创建者的 LOCALAPPDATA 基础路径，排序并检验 Unicode 双零；先证 Node 在普通实例账号下可启动，再测四种 capability/回环配置。每种检查实际 SID/LowBox、Node、shell、127.0.0.1 监听、宿主连接和反连，以及 synthetic 其他实例/宿主敏感文件返回 EACCES；无网络 capability 要求明确 EACCES，宿主接口有正向对照。没有测试真实 DSH；SYSTEM profile 与实例 token 的兼容性仍需复测。开发入站豁免不是生产方案。参见 [AppContainer](https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer)。

第四轮把 Node 从已暂存的 tools 副本复制到 `app/bin/node.exe`，脚本也在 bin，账号和本次 package SID 都显式 RX，运行目录各祖先可遍历；不依赖 runner hostedtoolcache 或 Program Files 的 ACL。`app/output` 和预建 JSON 文件同时授予两种 SID 修改权限，显式 Low IL 标签，保留 SYSTEM owner/OWNER RIGHTS，不扩大到 ALL APPLICATION PACKAGES。输出实际路径 SDDL（含标签）供 CI 核对。普通账号基线现在同时验证脚本可读和输出可写。

AppContainer 用 DETACHED_PROCESS 和 STARTF_USESTDHANDLES，HANDLE_LIST 只传 NUL stdin 与 stdout/stderr 文件句柄。日志位于受保护实例目录，结束 Job 后嵌入 JSON，每流最多 32 KiB。Node 内联 bootstrap 向 stderr 输出 `S3_PREFLIGHT`（实际 node/脚本路径权限、输出写入及 errno），即使脚本加载失败也有诊断。等待同时轮询进程和完整协议字段，预建 `{}`/bootstrap JSON 不算成功；提前退出带十六进制退出码，运行中截止与退出分开。每种配置的 `*_process_diagnostics` 记录清理前退出码/状态与 stderr，259 表示取样时仍在运行，不是一次自然退出。新权限布局尚未在 LowBox 下实测，不能把旧超时直接归因于某一个 ACL。

## 清理与发布判定

先登记资源意图再创建；停止并删除专属 SCM 服务、停止注销该 SID 的计划任务，终止可查询的 SID 进程，再删除用户 profile、LSA 权限、账号/组，回收 WFP provider、卸载销毁临时卷、删除受保护运行树。已知公共写入 canary 名称可确定并单独清理，不能递归清扫 Windows/Public。审计进程、profile 注册表项、Job、账号/组、服务、规则/子层/提供者和运行目录，外层另查总控计划任务。

仍缺任意散落文件的可靠 per-SID 清单，受保护进程不能仅靠枚举推断，强制取消也不保证 finally。只有一次性 VM 可运行本实验；生产需要文件系统边界或可靠清单。任一缺失发布门槛、负向检查失败、清理错误都阻止宣称隔离能力通过。报告保留 unsupported/fail，不将缺项吞掉。
