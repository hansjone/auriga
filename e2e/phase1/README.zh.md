# 邀请到云端 DSH 就绪的浏览器验收

`cloud-ready.mjs` 使用真实 Chromium 和页面控件完成第一阶段第 2、3 条。
不拦截或模拟 API，不注入 cookie/登录态，不调用模型，不生成截图、录屏或浏览器 trace。
当前脚本尚未在真实 Compose 栈跑通；本次工位预检被 `Wsl/Service/E_ACCESSDENIED` 阻塞。
本地断言测试通过不表示这条用户路径已通过。

## 准备全新测试栈

遵守所在工作区的整栈互斥锁；Windows 工位须先按 `_auriga-common.md` 获取
`agent-cache/locks/compose-stack`，写明 owner。已有锁就等待，不操作他人的 project。
先核 Docker context、endpoint 与 daemon 确为本机；不要连接 NAS 或远端。

按 [公开 Compose 入口](../../docs/dev/compose-access.md) 和
[真实 DSH 配方](../../docs/dev/m2.md#真实-dsh-与托管插件w3) 使用本工位源码全新部署：

```bash
cd <auriga-worktree>
export COMPOSE_PROJECT_NAME=auriga-cloud-ready-new-run
export AURIGA_M2_DATA_ROOT=/var/lib/auriga-cloud-ready-new-run
export NODE_IMAGE="$(node -p 'require("./deploy/compose/real-dsh/versions.json").nodeImage')"
export DSH_FIXTURE=real
bash deploy/compose/scripts/prepare.sh
bash deploy/compose/scripts/compose.sh --expected-project "$COMPOSE_PROJECT_NAME" up -d --build --wait
sh deploy/compose/var/iam-healthcheck.sh
```

每轮改用未使用过的 project 和数据目录，并从没有 `deploy/compose/var` 的新工位开始。
不要复用旧安装材料、节点、实例或账号。`prepare.sh` 默认是 **fake**，必须显式设
`DSH_FIXTURE=real`；脚本要求真实镜像元数据存在并符合固定基线。
此配方不用心跳观察器；实际插件按公开配置向平台上报，本脚本从浏览器收到的实例响应核 READY。

首次启动的 agent 正在等待 `deploy/compose/var/join/token`。不要提前运行 M2 e2e 或手动加入节点：
本脚本会经管理台生成加入材料，将本次新令牌写入这个既有文件，等待节点在线，再通过节点详情设置
`template=real-dsh-e2e`。已有节点时失败，防止旧部署的在线状态掩盖新部署缺口。

## 安装与运行

使用 Node 22.19.0，浏览器进程使用可运行 Chromium 沙箱的普通用户。
依赖独立于产品 pnpm workspace，Playwright 及其传递依赖由本目录 `package-lock.json` 锁定。
**先进入本目录再执行 npm**，不要在仓库根用 `npm install --prefix`。

```bash
cd e2e/phase1
npm ci --ignore-scripts --no-audit --no-fund
export PLAYWRIGHT_BROWSERS_PATH="$PWD/output/browsers"
node node_modules/playwright/cli.js install chromium
node --test cloud-ready.test.mjs
node cloud-ready.mjs
```

PowerShell 设置浏览器缓存路径用
`$env:PLAYWRIGHT_BROWSERS_PATH = Join-Path (Get-Location) 'output/browsers'`。
栈准备完成后可在 Windows 运行浏览器，但同一工位生成的配置与 enrollment 必须可读，join 文件必须可写。
口令、TOTP 与新成员凭据不从命令行输入、不输出到日志；成员口令仅存本次脚本内存。
不要在依赖缺失时把未执行算成通过。

默认使用 access.json 的真实浏览器入口，不伪造 Host。公开默认 domain 配方使用保留示例域名，
本机测试可额外设置 `AURIGA_PHASE1_RESOLVE_LOOPBACK=1`（PowerShell：
`$env:AURIGA_PHASE1_RESOLVE_LOOPBACK = '1'`）。这只给本次 Chromium 添加域名到回环的 resolver 规则，
不改系统 DNS/hosts、信任或 TLS 校验；报告明确标为 `domain-browser-loopback-resolver`，
不能据此宣称实际 DNS、NAS 或纯 IP 验收通过。纯 IP 模式不用这个选项，按公开配置设置实际入口。

## 逐步结果和失败处理

退出码 0 且 `output/cloud-ready.json` 的 `status=passed` 才表示本轮浏览器路径通过。
每次另写带时间和随机后缀的独立 JSON，重试不会覆盖首失原件。
脚本依次检查：

1. 管理员通过应急账号、口令及 TOTP 登录管理台；新节点正常加入并设置模板标签。
2. 管理台创建一次性普通成员邀请；读取页面显示的准确门户链接。
3. 新浏览器打开邀请、设置账号口令并完成注册。
4. 丢弃注册浏览器会话，用另一个空 cookie jar 登录，要求账号 ID 与注册成员相同。
5. 点击门户“启动并进入”，从真实 open/轮询响应核 `ownerId`、`ready=true` 与 running/pending_restart。
6. `/open` 正常换票后到达配置中的准确实例入口；真实 DSH 中点击 Auriga，面板可见。
7. 成员浏览器访问需要身份认证的 `/auriga/bootstrap` 成功，mode=managed、协议/插件版本有效，
   实际面板 context 为 managed，界面无口令表单；无 cookie 的浏览器访问同一插件入口必须 401。

第 7 条结合第 4、5 条的账号/实例归属证明成员通过平台会话进入其托管插件；
不把健康检查、面板安装或通信网络 connected 单独当作登录凭据，也不宣称插件 Team 入网/互发已完成。
报告只存步骤、账号/实例 ID、phase、状态码和合法 trace ID，失败不保存包含邀请/票据的 URL、
响应正文、DOM、cookie、Playwright 原始错误或 enrollment。用 `failedStep` 和 HTTP 状态定位，
再查该 project 的服务日志：

```bash
bash deploy/compose/scripts/compose.sh --expected-project "$COMPOSE_PROJECT_NAME" logs --no-color platform edge agent
```

失败保存首失报告后修根因，使用新的全新栈复验。无论成功或失败，结束时只清理自己的 project，
不删除卷或现场材料，按工作区规则释放互斥锁：

```bash
bash deploy/compose/scripts/compose.sh --expected-project "$COMPOSE_PROJECT_NAME" down
```

浏览器由脚本 `finally` 关闭；Compose 生命周期由拿锁的调用者负责。
本任务的 WSL 权限阻塞下未启动任何容器，未取得锁，因此没有执行 down 或释放别人的锁。
