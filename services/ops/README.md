# 告警与通知

管理页面：`/ops/alerts`、`/ops/notifications`。API：`/api/ops/v1/admin`，契约见 `contracts/openapi/ops.yaml`。

## 启用与升级

独立服务使用 `auriga.service.schema=ops` 与 common 管理安全链。合并模式显式启用 `AURIGA_OPS_ENABLED=true`，并提供 `AURIGA_OPS_JDBC_URL`、`AURIGA_OPS_DB_USER/PASSWORD`、`AURIGA_OPS_MIGRATION_USER/PASSWORD`。平台独立装配 ops 迁移/运行数据源、事务管理器、inbox 与投递器，不借用 IAM 数据源；运行角色不能拥有表或具有 SUPERUSER/BYPASSRLS。

Compose prepare 自动生成并复用 `var/iam-installation/ops.key`，写入私有 `compose.env`；该新增功能密钥独立于不可变的 identity receipt，不轮换既有 IAM/workspace 密钥。旧 PG 数据卷在停止 platform 后执行 `bash deploy/compose/scripts/compose.sh upgrade-ops`（先保证 postgres 运行），再启动新版 platform。NAS render 自动准备独立 ops 密钥及 owner/app 密码，start 会幂等补齐既有 PG 的角色/schema。普通 worker 不执行真实部署。

`AURIGA_OPS_NOTIFICATION_KEY` 必须为 32 字节随机密钥的 Base64。缺少时页面明确提示并禁用通知写入/测试，自动投递暂停且不消耗尝试次数；告警查询与采集仍可用。密钥非法则启动失败。备份时连同 ops.key（NAS 为 secrets/ops_notification_key）、数据库和其他私有材料一起备份。已配置环境丢失密钥会拒绝自动生成，必须恢复备份；直接换密钥会使已有渠道不可解密。

目标完整 URL（含查询凭据）与 Bearer 整体 AES-256-GCM 加密，并以租户/渠道 ID 绑定；读取与投递详情仅返回元数据，修改渠道需重填秘密。错误文案包已加入 combined 配置。内置 owner/admin 经正式权限目录获得 `ops.alerts.read/write`、`ops.notifications.read/write`，其他角色无默认授权；通知写操作要求近期 step-up。edge 使用管理会话、CSRF 与现有平台受保护上游。

## 信号与投递

`OpsSignals.observe` 是受信服务接口，无 HTTP 伪造告警入口。同租户/来源/对象的未恢复告警唯一；重复更新最近时间，恢复后再次发生形成新历史。最多返回最近 500 条告警、每告警最近 100 条投递结果。

ops 专属持久 inbox 消费 `instance.failed/started/deleted`，由 common 校验生产者与去重。combined 平台每 15 秒经 workspace 自有事务读取心跳快照：60 秒未见（含未曾上报）判节点离线；心跳恢复或节点撤销关闭告警。上游故障目前无可消费事件，证书/密钥到期、磁盘生产者尚未接入，空列表不代表系统健康。

新告警和 warning→critical 升级在同一事务内，按当时启用渠道和最低级别写入持久队列；重复观察不补发，后来新建渠道不追溯旧告警。确认、静默、恢复、渠道删除/停用/提高阈值会取消尚未完成的匹配投递；静默结束不补发，已开始的网络请求可能完成。恢复本身不发送通知。SMTP 未接入。

多副本通过行锁、90 秒租约协调；外发时不持有数据库事务。失败最多尝试 5 次，退避 30/60/120/240 秒，同渠道手工测试和自动投递共用至少 30 秒间隔。崩溃后租约到期可重领，属于至少一次投递，接收端应以 `deliveryId` 去重。详情显示状态、次数、投递/下次时间及脱敏错误，不回显响应/地址/凭据。

Webhook 仅允许 HTTPS 443 公网 IPv4；拒绝私网、保留、元数据、IPv6 或混合 DNS 结果。发送固定已校验 IP，并校验 TLS 主机名，不跟随重定向、不使用代理；沿用既有连接/读取超时。测试发送与自动投递使用同一安全发送器。

## 验证

- `./mvnw -B -pl services/ops/api,services/ops/impl,services/platform-app -am verify`
- `pnpm --filter @auriga/admin exec vitest run --maxWorkers=1`、`lint`、`typecheck`
- `cd infra/edge && go vet ./... && go test -count=1 ./...`
- `cd deploy/compose/bootstrap && go vet ./... && go test -count=1 ./...`
- `cd services/contracts && npm run check-schemas && npm run lint-openapi`
- ops 已纳入 Java HTTP 契约生成与 bundle，`web/shared/scripts/gen-api.mjs` 同步管理台 opsSchema。
