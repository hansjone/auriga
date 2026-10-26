# Device HTTP D7 验收探针

- `run-pg.ps1`：给定已有 PostgreSQL `bin` 路径，在本目录 `evidence/<RunTag>` 创建独立新库、随机回环端口；运行新 handler 或整个 ai-gateway 模块，最后关闭 PG，保留数据和日志。
- `sdk-probe.mjs`：由 Go `TestInstalledRC2AdapterRuntimeHTTP` 调用；用显式安装锚点的 rc.2 公开 loader/LlmRuntime 和本工位插件 Adapter，经真实 fetch 调新 HTTP，终点仅合成 fake Transport。
- 完整补验：`./e2e/relay-device/run-pg.ps1 -PgBin <本机已有PG-bin> -RunTag <新名字> -All -DshAnchor <实际安装dsh/package.json>`。
- 脚本不安装或升级包、不读用户 profile、不启动用户 DSH、没有真实模型出站。明确 anchor 不可读会失败；未提供 anchor 的 SDK 测试具名 skip。
- `evidence/` 不提交；首失、PG 数据和日志均保留。先确认真实 PG/SDK 测试结果，再交精确 HEAD 全 CI 和非作者独审。
