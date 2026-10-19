# 0018 Windows 原生隔离验证与发布门槛

- 状态：提议
- 日期：2026-10-19
- 关联：[ADR 0010](0010-windows-native-isolation.md)、[节点代理](../architecture/node-agent.zh.md)

## 背景与现有证据

ADR 0010 面向可信团队选择原生进程驱动，隔离弱于容器。本草稿记录 S3 spike 和安全审查后的验证方案，不直接改变既定架构，不将原型合入主分支。

第一轮 CI 覆盖 Server 2025 build 26100、Server 2022 build 20348，Node v22.23.3。SYSTEM batch 启动、CPU、进程树、旧 shim 重接管、受控 TCP 出站/回环和 ACL 有部分证据。第二轮修复了状态码符号扩展、LogonUser 类型 10 误用、进程数判据、配额 ACL 和环境块缺项。第二次 CI（run `39222617442`）确认两种镜像的 WFP 移除/清理、登录权限核对和 NTFS 配额通过；Server 2022 内存仍停在 44 MiB并超时，Server 2025 通过；每层 CREATE_NO_WINDOW 仍产生 conhost，四个负载加四个 conhost 被记为 8 个活动进程；AppContainer 四配置均等待 JSON 超时，普通账号 Node 基线通过。第三轮重设安全边界，第四轮处理这三项残留，两轮均尚未复测；旧结果不证明新模型通过。

## 提议

1. shim 使用独立 SYSTEM/服务身份，只有负载使用实例 token。Job 由特权方创建，显式 SYSTEM owner、OWNER RIGHTS 和不授予实例的 DACL。实例打开 Job、更改 DACL、操纵 shim 必须明确拒绝；有特权和实例自有对象正向对照。
2. 实例根和控制元数据不由实例所有，不授予根的 WRITE_DAC/WRITE_OWNER/DELETE。工作区根只有必要创建权限，子项继承修改权限；运行根不用 AU 读执行，工具按 SID 只读。特权读写/删除检查并锁定路径组件，OPEN_REPARSE_POINT 拒绝重解析点，按句柄删除。junction 正负对照及并发替换验证是门槛。
3. 每种限额独立验证，挂起创建→加入 Job→恢复，不启用 payload breakaway。必须补 CREATE_BREAKAWAY_FROM_JOB、计划任务 S4U、WMI 和进程外 COM 逃逸。创建失败要求具体拒绝码，创建成功必须仍在原 Job；不能因直接派生树正常就宣告没有逃逸。活 conhost 同样计数，短暂退出 PID 与活进程分开。
4. WFP 使用专属 provider、最高权重子层、拒绝硬裁决、事务、按 provider 回收孤儿规则。只允许白名单的发布门槛包括 TCP/UDP/DNS/UNC/SMB、WebDAV/BITS/WPAD 等系统服务代发，以及按 SID 限制监听地址和端口。单纯 ALE_AUTH_CONNECT 成功不足以发布该能力。
5. 账号拒绝交互、远程交互、网络和服务登录，核验 LSA 且实际测试支持的类型 2/3/5。保留 batch 仅为本轮原型。评估 S4U 免存口令，不能假定 S4U 自动绕过登录权或阻止其他 broker 创建进程；任务计划程序/WMI/COM 的授权必须另行收敛。
6. 实例存活用真实节点代理 SCM 服务停止测试：独立 shim 服务不随代理进程/父 Job 生命周期结束。再由新控制器重开 Job 和日志，并验证停止负载整棵树。
7. NTFS 配额须先证明实例有写权限，核验文件 owner 是实例 SID，再设置硬上限并证明具体配额错误；只在独立临时卷测试。AppContainer 除 Node/监听/shell 兼容性，还必须验证文件与无 capability 网络拒绝；开发豁免不能成为生产依赖。
8. 清理须覆盖服务、SID 进程、用户 profile、实例自建计划任务、外部写入、配额和 WFP 所有对象。未知残留不能写成“账号已删除所以已清理”。强制取消仍依赖一次性 VM 的销毁。

## 第三轮已实现、待 CI

- SYSTEM shim 与独立节点代理 SCM 服务、实际 Stop、Job owner/DACL 和句柄访问负向测试；复制令牌句柄而非传口令。
- 根/工作目录权限分离、OWNER RIGHTS、重解析点防护与实际 junction 对照，公共目录和 tools 的写入负向探测。
- breakaway、WMI、S4U 任务创建探测和 SID 清理；进程数逐 PID 诊断。
- 专属 WFP provider/子层、事务和 hard block、按 provider 清理、TCP 监听正负对照、UDP echo、bypass-cache DNS。
- 网络/服务登录拒绝、CPU 完成标志及不限额对照、AppContainer 文件/无网络 capability 拒绝、配额文件 owner、服务/profile/任务/进程/已知 canary 清理。

以上仅为原型实现，不是行为已通过。JSON 中的 unsupported/fail 要原样保留。

## 第四轮已实现、待 CI

- **控制台与总量**：原继承控制台、每层 CREATE_NO_WINDOW 两种观察保留；新增父及全部 Go 子进程 DETACHED_PROCESS 的严格断言。只有 Job 限额/活动计数均为 4、父+三个预期子进程存活且 N+1 返回 1816 才通过，任何活 conhost 都不能忽略。生产后台负载采用无控制台和显式日志句柄，真实派生负载同样需约束并复测；ConPTY 未验证。启动标志不能禁止之后的 AllocConsole/其他控制台模式，因此不能据此承诺恶意负载的严格总进程上限。[Windows 控制台创建文档](https://learn.microsoft.com/en-us/windows/console/creation-of-a-console)说明 DETACHED_PROCESS 的无控制台语义。
- **conhost 资源**：已有 CI 证明它计入活动记账而特殊创建未受同一进程数限制。新工件逐 PID 记录私有提交/工作集及 Job 峰值，不把 conhost 排除在预算外。按 [Job 内存字段定义](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_extended_limit_information)，内存上限衡量提交、峰值包含相关进程；conhost 特殊创建路径的内存限额执行尚未单独压力验证，不能从计数旁路推断内存是否同样旁路。
- **AppContainer 权限与诊断**：暂存 Node/脚本置于不可写的 app/bin，对实例账号及本次 package SID 双重 RX，避免依赖 hostedtoolcache ACL；独立 app/output 和预建 JSON 双重修改权限并显式 Low IL，不向 ALL APPLICATION PACKAGES 放权。输出实际 SDDL。普通账号基线增加脚本读取/输出写入，LowBox 使用显式 HANDLE_LIST 传递标准流，捕获清理前进程状态、退出码和有界 stderr；内联 bootstrap 报实际路径访问/errno，轮询区分提前退出与活进程报告不完整。旧证据不足以认定单一根因，新的 SID/标签布局及 SYSTEM profile 兼容性必须由 CI 判定。
- **内存负载与等待**：不限额 192 MiB正向对照，随后 96 MiB Job 下按 1 MiB块 VirtualAlloc 提交并触碰，每次尝试前同步 JSONL。释放块后才生成最终 JSON，绕开 Go 堆接近限额时的分配/回收行为。必须完成并退出 0、捕获内存/配额错误 8/1455/1816、实际提交不足 192 MiB、Job 峰值相符才通过；等待 20 秒超时仍失败。旧 44 MiB停滞只能确认负载未退出，没有栈证据可精确归因；新负载报告不能视为旧 Go allocator 行为已查明。

本机第四轮验证为 build/vet 通过、24 个顶层测试和 47 个子测试通过，仅覆盖非特权逻辑。第三轮所有安全门槛继续有效。

## 尚未完成的发布门槛与替代

- **任意进程外 COM、broker 授权收敛**：没有受控 COM 服务 fixture；未修改共享任务计划程序/WMI namespace ACL。WMI/任务若创建出 Job 外进程就记录失败，不能忽略。补受控服务、按实例组授权方案及完整回归；或改 VM/容器边界。
- **S4U 免口令且去 batch**：仍需实现/验证认证包、可信 LSA 连接、特权、登录类型与 token 使用。现有随机口令不落盘，但 Go 字符串不可承诺完全清除。[LsaLogonUser 文档](https://learn.microsoft.com/en-us/windows/win32/api/ntsecapi/nf-ntsecapi-lsalogonuser)不能替代此项实测。
- **外部 UNC/WebDAV/BITS/WPAD、UDP 资源绑定、BFE/OS 重启**：没有第二个受控一次性端点或完整重启编排。SID 规则不能约束 SYSTEM 代发；生产要全机 SMB/DNS/代理限制、按需关闭服务并复测，不能靠代理环境变量。参考 [WFP 过滤器语义](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter0)。
- **公共可写区和任意外部文件残留**：标准 VM 的 Public/Temp 等可能允许普通用户写入；probe 报失败而不改全机 ACL。已知 canary 可清理，任意散落文件没有可靠清单。生产需要受限令牌、AppContainer/VM 或独立文件系统方案，不能盲扫宿主机。
- **真实 DSH 与版本覆盖**：合成 Node/Go 不能代表插件、shell、MCP 全部兼容，SYSTEM profile 与实例 LowBox 组合也待复测。只覆盖两个 CI 镜像，不扩展到全部 Windows 版本。

因此“只能访问白名单”“不能逃逸 Job”“无宿主机残留”“可用于不可信租户”目前均不具备发布条件。ADR 0010 的可信团队定位保持，外部不可信租户仍用 Linux 容器或 VM。主控应根据完整 CI 工件补最终决定；本 ADR 继续为提议。

## 主控验收

两个矩阵均保存实际版本、每个正向对照和具体拒绝码；重点核对 SYSTEM shim/Job owner、SCM Stop 后存活、breakaway/broker PID、provider 清理、监听/UDP/DNS旁路、quota owner、AppContainer token/拒绝、所有清理及总控计划任务注销。第四轮还须核对三种控制台的逐 PID/资源证据、DETACHED_PROCESS 严格 4 进程及 1816、内存不限额对照/逐块记录/拒绝码，以及四种 AppContainer 的路径 SDDL、S3_PREFLIGHT、清理前退出码/stderr。不得把超时、路径不存在、服务不可用或一般网络错误当作拒绝成功。当前仍明确报告缺失门槛，即使代码编译通过也不能采纳强隔离结论。
