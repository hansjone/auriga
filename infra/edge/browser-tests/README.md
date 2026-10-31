# LiteLLM 浏览器交接回归

Playwright 固定为 1.56.1，使用它管理的真实 Chromium。依赖和浏览器都安装到本测试目录，独立于项目的 Web 依赖；不改系统浏览器、DNS 或信任。

在工位根目录的 PowerShell 中准备：

```powershell
npm.cmd --prefix infra/edge/browser-tests ci --ignore-scripts --no-audit --no-fund
$env:PLAYWRIGHT_BROWSERS_PATH = Join-Path (Get-Location) 'infra/edge/browser-tests/.browsers'
node infra/edge/browser-tests/node_modules/playwright/cli.js install --no-shell chromium
$env:AURIGA_LITELLM_CHROMIUM_TEST = '1'
Push-Location infra/edge
go test -count=1 -run '^TestLiteLLMChromiumHandoff$' -v .
Pop-Location
```

测试在随机端口启动 edge 和受控 IAM gRPC，管理员会话由夹具建立。Chromium 从管理台 Origin 的最小静态模型链接出发，执行生产 `/ai/ui/` 交接页的自动表单，观察浏览器自己产生的 Origin、303 和 UI 会话 cookie；不手工设置 Origin，不拦截网络请求。

默认上游为测试 UI。设置 `AURIGA_LITELLM_UI_TEST_ORIGIN=http://127.0.0.1:<临时端口>` 可改用本机真实 LiteLLM（需 `SERVER_ROOT_PATH=/ai`），仍使用真实 Chromium；这不是完整 Vue 管理台登录或真实 IAM 整栈验收。IP 独立端口与独立子域两种模式均执行，子域仅通过 Chromium 的进程参数解析到回环。

使用真实上游时，交接后还须看到 LiteLLM 的 Username、Password 和 Login 控件；仅收到 HTML 200 或第一份 JavaScript 不算通过。
