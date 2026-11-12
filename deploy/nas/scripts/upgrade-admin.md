# 已有 NAS 管理台升级操作单

适用于 LiteLLM 材料封存的旧 NAS bundle 升级到包含 Bifrost、ops、audit 的版本。
操作者在 NAS 本地执行，保留旧发行目录和旧镜像。先在独立测试安装完成相同操作单。
所有路径均须换成实际规范绝对路径。每套安装串行执行。

## 准备与检查点

1. 核对目标发行包的精确源码提交、`SHA256SUMS`、`images/manifest.json`。
   `sh "$NEW/deploy-nas/scripts/load-images.sh" "$NEW"` 导入并核目标 Engine Id。
   保存输出，更新 `stack.env` 时使用这些 Id。原 postgres、NATS、registry 镜像保持其现场批准值。
2. 记录当前服务镜像、Compose profile、实例 generation、成员数量、中转站供应商及 Key 的
   不可逆指纹、实例元数据。通过正常登录入口验证一次模型请求并记录结果。
   检查当前中转站是否已经使用 Bifrost。`materials-upgrade` 仅增加 Bifrost 秘密与配置，
   `upgrade-admin.sh` 无参数时增加数据库结构；其 `migrate-relay` 子命令负责只读导出旧
   LiteLLM 并通过 Bifrost 管理 API 导入。保留原 `AURIGA_IMAGE_LITELLM`，helper 使用
   原核验镜像的 Prisma/decryptor，不把明文配置写入文件。切换窗口先停止旧调用写入，
   直到迁移、验证完成；源库保持原样，可直接恢复旧服务。
3. 保存数据根、`stack.env`、访问输入和 runtime 审批输入的权限与完整备份。
   数据库使用 `pg_dumpall` 逻辑备份，文件快照应在停止写入后取得，包含实例目录、NATS
   数据、enrollment、secrets、全部材料与追加收据。用已有备份工具检查可恢复性。
   单独复制 `stack.env` 为本次备份目录的 `stack.env.before`，mode 0600。
   数据库迁移开始后回退服务须核对 Flyway 兼容性，需要时按批准的备份恢复流程处理。
4. 保留访问输入、CA、identity、runtime 参数、collab 根与收据原样。保持原安装模式。
   原有 `AURIGA_PLATFORM_SEED=false`。补齐目标版本需要的
   `AURIGA_IMAGE_BIFROST`、`AURIGA_IMAGE_OPERATOR` 和 platform/edge/agent 镜像 Id。
   `AURIGA_OPS_RUNTIME_ENABLED=false` 为默认值，worker 启用另按 `OPS-RUNTIME.md`。
   新 `AURIGA_EGRESS_TENANT_ID` 应与安装 tenantId 一致，DNS endpoint 仅在现场需要时配置。

## 材料 stage 与数据库准备

先设置并检查以下变量，`BACKUP` 应为已经完成的本次备份目录。

```sh
export AURIGA_DATA_ROOT=/srv/container/auriga-test
OLD=/srv/releases/old
NEW=/srv/releases/new
BACKUP=/srv/backups/auriga-before-admin
RECEIPT=$(sha256sum "$AURIGA_DATA_ROOT/config/materials/iam-installation/bootstrap.receipt" | awk '{print $1}')
test -s "$BACKUP/stack.env.before"
sh "$NEW/deploy-nas/scripts/materials-upgrade.sh" status
sh "$NEW/deploy-nas/scripts/materials-upgrade.sh" stage "$RECEIPT"
sh "$NEW/deploy-nas/scripts/render.sh"
sh "$NEW/deploy-nas/scripts/upgrade-admin.sh"
NAS_ROOT="$NEW/deploy-nas"
export NAS_ROOT
. "$NAS_ROOT/scripts/lib.sh"
load_stack_env
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" kill --signal HUP nats
```

原收据 SHA 应与备份中独立记录相同。首次 stage 创建追加材料，重跑复用同一秘密。
已经 staged/committed 的安装先查询 status，重复 stage 幂等。原始材料已经封存 Bifrost
的安装继续使用原始材料，无需 stage，也无需最后的 materials commit。

`render.sh` 生成 ops/audit 独立秘密与私有 env、NATS audit 用户、ops-runtime 目录。
资源目录同时启用独立 resource owner/app、schema 与游标签名秘密，旧秘密保留。
资源元数据读接口使用这套数据库接线。资源对象存储、发布/撤销签名及分发审批材料
仍须沿资源服务的公开部署配置提供，涉及这些操作时另核其可用性。
一次性 helper 连接正在运行的 postgres，依次执行角色/schema SQL、audit SQL、Bifrost
角色/库/schema。密码从已挂载的文件读取。任一步失败立即退出，修复原因后从该阶段重跑。
确认原 postgres 在同一固定项目的 internal 网络可达，准备过程中持续验证旧服务请求。
旧 postgres 缺少新版 entrypoint 挂载时 helper 仍使用新 bundle 的挂载与 SQL。
NATS 收到 HUP 后重载生成的用户与权限，保留既有连接。
Compose 的普通 up 无法通过绑定文件内容变化识别这次配置更新，须完成此重载再切换服务。

## 服务切换与提交

确认备份和旧流量冻结后执行。迁移失败立即停止后续步骤，不得 commit。

```sh
sh "$NEW/deploy-nas/scripts/start.sh"
sh "$NEW/deploy-nas/scripts/upgrade-admin.sh" migrate-relay
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-deps --no-build --pull never --force-recreate --wait edge
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" restart --no-deps agent
sh "$NEW/deploy-nas/scripts/status.sh"
sh "$NEW/deploy-nas/scripts/smoke.sh"
```

`audit-db-prepare`、`panel-prepare` 等一次性容器应成功退出，长期服务按各自 healthcheck
或实际协议检查。确认所有服务的真实镜像 Id，并从正常管理台入口登录。
edge 重建使文件 bind 读取本次原子发布的派生配置，随后 agent 恢复实例桥接端点。
逐项访问审计、运维告警、备份与升级、出网策略、现场终端、资源市场、我的资源与平台凭据。
在浏览器核对这些页面的实际 API 请求结果，审计使用 `/api/audit/v1`，运维使用 `/api/ops`。
`/security/egress`、`/runtime/devices`、`/resources/*` 为页面路由，页面加载与后端响应分别检查。
出网策略初始应显示未配置并开放，重新验证旧实例模型请求和出网请求。
旧安装缺 presence 三份材料时应报告 `device_presence=disabled`，保留其他功能。
核对原成员、实例元数据、中转站配置与 Key 指纹，完成一次现有实例真实请求。

### 中转站迁移边界

`migrate-relay` 先在只读事务中读取旧模型与调用 Key，完成全量预检后写入目标。
供应商、凭据、模型池及外部 Key 使用稳定幂等键；同一静止源可重复执行。
同名目标模型池采用 `If-None-Match: *`，避免覆盖人工已有配置。源模型或配额在首次
写入后变化会发生幂等冲突，应暂停并保留证据，不得通过更换幂等键重复创建。
helper 只输出数量和固定错误信息，第三方库及子进程 stdout/stderr 均被抑制。

支持 OpenAI、Anthropic、Gemini、DeepSeek、MiniMax 的直接模型，api_base 为 origin 或
`/v1`；保留旧公开模型名、供应商密钥和模型允许列表。高级模型参数、TPM/模型预算等
无法对应的策略失败封闭，需先按目标能力明确转换，不会默默丢弃。旧 OpenAI 模型映射为
`openai-compatible`，保持 Chat Completions 上游协议，不启用目标 native OpenAI 的 Responses 优先行为。
旧数据库仅存调用 Key 的 SHA256；公开管理端 `PUT /api/legacy-key-bindings` 接受
`{keyId, sha256, expiresAt?}`，要求安装管理员 Basic auth 与安装租户头，仅可绑定 external Key。
同一摘要禁止改绑，保留旧过期时间；撤销、预算、RPM 与租户检查沿用目标 Key 的正常路径。
调用方继续使用原 Key，新增的目标原生 Key 由管理界面管理，不输出到迁移证据。
目标调用入口为 `/v1/chat/completions` 和 `/v1/models`；旧 `llm_api_routes`、
`openai_routes` 或显式包含这两个路径的范围可转换，其余路由范围失败封闭。
旧中转站的其他调用协议与路由不自动启用。

目标预算按旧 `max_budget-spend` 转成微美元后向下取整，从切换起作为每月额度，旧用量历史
留在源库；无上限旧 Key 转为最大精确整数额度 `9007199254740991` 微美元。
未标价/零价模型使用目标最小正价格 1 微美元/百万 token，避免绕过预算。
这些转换会在计数输出 `unlimitedKeysCapped`、`minimumPriceModels` 标明，执行前须核对预算语义。
分组/组织继承策略、非月度预算重置、动态路由和高级多媒体参数不属于自动等价迁移范围，
须在切换前核对；不能仅凭数量相同宣称这些能力已迁移。

上述检查全部通过，才提交追加材料。

```sh
sh "$NEW/deploy-nas/scripts/materials-upgrade.sh" commit "$RECEIPT"
sh "$NEW/deploy-nas/scripts/materials-upgrade.sh" status
```

保留旧 bundle、镜像、备份及完整操作证据。提交后材料状态机拒绝 rollback。

## commit 前回滚

先保存失败日志，停止新 bundle 的项目服务，再撤回 staged 激活，恢复旧配置。

```sh
sh "$NEW/deploy-nas/scripts/stop.sh"
sh "$OLD/deploy-nas/scripts/stop.sh"
sh "$NEW/deploy-nas/scripts/materials-upgrade.sh" rollback "$RECEIPT"
cp -p "$BACKUP/stack.env.before" "$AURIGA_DATA_ROOT/stack.env"
sh "$OLD/deploy-nas/scripts/render.sh"
sh "$OLD/deploy-nas/scripts/start.sh"
sh "$OLD/deploy-nas/scripts/status.sh"
sh "$OLD/deploy-nas/scripts/smoke.sh"
```

原封存目录与追加材料历史全部保留。新增数据库和角色保留以支持重试。
两份 bundle 的固定服务定义分别清理各自容器，覆盖旧 LiteLLM 容器引用已移除网络的情况。
上述回滚恢复旧 bundle/env 与投影，数据库已执行的业务 Flyway 迁移需要独立兼容性验证。
恢复后从原页面再次验证成员、实例、供应商与模型请求。重新尝试升级时先恢复本次候选
`stack.env`，执行同一个 stage、render、数据库 helper 和验收顺序。
