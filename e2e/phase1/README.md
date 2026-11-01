# 第一阶段一键验收

Node 22.12+。脚本面向一套**已经部署**的测试栈，只通过给定地址访问；不会部署、清库或操作 NAS。管理员凭据从私有 JSON 文件读取，不放命令行。浏览器步骤使用 Playwright，依赖单独锁在 `e2e/package-lock.json`，不修改 pnpm 工作区。

```powershell
# 仓库根目录；安装精确锁定的测试依赖
cd e2e
npm ci --ignore-scripts --no-audit --no-fund
cd ..
# 首次需要浏览器；也可在凭据文件中指定已有 Edge 的 channel
node e2e/node_modules/playwright/cli.js install chromium

node e2e/phase1/run.mjs --base-url http://127.0.0.1:8080 --admin-credentials .dev/phase1-admin.json --only 1,2 --output e2e/phase1/.local/phase1-report.json
```

NAS 验收从本机执行同一个入口，`--base-url` 换为已部署的 NAS 门户地址，例如 `https://portal.example.com`；凭据文件中的 `admin.url` 换为对应管理入口。域名和纯 IP/HTTP 均支持；管理员和门户使用独立浏览器上下文。脚本不改 DNS、系统信任或 TLS 校验。私有 CA 需由运行环境预先信任。

IP 模式经既定 HTTPS 反代呈现时，私有凭据配置可显式增加
`portalProxy: { "upstreamOrigin": "http://192.0.2.10:8080", "publicOrigin": "https://portal.example.com" }`。
前者必须精确匹配部署 access 的门户 Origin，后者必须为 HTTPS 且精确匹配 `--base-url`；
不允许凭据、路径、query 或 fragment。它只声明浏览器预期入口，不修改 access、issuer、
请求 Host/Origin 或服务器返回的邀请链接；真实邀请与实例导航仍须留在该公网入口，失败照常记 FAIL。

完整运行省略 `--only`，桌面端另加 `--desktop-dsh .dev/phase1-desktop.json`。`--only` 接受逗号分隔编号，例如 `--only 3,5`。编号 3–6 必要时自动创建并登录本次受邀成员、准备双端；未被选中的编号仍标 SKIP，准备动作不冒该编号已单独验收。不要在生产租户运行：每次会留下一个验收成员及其云端实例，脚本不删除共享栈业务数据。

## 输出与判定

默认写当前目录的 `phase1-report.json`，`--output` 可改位置；同目录另写带时间/随机后缀的独占快照，重试不覆盖首失；同时打印每条状态、人读原因和失败取证命令。请将报告保存在忽略目录，勿提交现场地址。无效参数、无法读取凭据或无法写报告属于启动错误，退出 1，不保证生成报告。

- PASS：该项全部实际断言成功。
- FAIL：已配置能力执行失败，包括连接/TLS/超时、HTTP 错误、身份不一致、缺少真实回复或用量。失败不阻断后续独立条目。
- SKIP：未选中，或尚未交付/接线的能力缺少输入。配置后返回 404/503 不降级成“未部署”。
- 退出码 0：全部八项 PASS；1：存在 FAIL、启动错误或清理失败；2：没有 FAIL，但存在 SKIP。`complete` 仅在八项全部 PASS 时为 true；有 SKIP 的报告不能作为第一阶段总验收通过。

HTTP 健康/中转站/日志探针发 W3C `traceparent`；浏览器正常页面请求保留服务端返回的 trace，不拦截改写 API。FAIL 附 `traceId`、`traceAcknowledged` 和 `scripts/logs/collect --trace-id <32位hex>`。未收到合法响应 trace 时，明确标为“仅客户端生成”；连接失败不能声称服务端已有链路。日志命令在部署主机的仓库根执行；`scripts/logs/collect` 依赖 LOG-1 交付，本分支不创建或执行它。报告不存请求 URL query、响应正文、cookie、密码、TOTP 密钥、虚拟 Key 或 HAR。

## 管理员文件与健康清单

文件为 UTF-8 JSON（接受 BOM），权限仅给验收操作者。以下为结构示意，替换占位字段，不能直接当真实凭据使用：

```json
{
  "admin": {
    "url": "http://127.0.0.1:8081",
    "username": "ADMIN_USERNAME_FROM_PRIVATE_FILE",
    "password": "ADMIN_PASSWORD_FROM_PRIVATE_FILE",
    "totpSecret": "ADMIN_BASE32_TOTP_FROM_PRIVATE_FILE"
  },
  "browser": { "channel": "msedge" },
  "healthInventoryComplete": false,
  "healthChecks": [
    { "name": "iam", "url": "/api/iam/v1/login/providers", "kind": "providers" }
  ]
}
```

省略 `browser` 使用 Playwright 安装的完整 Chromium headless 通道（不使用独立 Headless Shell）。管理员默认使用 `emergency` 身份源；正常管理员可明确配置 `admin.providerType: "team-account"`，按真实口令及 TOTP 表单登录。WebAuthn、外部跳转、二维码暂不支持。NAS 的应急账号仍限真实回环，先按[公开首次管理员流程](../../docs/dev/nas.md#首次正常管理员真实回环)建正常账号再验收。邀请注册选 `team-account`；新用户名、密码和首个 TOTP 在本次运行内生成。重新登录使用全新上下文，核相同账号 ID，不复用注册会话。

① 默认仅探测公开 IAM 身份源；这不足以证明所有容器 healthy，所以标 SKIP 并列出已通过子检查。部署操作者需在 `healthChecks` 列全本次 compose 服务的实际健康 URL，包括数据库/NATS/节点等可通过部署的运维健康入口或汇总端点观察到的状态；确认清单完整后才设 `healthInventoryComplete: true`。不要用静态首页替代服务健康。支持 `kind: "spring"`（JSON `status=UP`）、`providers`（`items` 数组）及 `http`（HTTP 200 且可解析 JSON）。URL 可相对门户，也可为操作者可访问的完整运维地址。任何检查失败即 FAIL。

中转站未配置时第①、⑥、⑦项为 FAIL，不能作为未部署 SKIP 处理。管理就绪检查读取平台同源池列表；列表鉴权失败或后端故障均 FAIL。

②③ 共用 `cloud-ready.mjs` 中一套真实浏览器步骤，不再保留另一套邀请/登录/open 实现。② 从管理台控件创建邀请，成员点击公开邀请页、设置口令、从真实页面完成 first_totp 登记，再用空 cookie jar 以口令和 MFA 重新登录，核相同账号与邀请已消费。登录的 OTP 字段按真实 form 的 kind 识别（登记 otp、登录 code），避免复用已消费的 TOTP counter。

③ 原门户按钮发 open，同成员第二个门户页只读观察本人实例 `ownerId`、running/pending_restart、ready=true；按 access 配置核准确实例入口，点击真实 DSH 的 Auriga 侧栏，面板必须可见。随后断言真实 bootstrap 协议、插件版本、managed context、资源界面与无二次口令表单；无 cookie 浏览器访问同一插件入口必须 401。已内置这些实际断言，不再接受 `cloud.loggedInSelector` 代替登录证据。它不证明 Team 入网或云端/桌面互发。

DSH 首启的“预览版说明 / Preview Notice”与可选模型设置会遮挡侧栏。脚本仅通过对应对话框的“继续 / Continue”和“稍后配置 / Configure later”完成公开引导，不写模型密钥，不调用模型；未知对话框保持阻挡。③还要求部署已提供签名面板静态文件、清单及独立的面板验签公钥；缺失时 bootstrap/面板加载记 FAIL，不放宽验签或用普通连接标签替代。

配置须提供公开部署生成的 `access.json`（以 `access` 对象提供）和 `admin`；也可仅用 `composeDir` 指向同一部署的 compose 目录，自动读取 `var/access.json`、真实镜像元数据和应急 enrollment。Windows 可读 WSL UNC 路径，秘密不进入报告。

`shared` 默认为 true：复用在线、可调度、Docker、template=real-dsh-e2e 的节点，不改节点。全新部署设 false：脚本要求零节点，经管理台签加入令牌写同一 composeDir 的既有 var/join/token，等待上线再设模板标签。NAS可配置 `nodeTemplateLabel` 和既有私有 `nodeJoinTokenFile`，按公开 `join-node.sh` 交付令牌。只有不验节点的独立部署可设 `nodeEnrollment:false`，其结果不能冒节点加入通过。

仅重跑③或其他后续步骤时，可设 `existingCloud: { "credentialsFile": "先前真实成员交接文件", "instanceId": "该成员已有的实例ID" }`。
它在空Cookie上下文正常登录、核实际 `/me` subject 和实例归属，再点击真实“进入实例”或“启动并进入”，等待同一实例READY；不创建邀请或成员。
已有成员模式禁止选择②，不能用一次普通复登冒充邀请接受。完整邀请流程的私有交接另含最近TOTP计数，减少立即续做时的重放。

第④项可复用已经通过安装版应用公开插件管理器装包的独立 `desktop` profile：
设置 `desktop.installedApp:true`，`desktop.credentialsFile` 指向该平台测试成员的私有交接文件，
`--desktop-dsh` 指向以下描述文件。`state` 必须位于本 worktree，不能链接到用户 HOME。

```json
{
  "state": ".agent-evidence/desktop-app",
  "profile": ".agent-evidence/desktop-app/dsh/profiles/desktop",
  "runtime": {
    "kind": "installed-app",
    "executable": "C:/Program Files/DeepSeek Harness/DeepSeek Harness.exe",
    "cli": "C:/Program Files/DeepSeek Harness/resources/app.asar/dsh/node_modules/@deepseek-ai/dsh-desktop-host/lib/cli.js"
  },
  "installation": "installed-app-tarball",
  "dshVersion": "0.2.1-rc.3",
  "pluginPackage": ".agent-evidence/plugin-package/auriga-dsh-VERSION.tgz",
  "packageVersion": "VERSION",
  "packageSHA256": "发布清单中的实际SHA256"
}
```

可选 `connect` 为管理员提供的公开安装配置，平台地址必须与验收目标相同。
脚本核 CLI 实际版本、包摘要和该 profile 安装版本，以独立 HOME 和 Electron user-data-dir
启动应用，再经真实启动链接建立浏览器会话、正常登录/恢复同一成员、验证原生插件 active 与功能页。
只清理自己启动的进程树，保留 profile；不会用 npm DSH 代替安装版，也不会伪造 Cookie。

本机保留示例域名可设 `resolveLoopback:true`（或 AURIGA_PHASE1_RESOLVE_LOOPBACK=1）：只给本次 Chromium 设置 resolver 与禁用代理，不改系统 DNS、TLS 或信任；纯 IP 模式不使用此项。HTTP 健康探针仍使用实际可解析的地址（本机可给 healthChecks 配置回环运维端点），该设置不改变 Node 的 DNS。

示例（私有配置文件，不提交）：

```json
{
  "composeDir": "/tmp/auriga-new-run/deploy/compose",
  "shared": false,
  "resolveLoopback": true,
  "memberCredentialsFile": "/private/phase1-member-a.json"
}
```

`memberCredentialsFile` 是可选的测试成员交接文件：默认不写；显式设置才将成功重新登录的测试账号、口令、TOTP 写入该私有文件（0600、独占创建，已存在即失败）。父目录由操作者预先设为仅本人可读，Windows 需使用受限 ACL 的私有目录。不要在代码、报告或终端回显内容。为 CLOUD-DESK 准备两个成员时，分别指定两个新文件运行②③两次；保持账号和实例，凭据由工具从私有文件读。请勿把这些文件提交到仓库。

浏览器仅观察登录响应的状态/步骤字段，在应用导航前保存至内存；不保留票据、token、字段值，不改真实响应或认证会话。避免导航后 response body 被销毁；测试通过随机端口 HTTP 夹具覆盖此竞态、IP/domain CSRF、otp/code、TOTP 和成员交接。所有真实 DSH 页面仍以部署验收为准。

④ `--desktop-dsh` 为本机 JSON 描述文件，不是用户正在使用的 profile。示例：

```json
{
  "dshBin": "C:/tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js",
  "pluginPackage": "C:/artifacts/auriga-dsh-release.tgz",
  "patchFile": "C:/artifacts/phase1-connect.patch-template.yml",
  "url": "http://127.0.0.1/"
}
```

`dshBin` 指向满足插件公开版本要求的 DSH Node CLI；本脚本不升级 DSH。`pluginPackage` 必须为公开流程产出的本地 `.tgz`；使用 npm `install --ignore-scripts` 装入全新 profile，并核包名为 `auriga-dsh`。`patchFile` 按公开 DSH `--patch` 规则配置该插件启用、连接当前平台、免费模型及回环 listener，端口必须使用占位符 `__PHASE1_PORT__`，脚本每次分配随机端口后替换。URL 的端口也会同步替换。不从用户原 profile 复制配置，不修改上游 DSH 源码。

启动使用独立 `DSH_HOME`、HOME、APPDATA、XDG、临时目录和 npm 配置；不继承模型 Key、实例 token 或 Node hooks。仅启动本次临时进程，结束清理其进程树与 profile；清理失败写 `cleanupErrors` 并退出 1，保留目录供诊断。插件安装/登录功能未交付时，缺少描述字段会 SKIP；字段已配但文件缺失、安装失败或登录失败会 FAIL。桌面临时目录在报告旁的 `.phase1-desktop/run-*`，不要并发运行真实 DSH 全链。

凭据文件中补充桌面插件公开登录 UI 操作：

```json
{
  "desktop": {
    "loginActions": [
      {
        "type": "fill",
        "selector": "REPLACE_USERNAME_INPUT",
        "valueFrom": "username"
      },
      {
        "type": "fill",
        "selector": "REPLACE_PASSWORD_INPUT",
        "valueFrom": "password"
      },
      { "type": "click", "selector": "REPLACE_LOGIN_SUBMIT" }
    ],
    "loggedInSelector": "REPLACE_WITH_DESKTOP_AUTHENTICATED_MEMBER_SELECTOR"
  }
}
```

操作类型仅 `click`、`fill`、`wait`，不执行任意 JS/外部脚本。上述 selector 是能力缺口的明确输入，须按 PLUGIN-DIST/CLOUD-DESK 实际交付的原生 UI 填写，不能照抄占位值。`valueFrom` 取本次成员字段；需要动态 TOTP 时可用 `otp`，现有连接流程有其他表单/原生承接步骤时按实际 UI 补动作。

⑤ 可配置 `cloudDesk` 对象（必须 `testOnly:true`），直接运行原生 Agent 的两次入网、四次真实发送及接收端四个不同消息 ID 核验。HTTPS 认证、CA、进程域名映射、隔离桌面安装记录和配置见 [cloud-desk.zh.md](./cloud-desk.zh.md)。单选⑤不会重新执行②④，也不代表八项完整验收。

原有 UI 路径仍可配置 `messaging.testOnly: true`，确认两端默认模型均为免费假上游。`messaging.cloudToDesktop`、`desktopToCloud` 各包含 `sendActions`、`receivedSelector`、`replyActions`、`replyReceivedSelector`。发送动作通过 `valueFrom: "marker"` 填唯一消息；目标必须显示该 marker，再回复 `reply-<marker>`，源端必须收到回复。两个方向都成功才 PASS；旧消息或“连接成功”不算收发证据。

## 中转站与外部虚拟 Key

管理入口仅为管理员来源的 `/api/ai/v1/admin/relay/`，使用正常平台管理员浏览器登录和近期 MFA，不配置管理 Bearer 或服务口令。`relay.baseUrl` 必须与 `admin.url` 同源。`relay.publicBaseUrl` 是门户 `/ai/v1`。浏览器写请求沿用 CSRF，POST 带幂等键。

显式设置 `allowModelCalls:true`；真实模型采用 `upstreamMode:"live"`，每个模型声明 `name`（公开池 ID）、`provider`、`apiBase`（HTTPS 根地址）和 `upstreamModel`。免费夹具采用 `upstreamMode:"fixture"` 且每模型 `testOnly:true`；夹具不能作为真实部署证据。

第⑦项签发全新受限外部 Key，验证普通调用、RPM 429、零预算拒绝、按 Key 查用量和吊销。所有已签发测试 Key 均在 finally 中禁用；没有自动到期假设，清理失败直接 FAIL 并由管理员检查 `phase1-` 名称。

第⑥项通过公开 DSH Settings/Session RPC 核验真实新回复和按 `accountId` 查询的新用量；桌面使用已登录成员提供方，云端用实例已交付的 `auriga-managed`。禁止签发外部 Key 模拟成员。两端均为隔离验收 profile；不读复制托管 Authorization。

池演练须显式 `modelNative.allowPoolDisruption:true`，只支持已声明的双供应商池；禁用一个供应商后验证剩余路由仍有真实对话，最后恢复原配置并读回。发现并发管理员修改则拒绝覆盖并 FAIL。用量中的 provider 字段当前表示 admission 快照，不能冒充 fallback 实际选中供应商，见架构限制。

## 集中日志验证

⑧ 配置 `observability.lokiUrl`、`streamSelector`（例如 `{service=~".+"}`）和 `expectedServices`（本次链路实际应经过的服务列表）。脚本用 Loki 官方 `/loki/api/v1/query_range` 查本次失败 trace；若前七项没有失败，则发一个无副作用的 IAM 404 探针。每个失败 trace 都须在同一处出现预期服务的结构化 `trace_id`、`service`，最多等 10 秒入库。只附收集命令不代表⑧通过。LOG-1/observability 未接线时 SKIP。参考：[Loki HTTP API](https://grafana.com/docs/loki/latest/reference/loki-http-api/)。

## 本机共享栈与回归

先只读工作区 `agent-cache/locks/shared-stack.txt`，有共享栈时直接使用，不停栈、不清数据、不拿锁。无栈时按任务共同规则领取唯一 compose 锁再部署；真实 PG/DSH 全链仍须主控串行安排。本脚本不自动起 compose，也不取锁。

```powershell
# 隔离测试：随机端口HTTP夹具 + 真实Playwright浏览器，不起真实PG/DSH
# 默认使用锁定的完整Chromium；先按上文安装浏览器
node --test e2e/phase1/*.test.mjs
```

测试覆盖参数/退出码/JSON落盘、IP与domain CSRF邀请闭环、TOTP、otp/code、新上下文登录、测试成员私密交接、健康与SKIP、trace脱敏、免费虚拟Key正反例、Loki失败trace关联、桌面环境隔离。真实 compose 上①②仍必须单独跑；本机 Docker/WSL 或共享栈不可用应如实记阻塞，不计为验收通过。八小时连续运行属于主控后续总验收，本入口不代替该观察窗口。
