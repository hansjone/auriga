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

省略 `browser` 使用 Playwright 安装的完整 Chromium headless 通道（不使用独立 Headless Shell）。管理员使用 `emergency` 身份源；WebAuthn、外部跳转、二维码暂不支持。邀请注册选 `team-account`；新用户名、密码和首个 TOTP 在本次运行内生成。重新登录使用全新上下文，核相同账号 ID，不复用注册会话。

① 默认仅探测公开 IAM 身份源；这不足以证明所有容器 healthy，所以标 SKIP 并列出已通过子检查。部署操作者需在 `healthChecks` 列全本次 compose 服务的实际健康 URL，包括数据库/NATS/节点等可通过部署的运维健康入口或汇总端点观察到的状态；确认清单完整后才设 `healthInventoryComplete: true`。不要用静态首页替代服务健康。支持 `kind: "spring"`（JSON `status=UP`）、`providers`（`items` 数组）及 `http`（HTTP 200 且可解析 JSON）。URL 可相对门户，也可为操作者可访问的完整运维地址。任何检查失败即 FAIL。

LiteLLM 未配置时，①显示已通过的基础检查和 LiteLLM SKIP，整体为 SKIP；配置 `relay.baseUrl` 后调用 `/health/readiness`，必须返回 `status=healthy/ok`，故障即 FAIL。

②③ 共用 `cloud-ready.mjs` 中一套真实浏览器步骤，不再保留另一套邀请/登录/open 实现。② 从管理台控件创建邀请，成员点击公开邀请页、设置口令、从真实页面完成 first_totp 登记，再用空 cookie jar 以口令和 MFA 重新登录，核相同账号与邀请已消费。登录的 OTP 字段按真实 form 的 kind 识别（登记 otp、登录 code），避免复用已消费的 TOTP counter。

③ 原门户按钮发 open，同成员第二个门户页只读观察本人实例 `ownerId`、running/pending_restart、ready=true；按 access 配置核准确实例入口，点击真实 DSH 的 Auriga 侧栏，面板必须可见。随后断言真实 bootstrap 协议、插件版本、managed context、资源界面与无二次口令表单；无 cookie 浏览器访问同一插件入口必须 401。已内置这些实际断言，不再接受 `cloud.loggedInSelector` 代替登录证据。它不证明 Team 入网或云端/桌面互发。

DSH 首启的“预览版说明 / Preview Notice”与可选模型设置会遮挡侧栏。脚本仅通过对应对话框的“继续 / Continue”和“稍后配置 / Configure later”完成公开引导，不写模型密钥，不调用模型；未知对话框保持阻挡。③还要求部署已提供签名面板静态文件、清单及独立的面板验签公钥；缺失时 bootstrap/面板加载记 FAIL，不放宽验签或用普通连接标签替代。

配置须提供公开部署生成的 `access.json`（以 `access` 对象提供）和 `admin`；也可仅用 `composeDir` 指向同一部署的 compose 目录，自动读取 `var/access.json`、真实镜像元数据和应急 enrollment。Windows 可读 WSL UNC 路径，秘密不进入报告。

`shared` 默认为 true：复用在线、可调度、Docker、template=real-dsh-e2e 的节点，不改节点。全新部署设 false：脚本要求零节点，经管理台签加入令牌写同一 composeDir 的既有 var/join/token，等待上线再设模板标签。只有不验节点的独立部署可设 `nodeEnrollment:false`，其结果不能冒节点加入通过。

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

本分支基线尚无 `docs/design/relay-litellm.zh.md`，因此采用 LiteLLM 官方接口：`/health/readiness`、`/model/info`、`/v1/chat/completions`、`/key/generate`、`/key/info`、`/key/delete`、`/spend/logs`。`relay.baseUrl` 为这些入口的共同根（例如 `https://relay.example.com/`，不要再附 `/v1`）。后续正式 edge 路由设计不同需由主控接线。

```json
{
  "relay": {
    "baseUrl": "http://127.0.0.1:4000/",
    "adminKey": "LITELLM_ADMIN_KEY_FROM_PRIVATE_FILE",
    "allowModelCalls": false,
    "models": [
      {
        "name": "phase1-fake-a",
        "provider": "fake-a",
        "apiBase": "http://127.0.0.1:4101/v1",
        "testOnly": true
      },
      {
        "name": "phase1-fake-b",
        "provider": "fake-b",
        "apiBase": "http://127.0.0.1:4102/v1",
        "testOnly": true
      }
    ]
  }
}
```

默认禁止模型请求。仅在这两个部署已接**免费本地假上游**后，把 `allowModelCalls` 设 true；`testOnly` 是操作者对免费上游的明确声明，脚本不能证明任意外部服务不会收费，不要指向付费提供商。脚本不会代管理员改 LiteLLM 模型池。

⑥ 先核配置至少两个独立假提供商及 apiBase，且 `/model/info` 的模型与实际 apiBase 一致；再经两个 DSH UI 对每个模型各调用一次。`modelUi.cloud`、`modelUi.desktop` 各配 `actions`、`responseSelector`、`requestIdAttribute`；动作可用 `valueFrom: "model"/"marker"`。假上游须回显唯一 marker，回复 UI 还需暴露可关联的 LiteLLM request ID 属性。脚本按该 ID 查询官方 `/spend/logs?request_id=...&summarize=false`，核本次非零 token 用量，最多等 10 秒。无 UI 能力或请求 ID 接线时 SKIP/FAIL，不用直接代理请求代替 DSH 调用。

⑦ 需要 LiteLLM 数据库及 adminKey。签发 10 分钟有效的临时 Key：标准 OpenAI JSON 调用成功，第二次请求必须明确命中 RPM 限流；另一个预算为 0 的 Key 必须返回明确预算错误；查 Key spend 和本次请求 token 用量；吊销后相同 Key 必须 401/403。限流/预算的错误类型或消息必须符合对应语义，普通鉴权失败不能冒预算生效。所有新 Key 在成功/失败后均尝试删除；清理失败即 FAIL，请管理员检查别名为 `phase1-*` 的短期 Key。不调用付费模型。

参考：[LiteLLM 虚拟 Key](https://docs.litellm.ai/docs/proxy/virtual_keys)、[预算与限流](https://docs.litellm.ai/docs/proxy/users)、[用量记录](https://docs.litellm.ai/docs/proxy/cost_tracking)。实际固定镜像对零预算、RPM、日志异步落库的行为仍须部署验收；不能用本分支 HTTP 夹具替代。

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
