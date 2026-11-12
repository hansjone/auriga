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
   `upgrade-admin.sh` 仅增加数据库结构。旧 LiteLLM 供应商及 Key 的业务迁移须另有经过验证的
   公开迁移流程。缺少该流程时暂停服务切换，保留原服务继续运行。
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
```

原收据 SHA 应与备份中独立记录相同。首次 stage 创建追加材料，重跑复用同一秘密。
已经 staged/committed 的安装先查询 status，重复 stage 幂等。原始材料已经封存 Bifrost
的安装继续使用原始材料，无需 stage，也无需最后的 materials commit。

`render.sh` 生成 ops/audit 独立秘密与私有 env、NATS audit 用户、ops-runtime 目录。
一次性 helper 连接正在运行的 postgres，依次执行角色/schema SQL、audit SQL、Bifrost
角色/库/schema。密码从已挂载的文件读取。任一步失败立即退出，修复原因后从该阶段重跑。
确认原 postgres 在同一固定项目的 internal 网络可达，准备过程中持续验证旧服务请求。
旧 postgres 缺少新版 entrypoint 挂载时 helper 仍使用新 bundle 的挂载与 SQL。

## 服务切换与提交

确认中转站数据迁移检查点完成，再执行。

```sh
sh "$NEW/deploy-nas/scripts/start.sh"
sh "$NEW/deploy-nas/scripts/status.sh"
sh "$NEW/deploy-nas/scripts/smoke.sh"
```

`audit-db-prepare`、`panel-prepare` 等一次性容器应成功退出，长期服务按各自 healthcheck
或实际协议检查。确认所有服务的真实镜像 Id，并从正常管理台入口登录。
逐项访问审计、运维告警、备份与升级、出网策略、现场终端、资源市场、我的资源与平台凭据。
在浏览器核对这些页面的实际 API 请求结果，审计使用 `/api/audit/v1`，运维使用 `/api/ops`。
`/security/egress`、`/runtime/devices`、`/resources/*` 为页面路由，页面加载与后端响应分别检查。
出网策略初始应显示未配置并开放，重新验证旧实例模型请求和出网请求。
旧安装缺 presence 三份材料时应报告 `device_presence=disabled`，保留其他功能。
核对原成员、实例元数据、中转站配置与 Key 指纹，完成一次现有实例真实请求。

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
sh "$NEW/deploy-nas/scripts/materials-upgrade.sh" rollback "$RECEIPT"
cp -p "$BACKUP/stack.env.before" "$AURIGA_DATA_ROOT/stack.env"
sh "$OLD/deploy-nas/scripts/render.sh"
sh "$OLD/deploy-nas/scripts/start.sh"
sh "$OLD/deploy-nas/scripts/status.sh"
sh "$OLD/deploy-nas/scripts/smoke.sh"
```

原封存目录与追加材料历史全部保留。新增数据库和角色保留以支持重试。
上述回滚恢复旧 bundle/env 与投影，数据库已执行的业务 Flyway 迁移需要独立兼容性验证。
恢复后从原页面再次验证成员、实例、供应商与模型请求。重新尝试升级时先恢复本次候选
`stack.env`，执行同一个 stage、render、数据库 helper 和验收顺序。
