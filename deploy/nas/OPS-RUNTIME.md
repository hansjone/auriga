# 升级与 PG 备份管理

此能力默认关闭，必须由部署运维显式配置，适用于 NAS/Compose 的单安装管理租户。管理页位于 `/ops/upgrades`、`/ops/backups`；API 复用现有 workspace 上游的 `/api/workspace/v1/admin/ops/**`，不增加管理源、设备凭据或内部公网入口。

1. 在 `stack.env` 设置 `AURIGA_OPS_RUNTIME_ENABLED=true`、安装租户 UUID `AURIGA_OPS_TENANT_ID`，按既有流程 render/start。平台容器只挂载 `$AURIGA_DATA_ROOT/ops-runtime` 状态和归档目录，没有 Docker socket 或 worker 配置。
2. 运维复制 `ops-runtime-worker.example.json` 到数据目录 `config/ops-runtime-worker.json`，替换公开占位路径、同一租户 UUID、Compose argv 和已批准的目标。文件及父目录不得向平台容器开放写权限；文件权限 0600。`targets` 只列经既有签名验证流程批准的公开 bundle 与不可变镜像 digest，不能从网页上传目录或命令。
3. 由主机具备 Docker 权限的运维账户每分钟运行一次 `sh /absolute/deploy-nas/scripts/ops-runtime.sh`。采用主机已有任务计划器；本仓库不自动改写系统 crontab。等价 cron 项为 `* * * * * sh /absolute/deploy-nas/scripts/ops-runtime.sh`。先手动运行一次，再确认页面显示最新上报时间及实际 image ID。

worker 是短命进程，互斥防止重复执行；API 与 worker 用同一 POSIX 文件锁和原子替换保存状态。它只序列化 inspect 的镜像与运行元数据，不保存 Env、挂载或输出日志。Docker 实例额外按 `AURIGA_TENANT_ID` 环境字段筛选，仅返回本安装租户的受管容器。运行元数据来自 `docker inspect`，不是 Compose 期望值。非 Docker 运行时不在该主机镜像清单中。

升级登记先持久化，worker 再从本机只读配置解析目标，并调用既有 `scripts/runtime-upgrade.sh bundle image`；沿用现有签名、stage/commit/rollback 门禁，不在页面拉镜像。回执的 `succeeded` 表示该部署脚本成功提交；仍应检查之后的实际镜像 ID。进程中断、超时或不确定结果记为失败，不自动重放。需要重试时先核现场结果，再重新登记。

备份计划间隔 1–720 小时，默认保留最近 7 份且不超过 30 天（成功归档份数或天数任一达到即删除最旧归档；失败尝试不会挤掉成功归档，失败历史最多保留 100 条），可配 1–365 份、1–3650 天。保底成功份数 `minimumSuccesses` 默认 1，可配 1 至保留份数；自动清理不受时间/份数条件影响，始终留下最近的保底数量，即使计划关闭或连续失败多年。显式、已确认且经过近期验证的手动删除可以删除保底归档。保留天数×24必须不少于备份间隔小时数，API与页面都会拒绝不一致配置；旧无效计划会显示 backup_policy_invalid 并暂停自动清理，等待修正。最低剩余空间默认 1024 MiB，可配 64–1048576 MiB。每次 tick 先执行保留/用户删除，再排定时任务；满 1000 条或清理失败写明确告警，不静默停止。手动删除需确认、`ops.backups.write` 与近期验证；请求后立即撤销下载权限，主机下一 tick 删除仅 UUID 命名的本 worker 归档，其他文件不动。旧版 `.dump` 保留可下载，按相同保留策略清理。

worker 的 `databases` 默认 `auriga`、`bifrost`，另执行 `pg_dumpall --roles-only --no-role-passwords --no-password`。所有组件成功、custom dump 文件头正确后，才原子发布 `backups/<UUID>.tar`（各库 `<库名>.dump`、`roles.sql`），并记录覆盖清单、大小、SHA-256；已存在的任一库/角色导出失败，整份失败且不发布。仅旧实例确认没有 bifrost 库时，将它作为未安装组件跳过，回执的 databases 为实际导出库，skippedDatabases 标注 bifrost，页面覆盖列明确提示。主库和角色定义仍是必需组件；无法查询库是否存在不等于库缺失，超时/权限错误返回失败。不同库依次快照，不保证跨库事务同一时刻；需跨库业务一致性时先停写。通过原有容器本地认证或受控 pgpass，不在 argv/日志放口令。导出前和过程中检查可用空间；tar 打包需要同时容纳当前组件与包，空间不足中止并清理。告警在备份页与失败结果中显示；运维仍应配置宿主磁盘监控。

主机必须有 Linux/POSIX 文件锁、Python 3.10+ 与 Docker Compose，包含 container 安装模式；启用前先核 `python3 --version`，缺失时不启用此功能。权限更新仅对 nofollow 打开的 fd 操作；数据与备份目录通过固定目录 fd 锚定。升级超时先向进程组 TERM，等待 150 秒供原有 rollback trap 完成，再 KILL；中断记录不自动重放。备份用独立 PGAPPNAME；超时/恢复中断时取消同名数据库后端，清理仅该 UUID 的临时输出。外部取消命令不占 API 状态锁；psql/Docker 超时或取消未确认时记录 pg_cancel_failed/cancelPending 与告警，不让 tick 崩溃，后续 tick 重试取消。取消确认前保留该记录并阻止新增备份，避免并发遗留任务。导出超时返回 pg_dump_timeout。

读、执行、下载分别需要 `ops.backups.read/write/download`，升级读写为 `ops.upgrades.read/write`。默认仅 owner，所有写入/下载要近期验证。票据 60 秒、单次、绑定租户/账号/会话/归档，仅存摘要。票据响应以 no-store 回显已校验的浏览器 CSRF nonce；原生 POST 表单在 body 中携带 ticket/csrf，二者不进入 URL 或日志。edge 仅该精确端点允许从有界表单取 CSRF，继续执行原有 Origin、域名 cookie/IP 会话 nonce 校验，未放行任何 x-auriga 身份头。浏览器下载管理器直接流式保存，无整份 Blob/ArrayBuffer，也适用于 HTTP/IP。页面提示提交不代表下载完成；iframe 加载同源错误页、不可访问的错误文档或触发 error 时给出失败提示，忽略初始 about:blank，不读取/展示服务器错误正文。浏览器阻断通知或已开始下载后的传输错误仍以下载管理器与审计为准；离开页面后不再写入旧提示。

票据签发、下载开始、流关闭时的读出字节数/完成或失败，以及删除请求记入状态审计（actor、session、backupId、时间、结果），页面展示最近 50 条。审计保留最近 1000 事件，独立于归档清理；页面不返回审计中的 session。需长期取证的部署应定期只读导出状态至现有审计留存系统。完成表示服务器已读出完整流，不保证客户端已持久化；异常进程退出会保留 started，不能视为 completed。

恢复由运维在维护窗口完成：校验包 SHA-256，列出 tar 内容并仅提取预期库文件与 roles.sql；先在隔离新集群审阅 roles.sql，跳过目标已存在的 bootstrap 角色 CREATE 行，再用 psql ON_ERROR_STOP 导入角色；从原密钥管理恢复口令（归档刻意不含口令）。随后对各新空库分别执行 pg_restore --exit-on-error，核验角色/所有权、迁移、auriga 登录/实例与 bifrost 配置/用量。不要对生产库直接演练，不在页面执行恢复。成员卷、资源包、总部目录、部署密钥须分别备份；整包应离线加密留存。

升级登记历史上限 1000；备份终态历史随保留策略清理，幂等键的去重范围为仍保留的记录，不自动重试未知执行结果。配置与归档目录须一起纳入卷备份。部署后从正常管理页验证登记→回执、计划→备份→原生下载→审计、删除→清理，不能仅以容器 healthy 或本地测试宣称验收。

本地 Linux 验证：`python3 -m unittest discover -s deploy/nas/test -p ops_runtime_test.py -v`。真实归档恢复测试使用单个临时 PostgreSQL 容器、无外部网络/开放端口，使用已缓存镜像执行 `AURIGA_OPS_TEST_POSTGRES_IMAGE=postgres:17 python3 -m unittest discover -s deploy/nas/test -p ops_runtime_pg_test.py -v`；测试完成后自动清理容器和临时目录，不操作共享部署栈。

统一审计接线（2026-11-12 核实）：[PR306](https://github.com/hansjone/auriga/pull/306) 仍 open、未合并；本工位 services/audit/api 仅包说明，本轮保留已有状态审计，不宣称进入中央查询。接线位置是 `OpsBackupService.appendAudit`（ticket_issued/download/delete_requested）与 `recordAudit`（流关闭结果/字节数）。合入后应通过 ops 自己的持久 outbox 发布统一 `audit.recorded` / NATS `auriga.audit.ops`，使用已有 `auriga.common.audit.AuditLogger`、`AuditEntry` 和 `OutboxAuditLogger`。动作可映射为 ops.backup.ticket.issue / ops.backup.download / ops.backup.delete，target.type=backup、id=归档UUID，结果映射 success/failure；保留 actor/tenant 快照、字节数和稳定事件ID，不发送票据、CSRF或归档内容。当前是文件事务，没有 ops 数据库 outbox；不能在文件锁内直接调用要求业务数据库事务的 AuditLogger 来冒充可靠投递。主控接线时补持久队列、去重/确认重试与平台 EventPublisher 装配，再验证本地记录→中央查询；状态记录作为故障兜底保留。
