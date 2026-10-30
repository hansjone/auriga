# PR118：Loader 包根入口的真实收集器补证

## 结论与范围

在合并 `origin/main` 的 `50bb5bbebb1a4ee638d8b47cfabdd26d4eab1192` 后，
使用 `deploy/compose/real-dsh/package-lock.json` 安装的原版 DSH
`0.2.0-rc.2`、Node `22.19.0`，隔离 profile 的实际结果为：

- `name: auriga-dsh/plugin`：托管 Node 入口 ACTIVE，浏览器图没有 Auriga，clientPath 未定义。
- `name: auriga-dsh`：托管 Node 入口 ACTIVE，浏览器图包含 `auriga-dsh`，clientPath 指向本工位插件 `src/client.js`，文件可读且非空。

这证明包根 Loader 行能被真实浏览器模块收集器发现。两组均使用同一插件，
只替换 Loader 行的 `name`；生产代码仍只有 `pluginEntry` 的原有最小修正。
当前 main 的插件 manifest 已声明包根 Cordis 导出与 `./client` 导出。

这是宿主模块级证据：不代表浏览器执行成功、功能页可用、平台登录成功、
实例 READY、托管镜像部署成功或云端/桌面双向收发成功。

## 复现（PowerShell，工位根目录）

先按工作区规则安装本工位的锁定 pnpm 依赖，以便实际插件源文件可解析其依赖。
以下路径位于允许的 renderer 范围，依赖和运行日志均被 `node_modules/` 规则忽略。

```powershell
$probeRoot = Join-Path $PWD 'infra/agent/internal/renderer/testdata/node_modules'
$runtimeRoot = Join-Path $probeRoot 'pr118-dsh-runtime'
New-Item -ItemType Directory -Force $runtimeRoot | Out-Null
Copy-Item deploy/compose/real-dsh/package.json,deploy/compose/real-dsh/package-lock.json $runtimeRoot
Push-Location $runtimeRoot
try {
  npm.cmd ci --omit=dev --include=optional --ignore-scripts --bin-links=false --no-audit --no-fund
  if ($LASTEXITCODE -ne 0) { throw 'DSH dependency installation failed' }
} finally { Pop-Location }
$env:AURIGA_DSH_COLLECTOR_RUNTIME = $runtimeRoot
Push-Location infra/agent
try {
  go test -count=1 -v -run '^TestCordisRealDSHBrowserCollector$' ./internal/renderer
  if ($LASTEXITCODE -ne 0) { throw 'Collector probe failed' }
} finally { Pop-Location }
```

测试默认跳过，不下载依赖；显式指定安装目录后才运行。测试检查安装目录的
锁文件与 Compose 锁文件逐字节相同，并确认实际 DSH、app-boot、client-modules
包版本均与 `versions.json` 一致；未修改上游源码或替换收集器。

Go 测试调用真实 `renderer.Render` 生成 YAML，Node 探针从 `insert` 取出插件行。
原版 `loadOverlayPatches`、`boot` 和 `ClientModuleRegistry` 消费此行；
其余服务使用原版 webserver、credentials-local、connection，监听回环随机端口。
不启动完整 web 应用，只验证 Loader 与收集器。

宿主监听覆盖和 web-runtime 行不在本探针中执行；只消费实际渲染的插件行，
保留其 `id`、`inject`、`config.browserOrigin`。旧值对照只改 `name`。
临时 profile 在测试目录中声明插件依赖并建立目录链接，不改用户 DSH。
临时 Ed25519 公钥和实例 token 在测试时生成，使用测试身份；心跳只指向本测试
宿主，没有真实平台会话。结束时释放 Cordis 上下文、监听器、文件监视器和临时目录。

## 范围外发现

探索阶段以 standalone 模式运行相同 rc.2 宿主，两种 Loader 名称均在
`clients/dsh-plugin/src/local/host.js` 的 `installLocalAdmission` 报
`rc.3 native connection admission required`。这是当前本地插件与 rc.2 原生
准入 API 的兼容问题；托管模式的上述对照没有此激活失败。
本 PR 不修改插件、基线版本或宿主 API，也不把本地激活失败推断为云端页面的首因。
功能页、云端登录和互发仍须主控按真实客户端与整栈验收分别确认。
