# ctl：本机内置数据面

`infra/ctl` 管理单机开发用的内置 PostgreSQL 17 与 NATS JetStream。
不安装 Windows 服务 / systemd（那是后续交付任务）。仅监听 loopback。

从仓库根进入 `infra/` 后执行（或使用已编译的 `auriga-ctl`）。

## 命令

| 命令 | 作用 |
|---|---|
| `ctl dev` | 前台启动；Ctrl+C 停止 |
| `ctl start` | 后台启动同一套组件 |
| `ctl stop` | 停止本 data-dir 下由 ctl 拉起的后台进程 |
| `ctl status` | 打印是否在跑、端口、数据目录与连接摘要（**不含明文口令**） |
| `ctl --version` | 打印版本 |

`ctl serve` 是 `start` 拉起的内部前台进程，不要直接当日常命令用。

### 共用参数

| 参数 | 环境变量 | 默认 |
|---|---|---|
| `--data-dir` | `AURIGA_DEV_DIR` | 仓库根 `.dev` |
| `--cache-dir` | `AURIGA_PG_CACHE_DIR` | `<data-dir>/cache` |
| `--runtime-dir` | `AURIGA_PG_RUNTIME_DIR` | `<data-dir>/runtime/pg` |
| `--pg-port` | — | `15433` |
| `--nats-port` | — | `14222` |

两个端口必须不同，且只能是 1–65535。PostgreSQL / NATS 都绑定 `127.0.0.1`。

从当前目录向上查找 `contracts/buf.yaml` 确定仓库根；指定 `--data-dir` 后可在仓库外运行编译好的 ctl。

### 退出码

| 命令 | 含义 |
|---|---|
| `status` 退出 `0` | 正在运行 |
| `status` 退出 `3` | 已停止 |
| `start` 在已运行时 | 打印 `already running` 并以 `0` 退出（幂等） |
| `stop` 在已停止时 | 打印 `already stopped` 并以 `0` 退出 |

## 数据目录布局

```
<data-dir>/
  credentials.env       # 开发超级用户 auriga_dev 口令（0600）
  service-roles.json    # 各业务角色口令与连接元数据（0600）
  ctl.state.json         # 后台进程状态（pid、端口；无密钥）
  ctl.log               # start/serve 日志
  ctl.stop              # stop 请求标记（短暂存在）
  dev.lock              # 互斥锁（内含持有者 pid）
  pg/                   # PostgreSQL 数据
  nats/                 # JetStream 存储
  cache/                # PostgreSQL 发行包缓存
  runtime/pg/           # 可重建的解压运行目录（embedded-postgres 会删建）
```

`runtime` 不得与 `pg` / `nats` / `cache` / 整个 data-dir 重叠；ctl 会拒绝不安全路径。

## 凭据与角色

- 库名 `auriga`，开发超级用户 `auriga_dev`（迁移/管理用），口令为随机 256 位十六进制，写入 `credentials.env`：
  `PGPASSWORD=<64 hex chars>`
- 每个业务服务一个 schema 与登录角色 `auriga_<schema>`，清单：
  `iam`、`org`、`workspace`、`resource`、`knowledge`、`collab`、`audit`、`ops`、`ai`
- 角色**不是**超级用户，**没有** `BYPASSRLS`，只拥有本 schema 的 `USAGE`/`CREATE` 及默认对象权限；不授予跨 schema 权限。
- 业务口令保存在 `service-roles.json`（JSON，字段含 `database`/`host`/`port`/`roles.<schema>.{user,password,schema}`）。
- `dev` 启动成功后会在终端打印带口令的 PostgreSQL URL（仅本机开发）。`start` / `status` **不打印明文口令**，只给出文件路径。
- 已有数据目录若缺失或损坏凭据文件，ctl 拒绝静默重置，避免锁死数据目录。

## 安全注意

- 内置组件只听 `127.0.0.1`；不要把 `--pg-port` / `--nats-port` 暴露到非本机网卡（当前实现写死 loopback）。
- `dev` 与当前 `start` 的 NATS **无认证**（本机开发约定）。生产/安装包形态再加每服务 NATS 用户。
- 不要把 `credentials.env`、`service-roles.json` 提交进 git 或贴进日志/工单。
- 强制结束后若留下 `dev.lock`：先确认 PostgreSQL / NATS 已停，再删锁；或对同一 `--data-dir` 执行 `ctl stop`。

## 进程模型

- **`dev`**：当前进程内嵌 PostgreSQL（embedded-postgres）与 NATS；持有 `dev.lock`；Ctrl+C 后停服务并删锁。
- **`start`**：再拉起一个分离的 `ctl serve` 子进程（Linux：`setsid`；Windows：新进程组 + detached），日志写入 `ctl.log`，状态写入 `ctl.state.json`。
- **`stop`**：写入 `ctl.stop`，并在 Unix 上发送 `SIGTERM`；超时后强制结束；清理状态与锁。
- 同一 data-dir 上 `dev` 与 `start` 互斥；重复 `start` 幂等。

## 示例

```powershell
Set-Location infra
go run ./ctl dev
go run ./ctl start --data-dir ../.dev
go run ./ctl status --data-dir ../.dev
go run ./ctl stop --data-dir ../.dev
```
