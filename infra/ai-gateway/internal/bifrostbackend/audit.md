# 中转服务审计

成员默认 Key 的首次创建、默认预算变化，管理员 Key 的创建、更新、撤销与预算变化，以及供应商凭据的轮换、撤销，都在原业务事务内写入 `ai.audit_outbox`。同一条记录的 CloudEvents ID 在投递失败、确认丢失和重试时保持不变；JetStream 的消息 ID 和中心审计 inbox 再次去重。

`AURIGA_AUDIT_NATS_URL` 配置审计发布连接。正式 compose 和 NAS renderer 已生成该环境变量，并使用只能发布 `auriga.audit.ai-gateway`、订阅自身回复 inbox 的 `bifrost_audit` 账号。`AURIGA_AUDIT` 流仍由 combined platform 初始化；中转进程不申请流管理权限。未配置该变量时会明确记录暂停投递的启动告警，业务事务仍保存审计 outbox；配置并重启后继续投递。网络不可用时自动重连，失败记录指数退避，最长 60 秒，直到收到 broker 确认，不自动删除未成功记录。

管理请求的操作者、IP 来自 edge 在核实当前会话、精确权限及 step-up 后重建的内部请求头，外部同名头会被覆盖。成员默认 Key 按已验证的成员账号记录操作者，IP 为内部服务实际观察到的对端地址。traceparent 沿调用链传播。审计内容仅包含固定动作、目标标识、操作者、结果、IP 和 trace，不序列化请求体、Key 名称、凭据、prompt 或模型消息。

同值 Key PUT、无变化的默认策略刷新及 POST 幂等重放不新增审计。现有虚拟 Key 的撤销是将 active 设为 false；虚拟 Key 本身尚无轮换接口，本轮的凭据轮换审计接在现有 `/api/providers/{id}/keys` 操作，不新增产品接口。已确认投递的 outbox 行保留，暂不新增保留期或清理机制。

Java 的 `AuditCombinedIntegrationTest.relayBusinessAudit` 会启动临时 PostgreSQL、NATS 与真实审计 API，并调用 Go 的 Key 集成测试；覆盖审计插入失败回滚、租户隔离、请求重放、断网重试、确认丢失、中心查询去重及元数据脱敏。
