# 云端 DSH ↔ 桌面 DSH 验收

`cloud-desk.mjs` 驱动节点代理实际创建的 managed 云实例和原生 CLI 安装 tgz 的独立桌面 profile。
使用公开 IAM 登录/实例入口或浏览器登录、DSH Settings/Session RPC 和 Auriga 工具；默认模型响应来自本地固定 OpenAI 兼容服务。
不注入登录态、来源身份、网络入站或回执，不修改系统 DNS、信任或 TLS 校验。真实托管模型模式须显式同意调用。

## 验收口径

两端真正的 Agent 分别执行 `auriga_network_join`，读取工具返回的实际地址，再打开接收。
桌面向云端发送请求，由云端 Agent 调用真实发送工具回复；随后反向再做一次。
`passed` 必须同时具备两次入网结果、四次发送工具结果，以及正确接收端模型输入中的四个不同消息 ID。
发起提示、本地回显、`request_submitted`、宿主 ACTIVE 或云实例 READY 都不能单独算互发通过。

云端已有只读托管提供方时，可配置 `managedCloud: { "allowRealModels": true, "model": "team-pool" }`。
模型必须存在于该实例的 `auriga-managed` 目录；此模式会调用真实模型并产生用量，保留其原有凭据和提供方设置。
桌面仍使用本机回环固定模型，无须配置 `fixture` 或云端模型操作通道。
云端工具证明取原生会话中匹配 call ID 的工具调用与成功结果；收件证明取原生 user/message，
精确匹配消息正文、外部消息 ID 和宿主 request ID。模型复述或发起提示不能充当收件。
桌面证明仍来自真实模型输入，报告分别标明两端模型来源；清理自己创建的活动请求、桌面临时提供方和接收开关。

未完成真实云桌互发前，单测和桌面冒烟只能证明准备部分。部署须具备 managed N1 工具和接收控制，
仅升级宿主不能证明该接线已完成。脚本遇到缺工具或缺路由会失败，不退回 connect 冒充 managed。
接收控制检查 `/auriga/local-login/messaging/receive` 公开入口；先确认两端网络真正 connected，再由 Agent 入网。

## 依赖和桌面准备

Node、pnpm 和桌面 DSH 使用 `e2e/network/support/native-install.mjs` 的现有锁（DSH 0.2.1-rc.3）。
插件由已审的 `scripts/plugin/package.mjs`（集成前取 `origin/agent/codex/auriga-PLUGIN-DIST` 同名文件）生成。
保留脚本来源提交和 tgz SHA256；本脚本校验发布清单，再通过原生 `dsh plugin add` 安装。
运行时和 profile 都放本 worktree 的独立测试目录，不使用用户的 DSH 或全局 profile。

将下列键写入本地 JSON，路径相对于 worktree 根；`state` 必须尚不存在，其父目录须已创建：

```json
{
  "state": ".agent-evidence/cloud-desk/desktop",
  "runtime": ".agent-evidence/cloud-desk/runtime",
  "pluginPackage": ".agent-evidence/cloud-desk/plugin-package/auriga-dsh-VERSION.tgz",
  "packageManifest": ".agent-evidence/cloud-desk/plugin-package/auriga-dsh-VERSION.json"
}
```

```powershell
node e2e/phase1/cloud-desk.mjs prepare .agent-evidence/cloud-desk/prepare.json
```

生成 `desktop/cloud-desk-desktop.json`，后续运行直接复用，不重复安装。
已使用安装版应用准备的独立 `desktop` profile，可按 [统一入口说明](README.md) 提供
`runtime.kind=installed-app` 的安装记录作为 `desktopDescriptor`。启动会核实际内置 CLI 版本、
发行包摘要及 profile 的安装版本，并使用隔离 HOME/user-data-dir；Windows 结束时清理该进程树。
该描述文件复用已有安装，不会让 npm CLI 管理安装版保留的 `desktop` profile。
首次真实登录使用全新 profile；`usedForLiveRun=true` 的 profile 不自动重置或覆盖。
中断恢复可设 `resumeDesktop:true`，必须保持记录中的同一平台与成员；已登录状态还核对真实 subject。
需要正常轮换设备时可另显式设 `reauthenticateDesktop:true`：恢复并核同一成员，调用公开退出接口，
等待远程撤销完成，再正常登录同一成员。它不删除profile或账本，不可用于跳过来源未决操作的真实性检查。
未做真实登录的 R3 profile 可以直接续用；不要重复安装或清空身份。

Playwright 复用 `e2e/package-lock.json` 锁定依赖（当前为 1.56.1），不要改变产品 workspace 依赖。
可在独立测试依赖目录运行 `npm ci --ignore-scripts --no-audit --no-fund`，
然后设置 `PLAYWRIGHT_BROWSERS_PATH` 指向测试缓存，运行其中的 `node_modules/playwright/cli.js install chromium`。
`playwrightModuleRoot` 指向该依赖目录；已集成 phase1 依赖时可以省略。

## 运行配置

先读共享栈交接文件。只在交接明确可用时测试；部署修改须另按共享锁规则进行。
运行配置放本地受保护 JSON，不在命令行放口令、TOTP 或票据。必填项：

- `accessFile`：该新测试栈的公开 `var/access.json`。
- `enrollmentFile`：同一个新栈的 `var/enrollment/emergency-enrollment.json`；脚本仅在内存读取，不输出。
- `desktopDescriptor`：上述桌面安装记录；`cloudCwd`：新云实例允许的工作目录。
- `connect`：安装实际发布的公开 connect 配置，含 `platformOrigin`、`issuer`、`clientId`、`providerId`、
  `network.realm`、`network.wssUrl` 和真实 Ed25519 公共 `network.roots`。不能编造根、身份或票据。
- `caFile`：需要时提供该测试安装的公共 CA，仅设置桌面子进程的 `NODE_EXTRA_CA_CERTS`。
- `resolveLoopback`：示例域名在本机测试时可设 `true`，仅给本次浏览器添加 resolver 规则。
- `fixture`：`bind`、`cloudHost`、`desktopHost`。后两项必须是明确可达的本机/私网 IPv4，不能填模型供应商地址。
  默认在当前 Node 启动随机端口；Windows 使用 WSL 时加 `wsl: { "distribution": "Ubuntu", "node": "锁定 Linux Node 的绝对路径" }`。
  WSL localhost 转发不可用时，用 `bind: "0.0.0.0"` 和实际 WSL 私网地址，不改系统转发或防火墙。

R4 复用已有成员时用 `existingCloud: { "credentialsFile": "共享栈的member-a.json路径", "instanceId": "交接的A实例ID" }`，
并指定 `desktopMemberFile` 为同一栈的 `member-b.json`，此时不需要管理员 enrollment，也不会创建新邀请/成员。
凭据文件的 `portalUrl`、`accountId` 必须与实际登录安装和本人实例相符；支持正常 TOTP 和 `platform_totp`，不注入 cookie。
现有实例通过真实“进入实例”按钮打开，停机实例通过“启动并进入”唤醒。
本机示例域名模式只给本次 Chromium 设置 resolver 和明确的 direct proxy，不改系统代理或 DNS。

共享栈测试前须按当前共享规则持锁，结束立即释放。配置
`sharedStackLock: { "locksDirectory": "共享 agent-cache/locks 的绝对路径", "owner": "owner.txt 的完整内容" }`，
精确核对持有人，不限制任务名。也可用 `AURIGA_SHARED_STACK_LOCKS_DIR`、`AURIGA_SHARED_STACK_LOCK_OWNER`；
目录省略时从 worktree 向祖先查找 `agent-cache/locks/compose-stack/owner.txt`，缺锁或持有人不符即拒绝。
NAS 等远端测试在已有独立协调授权时，可显式设 `sharedStackLock:false`，不要求本机共享栈锁。
此开关仅控制测试协调，不授予部署、重建或停止服务的权限；默认仍须声明并核对锁持有人。
云实例禁止访问宿主私网时，可用 `fixture: { "cloudInstanceId": "同一个A实例ID" }`：
脚本通过 Docker exec 的 stdin 在该实例临时启动仅听回环的随机端口模型服务，桌面模型另听本机回环。
不写云端插件/配置文件，不发布端口、不修改网络隔离；私有控制键不放 argv，结束关闭两个临时服务。
两侧夹具使用同一 run ID 汇总真实请求/回复证据。主会话固定使用 standard Agent 预设和4096-token模型，
64-token的标题辅助请求只返回标题，不触发工具或充当收件。报告只记请求结构和工具名，不记原始提示。

HTTPS 共享栈可配置 `httpsCloud: { "credentialsFile": "本栈owner凭据文件", "instanceId": "本人实例ID" }`，
替代浏览器登录分支；当前此传输只验收域名模式。它真实完成 IAM 表单/MFA、读取 `/me`、打开本人实例，
再通过 `/open/{id}` 的真实重定向和合法 Domain cookie 换取实例访问权限，不注入登录态。
`caFile` 同时用于 Node HTTPS 证书校验；`dnsMappings` 为本次测试域名到回环/私网 IPv4 的映射。
桌面通过 Node `--import` 预载映射，只给子进程补这些域名的 `NO_PROXY`，保持原 URL、Host、SNI 和 TLS 校验。
此分支报告 `authentication=public-iam-cookie-redirect`，不宣称浏览器 UI 已验收。

第二成员可先使用 `invite-member`：`adminCredentialsFile` 指向同一测试栈管理员材料，
`desktopMemberFile` 指向调用方指定的私有目录中的文件。目录须预先创建且仅本人可读写，
脚本在登录/邀请前检查父目录及已有文件，不自动收紧任意父目录，也不接受链接路径。
Windows 目录必须禁用 ACL 继承且仅当前用户获授权（可按 `scripts/beta-ui/private.ps1` 设置）；
新文件继承该私有 ACL 后再独立收紧并校验。Linux 检查当前 owner 及 group/other 无权限，新文件以0600独占创建。
它发现真实 provider 和内置 member 角色，
正常创建一次性邀请、注册及 MFA；邀请显式绑定 member 角色，避免无角色设备注册网络时返回 403。
再次执行会保留同一成员，只通过管理员公开角色绑定接口补齐 member 角色，不重复创建账号或授管理员角色。

```powershell
node e2e/phase1/cloud-desk.mjs invite-member .agent-evidence/cloud-desk/run.json
```

统一入口第⑤项支持凭据配置中的 `cloudDesk` 对象，内容就是上述运行配置，另加 `testOnly:true`。
可加 `output` 指定全新的原生证据目录；否则在统一报告旁分配随机目录。运行方式：

```powershell
node e2e/phase1/run.mjs --base-url https://portal.example.com:8080 --admin-credentials PRIVATE_CONFIG --only 5 --output .agent-evidence/cloud-desk/phase1.json
```

第⑤项只在四条真实收件及全部清理均通过时 PASS，并保留原生报告状态和 run ID；失败 trace 只取实际失败请求。
仅选择⑤即使通过也有七项 SKIP、退出 2，不代表八项完整验收。

只核云端是否能走到真实入网工具（桌面入口尚不可用时）可执行：

```powershell
node e2e/phase1/cloud-desk.mjs probe-cloud .agent-evidence/cloud-desk/run.json .agent-evidence/cloud-desk/cloud-probe-001
```

该命令不需要桌面 connect 配置；正常登录 A，核 managed Node 健康和原生 ACTIVE，读取 Settings/Session 公开 API，
由真正 Agent 调固定模型发起 `auriga_network_join`。面板已知 502 会单独记录并继续；401/403 不跳过。
`probe-passed` 也明确带 `conversationPassed=false`，不算双向互发通过。
失败记录公开会话事件类型、白名单错误分类和模型连通性，清理自己的测试 provider/key 和活动请求，保留会话历史。

共享栈若仅有 DNS HTTP 入口，桌面 connect 会按产品规则拒绝；应由安装发布有效 HTTPS 或受支持的私网 IP 配方，
不能用 HTTP 域名伪装回环、关闭 TLS 校验或从用户私有凭据库补材料。
Docker 中的云实例还须按实际部署的出站策略允许访问该临时本地模型端口，脚本不修改出站策略。

```powershell
node e2e/phase1/cloud-desk.mjs run .agent-evidence/cloud-desk/run.json .agent-evidence/cloud-desk/run-001
```

输出目录必须全新且位于本 worktree。`report.json` 白名单记录阶段、状态、消息/实例 ID、合法 trace ID 和清理结果，
不保存 HTTP 正文、浏览器 DOM、cookie、模型系统提示或 fixture URL 能力键。非通过退出 1。
结束时取消自己创建会话的活动请求、关闭接收、移除自己唯一的模型配置/测试 key，停止临时模型及桌面进程、关闭浏览器。
新成员、云实例和会话历史作为测试证据保留，不停止共享服务或他人实例。

NAS 等远端测试可在 `fixture.cloudInstanceId` 旁设置受信任的
`fixture.containerCommand=["操作通道程序","固定参数"]`。入口不开 shell，追加唯一的实例 UUID
作为最后一个 argv，stdin 是要在该实例 Node 中执行的源码，stdout 为原始结果，stderr 不进入报告。
操作通道须限制目标实例、保护源码和结果中的临时 fixture 能力键，并在退出/取消时清理自己
启动的进程；不得打印 native 令牌或借此改宿主/DSH。远端启动和单次控制各限时 60 秒。
不配置时仍沿原本机 WSL `docker exec`；该传输只替换测试模型的操作通道，不替代真实
IAM、插件、WSS、READY 或消息收件证明，也不能作为真实中转站第⑥项证据。

编程调用 `run(cfg, output)` 的真实托管模型验收可提供受信任的
`cfg.readReceiptWitness({sessionId, marker})` 异步只读回调。原生宿主用收件项 ID 去重，
它与消息 ID 不同；回调须从真实、未改写的 durable inbox 中读取已完成的收件映射，返回
`{sessionId, marker, messageId, recipientId, stage: 'committed'}`，尚未提交时返回 `null`。
操作者应同时核对持久收件项、宿主会话及 `host_accepted` 回执；不返回正文、签名能力或凭据。
脚本再将消息头/唯一正文、原生 `user/message` 的请求 ID 和这份映射逐一比对。
模型参数、助手复述、已提交发送及未提交的 inbox 项不能提供此见证；未配置回调时保留原有直接请求 ID 匹配。

云端 join 只有明确返回 `unavailable` 且 `retryable: true` 时，调用方才在原 120 秒总截止内
最多重试三次并保留每次工具结果。发送动作不自动重试，最终仍须四条真实收件及全部清理通过。

## 检查

```powershell
node --test e2e/phase1/cloud-desk.test.mjs
node --test "e2e/lib/*.test.mjs" e2e/phase1/cloud-desk.test.mjs
node node_modules/eslint/bin/eslint.js e2e/phase1/cloud-desk*.mjs
node node_modules/prettier/bin/prettier.cjs --check "e2e/phase1/cloud-desk.*"
```

如实际首失证明 rc.2 缺宿主能力：将 `deploy/compose/real-dsh/package.json` 的 DSH 精确钉到 0.2.1-rc.3，
按 Linux amd64/glibc 原安装策略重新生成 `package-lock.json`，同步 `versions.json` 的 `dshVersion` 和 `lockSha256`，
保留所有新解析依赖 integrity，检查无无关锁项变化，跑 `e2e/lib/real-dsh.test.mjs` 和镜像构建后再拿锁部署复验。
影响包括镜像重建、宿主公开 API 变化和现有实例重建；不能只改版本文字或把桌面 lock 复制成云镜像 lock。
