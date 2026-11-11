# 告警与通知

管理页面：`/ops/alerts`、`/ops/notifications`。API 前缀 `/api/ops/v1/admin`，见 `contracts/openapi/ops.yaml`。

## 启用

独立服务使用现有 `auriga.service.schema=ops`、管理令牌与 common 安全链。合并部署必须显式设置 `auriga.platform.ops.jdbc-url`、`auriga.platform.ops.username`、`auriga.platform.ops.password`；未设置时本线 API 不装配，不借用 IAM 数据源。ops 数据库角色须按平台既有迁移/运行角色规范准备。

将独立生成的 32 字节随机密钥经环境变量 `AURIGA_OPS_NOTIFICATION_KEY`（Base64）注入。缺少密钥时通知写入/发送失败封闭。目标完整 URL（包括路径与查询凭据）和 Bearer 令牌整体 AES-256-GCM 加密，以租户和渠道 ID 绑定；读取只返回元数据，修改时重新填写目标及令牌。保存前确认密钥已可靠备份，直接更换密钥将无法解密已有渠道。

错误文案包 `messages/ops-errors` 需通过 `auriga.errors.message-bundles` 追加到部署的错误文案列表。

权限为 `ops.alerts.read/write`、`ops.notifications.read/write`；通知写入、删除、测试要求近期 step-up。需由权限目录/角色配置授予，不将已有业务权限解释为 ops 权限。API 使用平台管理会话与 CSRF；不新增浏览器直连认证方式。

## 信号与边界

`ops-api` 的 `OpsSignals.observe` 为受信服务接口，不提供 HTTP 伪造告警入口。同租户同来源同对象的未恢复告警唯一；重复观察更新最近时间，恢复关闭，再次发生形成新历史。返回最近 500 条，不是完整历史导出。

独立 ops 服务通过 common 的持久 inbox 消费 `instance.failed/started/deleted`，生成或恢复实例失败告警。合并部署须主控以 ops 专属 inbox/事务管理器装配该处理器，不能绑定 IAM 的主 inbox。节点离线、中转故障、到期、磁盘生产者尚待接入；空列表不代表系统健康。

渠道保存启用状态及最低级别规则；**自动告警投递尚未接入**。当前仅支持管理员显式发送测试。邮件 SMTP 未接入。确认/静默状态持久化，后续自动投递器须据此过滤；不能将已保存规则误认为已产生通知。

测试仅向 HTTPS、443 端口的公网 IPv4 主机发送，不跟随重定向、不使用代理；拒绝任何私网/保留/元数据/IPv6 或混合 DNS 结果。发送连接固定已校验 IP，同时校验 TLS 主机名。地址、上游响应、令牌均不回显；同渠道测试间隔至少 30 秒。IPv6 暂时拒绝。每租户最多返回 500 条历史。

## 验证

`./mvnw -B -pl services/ops/api,services/ops/impl -am verify`

`pnpm --filter @auriga/admin exec vitest run test/ops.test.ts`

`pnpm --filter @auriga/admin lint`、`pnpm --filter @auriga/admin typecheck`

`pnpm exec openapi-typescript contracts/openapi/ops.yaml -o web/admin/src/admin/opsSchema.ts`

新增 ops.yaml 暂未加入根级契约生成/检查脚本（本线无权改脚本）；交接时补入主控接线。
