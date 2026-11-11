# 中转服务审计

成员默认 Key 的首次创建、默认预算变化，管理员 Key 的创建、更新、撤销与预算变化，供应商凭据的创建、轮换、撤销，以及中转池的创建、更新，都在原业务事务内写入 `ai.audit_outbox`。同一条记录的 CloudEvents ID 在投递失败、确认丢失和重试时保持不变；JetStream 的消息 ID 和中心审计 inbox 再次去重。同值池更新不另记事件。

`AURIGA_AUDIT_NATS_URL` 配置审计发布连接。正式 compose 和 NAS renderer 已生成该环境变量，并使用只能发布 `auriga.audit.ai-gateway`、订阅自身回复 inbox 的 `bifrost_audit` 账号。`AURIGA_AUDIT` 流仍由 combined platform 初始化；中转进程不申请流管理权限。未配置该变量时会明确记录暂停投递的启动告警，业务事务仍保存审计 outbox；配置并重启后继续投递。网络不可用时自动重连，失败记录指数退避，最长 60 秒，直到收到 broker 确认，不自动删除未成功记录。

管理请求的操作者、IP 来自 edge 在核实当前会话、精确权限及 step-up 后重建的内部请求头，外部同名头会被覆盖。成员默认 Key 首次创建按已验证的成员账号记录操作者；随后由部署默认策略触发的预算刷新记为 `system/svc:ai-gateway`，不归因于触发调用的成员。IP 为内部服务实际观察到的对端地址，traceparent 沿调用链传播。审计内容仅包含固定动作、目标标识、操作者、结果、IP 和 trace，不序列化请求体、Key 名称、凭据、prompt 或模型消息。

发布端启用专用回复前缀 `_INBOX_bifrost_audit`，NATS ACL 仅允许订阅 `_INBOX_bifrost_audit.>`；不允许订阅通用 `_INBOX.>`、其他服务回复或会话 KV，也不允许请求会话 KV/业务流。升级时应同时更新客户端与两套 NATS 配置，避免旧客户端的默认回复被新 ACL 拒绝。

同值 Key PUT、无变化的默认策略刷新及 POST 幂等重放不新增审计。现有虚拟 Key 的撤销是将 active 设为 false；虚拟 Key 本身尚无轮换接口，本轮的凭据轮换审计接在现有 `/api/providers/{id}/keys` 操作，不新增产品接口。已确认投递的 outbox 行保留，暂不新增保留期或清理机制。

Java 的 `AuditCombinedIntegrationTest.relayBusinessAudit` 会启动临时 PostgreSQL、NATS 与真实审计 API，并调用 Go 的 Key 集成测试；覆盖审计插入失败回滚、租户隔离、请求重放、断网重试、确认丢失、中心查询去重及元数据脱敏。
