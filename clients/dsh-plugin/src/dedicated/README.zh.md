# 显式专用实例模块

`auriga-dsh/dedicated` 仅由安装侧为管理员明确授予的、当前成员独占的 dedicated 实例加载。它不与 `shared`、`shared-ports`、`shared-browser` 或原个人登录模块同时加载。shared 的有限工具及非 owner 隔离契约保持独立。

Node 正式 launcher 使用批准包内固定入口 `file:///opt/auriga-dsh/src/dedicated/plugin.js`，公开 boot 的 bare module anchor 固定 `file:///opt/dsh/package.json`。这样 ClientModuleRegistry 沿 pathLike/nearestPackage 发现同一包的 `dsh.client`；不能假定 bare 子路径会自动发现客户端资源，也不能为了发现资源加载旧个人 owner 主入口。

安装参数为 `configFile` 和正式原生页面的绝对 `distIndex`。固定配置位于 `/var/lib/auriga/pools/<physicalInstanceId>/control/dedicated-host.json`；它含完整 dedicated binding、现有 Node socket、Edge 公钥、enterprise/model capability 公钥及平台模型配置。成员、物理实例、placement、epoch 和 revision 在本进程固定，配置变更须受控替换进程。浏览器不能选 mode、owner、验证函数、租户或成员。

正常页面为 `/`，使用原生 dist 与独立的 `/auriga/dedicated/browser/rpc`、`stream` POST carrier。每次请求都要有 Edge 当前签名身份并在线核 W。本人唯一绑定可映射到本实例原生 owner Peer，以支持公开 Gateway 的原生工具和问答；该 Peer 保留在服务端，每个动作、交互结果及流帧仍核当前 binding。服务端不签发 owner cookie，旧 `/api` HTTP 被拒；Node/Edge 必须同时阻断原 `/connect`、WS 和升级旁路。会话先经 K 登记成功再发布，列表与投影按 K 归属过滤。

`GET /_auriga/dedicated-profile` 仅在实际模块和资源均符合时返回 `auriga.dedicated.v1`，精确能力为 `pluginInstall: true`、`privateSkills: true`、`includeDefaultRoots: true`、`toolPresentation: native`。它验证真实 native ToolRuntime、SkillFilesystem、ShellEnvRegistry、PluginManager、可写专用 profile，且拒同时运行 shared 模块。此四项不代替某个 Shell、MCP 或后台任务控制器的安装事实。

Node 校验签名、ACL、租约和高水位后，将原 Resource view 与逐项 manifest/artifact 摘要和安装 revision 原子写入 `dedicated-resources.json`，并把只读缓存挂载到 `/run/auriga/dedicated-resources`。模块再核真实 `resourceSnapshot` 与完整当前意图、文件摘要、归档内容和 Skill 解包字节。Plugin 经公开 PluginManager 安装、启停和移除，Skill 经公开 SkillRegistry provider 加载与移除；浏览器不能绕过平台资源意图直接安装任意 bundle。

`GET /_auriga/dedicated-resources` 返回精确 binding/view/intentDigest/hostInstanceId 的实际 `ready`、`applying`、`restart-required` 或 `failed` 回执。没有 HMR 时必须由 Node 受控排空并重启。旧进程不会把磁盘已修改视为加载完成；新进程须观察批准 plugin 的实际 active 行才可 ready。回执中的失败不会自动冒充成功，未知提交结果也不会盲目重放。

`tools/execute` 和同步 `shellEnv.collect` 通过原生 opaque execution token 关联。真实调用借用本回合普通父 lease，并按 typed 状态持有可选企业子任务，注入 `DSH_AURIGA_ID`、`DSH_AURIGA_TOKEN` 和现有 helper 变量，保留原生 `DSH_SESSION_ID`。凭据刷新沿同一个任务 handle 续期，退出、撤销或资源变更先隔离环境，实际工作排空后才确认 Finish。后台 handle 保留到原生 Jobs registry 的真正终态。已经启动的 OS 子进程环境不会被改写，长任务应使用会续期的回环 helper。

当前普通任务复用真实 turn 的唯一父 lease。只有安装方已经实际加载 LocalJobRegistry 和正式 job controller，专用模块才会接力后台任务；本模块不自行安装 Jobs 或 Shell，也不代表 NAS 已有该能力。接力在原生注册当场交叉核对 SessionStore 的 `job/registered` 日志、真实 registry 的 registered/get/list、当前异步工具执行与 callId/rootCallId；结果 JSON 或旧同成员 jobId 不能授权 retain。正常 turn/end 后仅此已有 lease 可续期，下一回合等待它结束。stopping/kill 先撤销环境及 helper 权限，但预算和资源屏障保留到所有真实 job settled、企业子及普通父 Finish 均确认。helper 依真实 capability→taskId 映射前后核私有活动 row，并向外呼传播该 row 的取消信号。

Node launcher 需使用公开 app-boot profile 层组合，专用可写目录固定为 `/home/dsh/.host/.dsh/profiles/auriga-dedicated`，name 为 `auriga-dedicated`、installAnchor 为 `/opt/dsh/package.json`。本人选装更新 Resource view，不能修改 W placement binding digest。Node、W 与 Edge 的正式接线和正常页面部署由主控另行集成验收；本目录的纯模块测试不代表 NAS 已部署。专用实例消息接收尚未接通，不能安装 shared 收件器代替。

目标测试使用锁定 rc.3 包，通过 `AURIGA_DSH_PUBLIC_PACKAGE_ROOT` 指定本地公开包根目录，运行 `node --max-old-space-size=512 --test test/dedicated/*.test.js`。测试启动真实 Cordis、Peer、Gateway、SessionController、ToolRuntime、ShellEnv、Jobs、SkillRegistry 和 PluginManager 的纯组合，不启动完整 DSH、Agent、PG、浏览器或模型供应商。外部包进程在资源协调测试中为明确替身；另有真实无 HMR PluginManager 选择测试。Node wire/protoJSON fixture 来自同仓 Node `wire()` 的 Go overlay 纯测试，生成源位于测试目录。
