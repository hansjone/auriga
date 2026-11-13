# Desktop 认证 broker

本目录由 C 维护。`build.mjs` 生成同一个自包含制品，含 Windows helper、冻结 schema、锁定 Ajv 闭包和逐文件 SHA-256 清单。Desktop 放在 `auriga/shell-auth/`，独立插件由 D 负责引用及打包。

`startDesktopAuth({root,dataHome})` 读取安装级 `desktop-origin.json`，启动本用户 broker、随机端口控制页及 OS IPC。平台未配置或 helper 不可用时保留本地 DSH。设备登录只打开 HTTPS 门户 `/device`，不创建云端实例。用户码展示仍等 AUTH-K2，正式登录链尚不能验收。

Windows helper 使用当前 SID 的命名管道 DACL、禁远程标志、真实客户端 SID、受控子进程及其启动时刻和随机能力准入。能力经 DSH 的继承 fd3 交给插件后端，不写 argv、env、配置或页面。D 从 fd3 读取首行描述符后调用 `connectIpc(descriptor)`，可用 `call`、`subscribe`、`enterprise`、`streamCommand` 和 `subscribeStream`。IPC 断开或订阅返回 null 时必须立即停用平台能力。真实跨用户账号拒绝尚未验收；Unix 缺少 peer verifier 时明确不可用。

`Broker` 负责 epoch/revision、请求 ID 幂等、设备轮询、取消归属、晚结果撤销、立即本地退出、DPAPI 撤销队列和刷新单飞。页面只开放冻结的 snapshot/open_login/cancel_login/skip_login，不能读取凭据。控制页用 30 秒一次性票据和 15 分钟内存双令牌保护；刷新需重新从启动器打开。

固定 JSON 代理直接读取 AUTH-K 操作清单，校验请求及响应，按 audience 内部取票、按操作白名单转发头，重核身份和期限后才交结果，不自动重放写请求。SSE、流额度控制和上传引用已实现。上传存于本用户私有目录，每块 fsync 后应答，结束核摘要，消费后删除。

**网络 NATS、Team 可信执行上下文的跨进程接线尚未完成；不能声称全部平台功能可用。** Team 的内部登记/解析、MCP 请求与流投影已有替身测试，但冻结契约没有 D→C 上下文登记/撤销消息，未擅自开放新 IPC 路径。需要主控补契约后接线；携带未知上下文的操作拒绝。

随 Desktop 安装器导出诊断：

```text
auriga-desktop.cmd diagnostics export --output <目录> --tenant <UUID> --provider <UUID>
```

使用随包 Node，默认两小时、最大 24 小时。仅读取固定本地 broker/插件日志，使用与落盘相同的正向脱敏投影；不含 journal、vault、资源或完整环境。ZIP 清单含摘要和缺源；服务端观测源未接时 partial=true、退出码 3。重解析路径拒绝，临时目录用户独享，原子发布且不覆盖。

工作树根目录验证：

```powershell
node --test clients/shell-auth/test/*.test.mjs
node node_modules/eslint/bin/eslint.js --config clients/shell-auth/eslint.config.mjs clients/shell-auth
node node_modules/prettier/bin/prettier.cjs --check clients/shell-auth
$f=Get-ChildItem clients/shell-auth -Filter '*.mjs' | Select-Object -ExpandProperty FullName
node node_modules/typescript/bin/tsc --allowJs --checkJs --noEmit --target es2024 --module nodenext --types node --skipLibCheck @f
```

测试用运行时生成密钥和合成身份，无新增或升级依赖。实际 Windows helper 已测 DPAPI、ACL、受控子进程准入及未登记进程/错误能力拒绝。替身测试不能代替正式安装包和门户的正常入口验收。
