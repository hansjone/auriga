# 升级与 PG 备份管理

此能力默认关闭，必须由部署运维显式配置，适用于 NAS/Compose 的单安装管理租户。管理页位于 `/ops/upgrades`、`/ops/backups`；API 复用现有 workspace 上游的 `/api/workspace/v1/admin/ops/**`，不增加管理源、设备凭据或内部公网入口。

1. 在 `stack.env` 设置 `AURIGA_OPS_RUNTIME_ENABLED=true`、安装租户 UUID `AURIGA_OPS_TENANT_ID`，按既有流程 render/start。平台容器只挂载 `$AURIGA_DATA_ROOT/ops-runtime` 状态和归档目录，没有 Docker socket 或 worker 配置。
2. 运维复制 `ops-runtime-worker.example.json` 到数据目录 `config/ops-runtime-worker.json`，替换公开占位路径、同一租户 UUID、Compose argv 和已批准的目标。文件及父目录不得向平台容器开放写权限；文件权限 0600。`targets` 只列经既有签名验证流程批准的公开 bundle 与不可变镜像 digest，不能从网页上传目录或命令。
3. 由主机具备 Docker 权限的运维账户每分钟运行一次 `sh /absolute/deploy-nas/scripts/ops-runtime.sh`。采用主机已有任务计划器；本仓库不自动改写系统 crontab。等价 cron 项为 `* * * * * sh /absolute/deploy-nas/scripts/ops-runtime.sh`。先手动运行一次，再确认页面显示最新上报时间及实际 image ID。

worker 是短命进程，互斥防止重复执行；API 与 worker 用同一 POSIX 文件锁和原子替换保存状态。它只序列化 inspect 的镜像与运行元数据，不保存 Env、挂载或输出日志。Docker 实例额外按 `AURIGA_TENANT_ID` 环境字段筛选，仅返回本安装租户的受管容器。运行元数据来自 `docker inspect`，不是 Compose 期望值。非 Docker 运行时不在该主机镜像清单中。

升级登记先持久化，worker 再从本机只读配置解析目标，并调用既有 `scripts/runtime-upgrade.sh bundle image`；沿用现有签名、stage/commit/rollback 门禁，不在页面拉镜像。回执的 `succeeded` 表示该部署脚本成功提交；仍应检查之后的实际镜像 ID。进程中断、超时或不确定结果记为失败，不自动重放。需要重试时先核现场结果，再重新登记。

备份计划间隔为 1–720 小时，首次启用从保存时起等待一个间隔，不追赶全部历史时点。手动与定时备份共享单一 worker，不并行执行。执行容器内 `pg_dump --format=custom --no-password`，通过既有容器本地数据库认证；不设置或打印密码，不把密码写入参数。修改过容器本地认证的部署必须自行提供容器内受控 pgpass；不可改为明文 argv。仅成功且有 PGDMP 文件头的归档原子发布为 `backups/<UUID>.dump`，同时保存时间、大小及 SHA-256；stderr 不写入回执。失败的本次临时输出会清理，历史归档不会自动删除。

备份读取、执行、下载分别需要 `ops.backups.read/write/download`；升级读写分别需要 `ops.upgrades.read/write`。目录默认只授予 owner，新登录获取权限后才生效；读权限不会隐含整库下载权。写入和下载均要求近期验证。下载授权只返回内存中的 token，60 秒有效，绑定账号、管理会话与归档，只可使用一次；token 放专用请求头，不进入 URL。服务端仅存摘要。下载失败后应重新申请授权，不能重放旧票据。

归档包括整个数据库，但不包含成员卷、资源包、总部目录、部署密钥或数据库角色创建脚本。备份含敏感业务数据，目录只允许平台服务账户和主机运维读取，另做离线加密留存和隔离恢复演练。恢复始终由运维在维护窗口使用 `pg_restore` 完成；没有网页恢复端点。

状态历史最多保存 1000 个升级登记和 1000 个备份记录，不静默删除旧归档；达到容量后停止接单，运维应先离线归档并受控维护记录。控制目录与备份目录必须一并纳入部署卷备份；丢失队列文件无法还原执行回执。先在正常管理页实测登记、回执、备份与下载，不能仅凭容器 healthy 报已验收。

本地 Linux 验证：`python3 -m unittest discover -s deploy/nas/test -p ops_runtime_test.py -v`。真实归档恢复测试使用单个临时 PostgreSQL 容器、无外部网络/开放端口，使用已缓存镜像执行 `AURIGA_OPS_TEST_POSTGRES_IMAGE=postgres:17 python3 -m unittest discover -s deploy/nas/test -p ops_runtime_pg_test.py -v`；测试完成后自动清理容器和临时目录，不操作共享部署栈。
