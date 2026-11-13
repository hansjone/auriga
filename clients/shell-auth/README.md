# Desktop 认证控制页（尚未接入启动器）

本目录目前仅实现冻结契约中的 loopback 页面边界，不是完整 broker 制品。
设备流、系统浏览器启动、OS IPC helper、DPAPI、guardian 和诊断导出尚未实现。
当前安装包不包含本模块，不能用这些测试宣称正式 Desktop 登录验收通过。

`createLoopback({broker, validate, now?})` 仅监听 `127.0.0.1` 随机端口。
`issue()` 返回带 30 秒一次性 fragment 票据的控制页地址，不记录地址或票据；
`close()` 清理连接及全部票据/页面会话。调用者必须在退出时调用它。
`validate(name, value)` 必须使用冻结 JSON Schema 编译的验证器，覆盖
`ShellAuthLoopbackExchange`、`ShellAuthPageCommand`、`ShellAuthEvent`。
测试直接编译 `contracts/` 原始 schema，不自写协议判定。

`broker.epoch` 为本次 broker 的 UUID。
`broker.pageCommand(command, pageOwner)` 由未来 broker 实现，必须负责
epoch/revision 校验、requestId 内容幂等、取消尝试的 pageOwner 绑定、
skip_login 对有效身份的 conflict 保护，以及快照时间/身份有效性。
此模块负责浏览器传输准入，不提供 OS 身份认证，也不把 HTTP 头当 OS 身份。
`pageOwner` 为启动票据摘要，只用于绑定本页发起的尝试，不是平台或 DSH owner。

POST 精确检查 Host、Origin、Fetch Metadata、内容类型、双内存令牌，
Cookie 不参与认证。静态 GET 不含身份；不提供 CORS、任意 URL、登录步骤代理或 logout。
页面刷新后只显示重新从启动器打开的恢复提示。票据和会话只保存在有界内存表中。
后续 broker 操作完成时再次检查页面期限和 epoch，禁止旧页面取得新代次结果。

当前实现阻塞：`ShellAuthEvent` 和 `ShellAuthSnapshot` 均禁止额外属性，
无法向控制页投影设计要求同步显示的设备 `user_code` 及有效期。
请主控在 AUTH-K 冻结契约增加最小公开设备尝试投影（禁止包含 device_code、
access/refresh token 或任意可导航 URL），确定多页面归属和终态清理后继续 C 线。
本模块没有自行增加字段或修改契约。

在工作树根目录验证：

```powershell
node --test clients/shell-auth/test/loopback.test.mjs
node node_modules/eslint/bin/eslint.js --config clients/shell-auth/eslint.config.mjs clients/shell-auth
node node_modules/prettier/bin/prettier.cjs --check clients/shell-auth
node node_modules/typescript/bin/tsc --allowJs --checkJs --noEmit --target es2024 --module nodenext --types node --skipLibCheck clients/shell-auth/loopback.mjs clients/shell-auth/public/page.js
```

没有新增依赖；测试使用本工位已锁定的 Ajv。临时 HTTP 服务全部由测试创建并关闭。
