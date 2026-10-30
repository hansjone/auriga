# 第一阶段统一浏览器验收

统一入口为 [run.mjs](./run.mjs)，完整中文说明见 [README.md](./README.md)。
②③复用 cloud-ready.mjs 的真实邀请、MFA、空会话登录、本人云端 READY 与托管插件步骤；该文件仅作为模块，不再单独执行。
Playwright 依赖仅声明并锁定在 e2e/package.json 与 e2e/package-lock.json。

全新部署按 [公开 Compose 文档](../../docs/dev/compose-access.md) 和 [真实 DSH 配方](../../docs/dev/m2.md#真实-dsh-与托管插件w3)，显式设置 DSH_FIXTURE=real。
WSL 使用原生 Linux 私密目录；从 Windows 导出源码时使用 git -c tar.umask=0022 archive 保留执行位与合格目录权限。
部署自己的代码前按工作区共享栈规则领取 compose-stack 锁，用完更新 shared-stack.txt 并释放自己的锁。
接口复验直接复用共享栈，保持其他成员、实例与数据；不碰 NAS、系统 DNS/信任或付费模型。

NAS 现场验收须另有任务明确授权，并按 [NAS 公开流程](../../deploy/nas/README.md#可选集中日志)
开启日志 profile。第⑧项可用 `observability.queryCommand` 通过已有受信任操作通道查询内部 Loki，
无需发布 Loki 端口。该字段必须是命令和参数数组，执行时不开 shell；stdin 接收查询 URL，
stdout 只接受不超过 1 MiB 的原始 JSON，命令失败、超时或返回无效 JSON 均使验收失败。
命令默认 15 秒超时；`queryTimeoutMs` 可显式设置为 1–60000 的整数。
查询时间窗默认前后各容忍 5 秒部署时钟偏差，`clockSkewMs` 可设为 0–60000；
这只影响日志检索，认证时间边界不变，仍精确匹配当前失败 trace。
没有前序失败时，入口向一个随机、不存在的登录事务提交空 advance，取得不会更改账号的
控制器验证失败，避免仅在安全链返回 401 时遗漏应用日志过滤器。
缺省仍直接 HTTP 查询 `lokiUrl`；两种方式执行完全相同的 trace 和跨服务覆盖检查。

真实模型验收可使用 `modelNative` 代替 `modelUi`，通过两端已登录的真实 DSH 页面调用
公开原生 Settings/Credentials/Session RPC，逐模型及池核验新 assistant 回复、成员 Key 绑定和用量。
须提供 `pool`，以及 `cloud`、`desktop` 的 `baseUrl`、`cwd`、`contextWindow`、`maxTokens`。
桌面 URL 必须是 `relay.publicBaseUrl`；云端也应使用正式部署可达的模型入口，内网别名不能凭
自定义 Host 已设置就声称可用。`supportsStore:false` 按中转站能力写入 DSH 兼容配置。

只有独立测试部署经明确授权时才设 `modelNative.allowPoolDisruption:true`：正常双端调用后，
仅对指定池中与两个已配置上游精确对应的一个在线部署调用 LiteLLM `model/block`，
确认该部署不再可用，再让两端实际会话走池，且用量必须关联到存活部署的 `model_id`。
无论中途失败与否都调用 `model/unblock` 恢复；已离线、额外或不匹配部署一律不执行演练。
变更接口仅丢弃响应正文，不读取或回写供应商 Key。每次会话的临时 Key、凭据和提供方均清理，
任一回复、归属、用量、切换或恢复证据缺失都不能使第⑥项通过。
