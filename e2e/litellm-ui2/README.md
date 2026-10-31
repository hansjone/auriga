# LiteLLM UI 会话验收

先按 `infra/edge/browser-tests/README.md` 安装精确版本的 Playwright 与 Chromium，不改系统浏览器、DNS 或证书信任。

## 独立本机夹具

在本机 WSL Docker 上运行 `python3 e2e/litellm-ui2/fixture.py start`。脚本只接受本机 Unix socket，以随机 project、随机发布端口启动 PostgreSQL、假模型和固定 digest 的 LiteLLM。秘密现场生成，状态保存在不入库的 `.runtime/fixture.json`，临时密码文件为 `.runtime/password`，不会打印密码。若启动失败也须执行 stop 清理自身资源，不得清理其他 project。

在 Windows 的 edge 测试进程设置：

- `AURIGA_LITELLM_UI_TEST_ORIGIN`：上述 JSON 的 `origin`。
- `AURIGA_LITELLM_UI_TEST_PASSWORD_FILE`：当前工位 `.runtime/password` 的绝对 Windows 路径。
- `AURIGA_LITELLM_CHROMIUM_TEST=1`。
- 可选 `AURIGA_LITELLM_UI_TEST_EVIDENCE`：不入库的截图目录。

然后在 `infra/edge` 执行 `go test -count=1 -timeout=5m -run '^TestLiteLLMChromiumHandoff$' -v .`。测试使用最小管理台链接与 IAM gRPC 夹具，覆盖真实浏览器的自动 POST Origin、原生 LiteLLM 登录、IP/子域页面、公开模型入口拒绝及平台会话撤销。结束后在原 WSL 工位执行 `python3 e2e/litellm-ui2/fixture.py stop`，清理该 project 与临时秘密；截图和脱敏测试日志保留。

## 真实部署

仅对已获授权的部署运行 `node e2e/litellm-ui2/deployed.mjs`，JSON 从 stdin 输入，包含 `admin`、`relay` 两个完整 Origin，`evidence` 截图目录，以及该部署已有应急账号的 `credentials`（`providerId`、`username`、`password`、`totpSecret`）。凭据仅在进程内取用，不写 JSON 文件、不放 argv、不打印；无需 LiteLLM master。脚本建立一个自己的真实平台管理员会话，经实际管理台 LiteLLM 链接自动交接，等待模型/Key/用量页面标题并截图，再通过平台 CSRF 与 logout API 退出该会话，保留 UI cookie 重访应为 401。它不创建模型或 Key，不调用付费模型，不退出其他会话。

脚本等待下一个 TOTP 周期避免重放；不要同时用同一应急账号跑其他登录验收。截图与失败日志保持私有、不入库；不要把真实域名、IP 或凭据写入公开材料。HTTPS 部署须有浏览器正常信任的证书，不设置跳过验证开关。
