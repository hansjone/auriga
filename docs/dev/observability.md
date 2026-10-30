# 请求日志与 trace 定位

Auriga 使用现有 W3C `traceparent`，日志字段为 `trace_id`、`service`、`level`。
`X-Trace-Id` 和 `traceparent` 随 HTTP 响应返回。`X-Request-Id` 仍是原有业务请求 ID；
模型网关的 ULID、幂等键、消息 ID 均不被 trace 替代。trace 只帮助查日志，不能授权。
启动及无请求背景事件的 `trace_id` 为空，不虚构与之前请求的因果关系。

## 已接线的边界

- 插件登录/平台、设备控制、AI、托管心跳/面板出站 HTTP 各起一个 trace。
  `network/submit` 的真实路径是 plugin → edge → ai-gateway → IAM/collab gRPC。
  IAM/workspace/collab HTTP 经 edge 后由 common 继续 trace。
- WSS 每次握手、重连有自己的 trace；NATS 为保留既有 64 KiB broker 预算，仍发送原 PUB，
  不增加占用 payload 配额的 header。插件与 gateway 用已存在的逻辑 `request_id`
  做域分离 SHA-256（前缀 `auriga-nats-request-v1:`、取前 128 bit）得到同一 W3C trace ID，
  span 随机。gateway → collab gRPC 用标准 traceparent 继续。签名 JSON 帧和业务 ID 不变。
  edge 透明代理 NATS 字节，**握手 trace 不能冒充其后每条消息的 trace**。
  后台 head/协调工作另起 trace，不继承某个历史请求。
- 外来 trace 限为 55 字节格式，零 ID、无效格式、重复 HTTP 头会重建；不记录原值。
  插件每次 HTTP 出站生成新 trace；NATS 同一逻辑 request_id 的重发保留 trace、span 各异，
  不同请求独立。没有有效 request_id 的非法帧或背景事件各自生成新 trace。
  gRPC 的请求 ID/tenant/服务身份仍由原鉴权拦截器独立处理。
  edge 请求日志的 `external_trace` 仅表示采用了有效入站 trace，不能证明来源可信；
  对第三方模型服务不发送内部 traceparent，内部 RPC 的 trace 接续保持。

HTTP 日志只记路由模板；未匹配/被鉴权提前拒绝的请求只记固定服务前缀，不记原始
动态路径段（邀请码、设备授权码等）。edge 的健康探针不记录逐请求 INFO，完成日志
带耗时；不包装 ResponseWriter，保持 streaming/upgrade 的原生行为。
gRPC 常规业务拒绝只保留 method/code 的一行 WARN，全进程每秒至多一条；重复拒绝
和完整拒绝栈仅 DEBUG 可见。5xx/意外异常仍保留 ERROR 栈。

只在测试环境设置 `AURIGA_LOG_LEVEL=DEBUG`；默认 INFO。Java 对 `auriga` 包生效，Go
和插件使用同一变量。异常保留栈与代码位置，脱敏 token/JWT、Authorization、Cookie、
密码/TOTP、私钥和凭据环境赋值。业务账户/tenant/node/message ID、`input_tokens`、
`max_tokens`、`token_id` 不因旧白名单被丢弃。调用点只提供选定上下文，不输出请求体、
整组 headers 或环境全集。不要把凭据作为未标名的普通业务文本写日志。

## 本机插件日志

固定目录：Windows `%LOCALAPPDATA%\Auriga\logs`（无变量时用用户 AppData/Local），
Linux `${XDG_STATE_HOME:-$HOME/.local/state}/Auriga/logs`，macOS `~/Library/Logs/Auriga/logs`。
不读取 DSH 私人 journal。

文件为 `auriga-dsh-0.jsonl` 到 `auriga-dsh-7.jsonl`，各有一个 `.1` 轮转文件，
每文件最多 2 MiB（总计最多 32 MiB）。每进程独占一个槽，多个插件实例共享当前进程
sink；锁标明 PID，活进程的槽不抢占，异常退出后的死 PID 槽可回收。正常卸载会排空
文件队列并关闭句柄/释放锁；关闭后的写入不复活 sink。队列最多 64 条，每条最多
64 KiB，文件错误、槽耗尽或压力时丢记录，不阻塞业务。stderr 复用既有有界独立 writer，
最多 32 条，但只发送原有网络诊断的枚举/整数/布尔白名单事件，单条最多 2048 字节；
INFO、任意业务字段和完整异常栈仅写本机文件，不写 stderr。两者的成功均不是投递保证。
脱敏前限制单条文本为 64 KiB，超限整条省略，避免截断后泄漏凭据。日志时间统一 UTC。

## 可选集中日志

按现有 [M2 部署流程](m2.md)先部署业务栈。`observability` profile 默认关闭。
固定版本为 Loki **3.5.5**、Grafana **12.1.1**、Alloy **1.10.2**；无需升级业务依赖。

1. 在 `deploy/compose/var/observability/grafana-admin-password` 生成本机随机密码文件，
   不把值放进命令行/仓库。可用以下 Node 脚本（从仓库根执行，无输出）：

   ```js
   // 保存为本机临时 .mjs 后运行，或在 Node REPL 执行。
   const { mkdir, writeFile } = await import('node:fs/promises');
   const { randomBytes } = await import('node:crypto');
   await mkdir('deploy/compose/var/observability', {
     recursive: true,
     mode: 0o700,
   });
   await writeFile(
     'deploy/compose/var/observability/grafana-admin-password',
     randomBytes(32).toString('base64url'),
     { flag: 'wx', mode: 0o400 },
   );
   ```

   Linux/NAS 将该目录设为 `0755`、密码文件属主设为容器 Grafana UID/GID `472:472`、
   文件权限 `0400`，确保容器可读；文件不可公开分享。Windows 用当前用户私有目录/ACL。
   登录用户名是公开占位值 `auriga-admin`，密码由部署者从本机文件使用，不回显给模型。
   不提供默认密码、匿名访问或注册入口。密码文件缺失时 Compose 拒绝创建宿主路径；
   文件为空、不是普通文件或不可读时，Grafana 启动前检查退出，不回退默认密码。

2. 在同一 Docker 主机创建 `deploy/compose/var/plugin-logs`。本机部署可设置
   `AURIGA_PLUGIN_LOG_DIR` 为上面的插件日志绝对目录。NAS 部署时该目录是本机证据
   导入点：将收集所得 `auriga-dsh-*.jsonl*` 脱敏文件复制进去。远端桌面文件不会自动
   出现在 NAS；未导入不能声称 Grafana 已含 plugin 段。无需暴露 Loki 上传 API。

3. 使用与业务部署一致的 Compose project 名；同时设置 `COMPOSE_PROJECT_NAME`
   （默认 `auriga-m2`，不要只用 `-p` 改名，以免 Alloy 发现另一个项目）。从仓库根执行：

   ```sh
   docker compose -f deploy/compose/docker-compose.yml --profile observability up -d loki grafana alloy
   docker compose -f deploy/compose/docker-compose.yml --profile observability ps loki grafana alloy
   ```

   Windows 可用同一 Docker CLI 命令。Linux/NAS 同样使用本地 Unix Docker socket；
   Windows Docker Desktop 使用 Linux 容器 socket，**不用开放 2375 明文 daemon**。
   Alloy 只发现这个 project 的全部 compose 容器 stdout/stderr，含 host-network 容器；
   标准 Docker API 为只读采集。socket 的只读挂载不等于 Docker API 权限隔离，
   应只在受控测试主机启用此 profile。它不修改业务容器或数据。

4. 本机打开 `http://127.0.0.1:13000`，进入预置 **Auriga trace logs** 面板，
   选择时间窗，粘贴 32 位 `trace_id`，服务可多选。Grafana 仅绑定主机回环；NAS 使用
   已有 SSH 通道 `ssh -L 13000:127.0.0.1:13000 <user>@<nas-host>` 访问，不改路由/DNS/信任。
   Loki/Alloy 不发布主机端口，只连隔离的 observability 网络。
   Grafana 另接仅供它使用的普通 bridge `grafana-publish`，为 Docker 端口发布提供网关；
   该 bridge 也允许 Grafana 默认出站，不接业务控制网。`observability` 仍为
   `internal: true`，Loki/Alloy 不接发布网。仅连 internal bridge 不能满足这里的宿主
   端口发布要求，依据见 [Docker internal 网络说明](https://docs.docker.com/compose/how-tos/networking/#internal-networks)。
   启动后应核 `docker compose -f deploy/compose/docker-compose.yml --profile observability port grafana 3000`
   和实际 `http://127.0.0.1:13000/api/health` 返回200且 `database=ok`；登录前还须确认
   `/api/user` 未认证请求返回401。配置中的 `ports`、HostConfig声明或容器healthy均
   不能替代上述公开入口检查；容器IP请求只作诊断对比。
   服务查询也可在 Explore 用 `{service="edge"}`；原始 JSON 的 `service` 与 Compose
   标签重名时显示为 `service_extracted`，例如容器 `network` 的业务服务为 ai-gateway。
   `trace_id` 在 JSON 中解析，**不作为高基数 Loki 索引标签**。

5. 停止采集：`docker compose -f deploy/compose/docker-compose.yml --profile observability stop alloy grafana loki`。
   这保留三个独立 named volume（`alloydata` offsets、`lokidata` 日志、`grafanadata` 账号）。
   不用全栈 `down -v` 删除业务数据。清空日志需单独核具体 volume，并由部署者执行。

业务与观测容器 Docker 日志均为 json-file，最多 `3 × 10 MiB`/容器；Docker 轮转后不能
补回。Loki 使用单机 filesystem/TSDB v13、7 天 compactor 保留期；这是时间保留，
**不是磁盘硬配额**，删除异步发生。部署者应为独立观测卷留容量并看磁盘使用量。
Grafana/Alloy/Loki healthy 只证明进程或初始配置可用，不能替代日志已采集或双端成功。
第三方容器的普通运维日志依靠 Compose 服务标签检索，不人为生成业务 trace。

## 一条命令收集

Node >=22、Docker CLI 可用即可，从仓库根执行（Windows 使用 `scripts/logs/collect.cmd`，
PowerShell 使用 `./scripts/logs/collect.ps1`；Linux 使用 `./scripts/logs/collect`）：

```sh
scripts/logs/collect --since 2026-10-29T11:00:00Z --until 2026-10-29T13:00:00Z --trace-id 4bf92f3577b34da6a3ce929d0e0e4736 --out ./logs-case
```

可只给时间窗或只给 trace（默认最近 1 小时）；`--project`、`--docker-context` 显式选择
采集目标，`--plugin-dir` 指向另一台机器复制来的公开日志目录。输出为一个新目录，含
各容器日志、插件固定文件、`manifest.json`；已有目录拒绝覆盖。分享前须检查每个文件，
尤其第三方容器日志和未标名的自由文本；自动脱敏不保证识别所有凭据。确认无秘密后，
只打包排障必需的文件，不直接转交整个目录。

脚本仅执行 Docker `ps` 与 `logs --since/--until`，不 inspect 配置/环境，不执行容器内
命令，不重启/修改容器。trace 是**对 Docker 时间窗结果的本地精确 JSON 筛选**，不是
服务端全量 trace 查询。每源最多 128 MiB/30 秒，保存退出码、缺源/截断；0 表示已发现
源的采集命令成功，2 表示缺源/部分失败，1 表示参数或采集本身失败。无匹配日志仍可为
0，不能据此宣称请求没发生。只保留脱敏后的采集文件；坏行/源缺失留 manifest。

## root 串行真实验收

作者只跑单元与随机端口临时 fixture；以下由持有唯一真实 PG/DSH/M2 槽的 root 执行。

1. 记录最终已审源码 SHA、实际 Docker context/endpoint、Compose project、所有镜像与
   DSH/插件版本；按公开流程全新部署。启用 profile 并确认没有外部 Loki/Alloy 端口、
   Grafana 匿名请求被拒、插件目录已实际挂载或导入。
2. 两个真实 DSH A/B 登录、READY，先分别完成 A→B、B→A 的真实回复，记录目标地址及
   会话，不靠 health 代替。随后使 B 的既有接收会话离线/撤销，再从 A 用该已知目标
   发一次，保留真实失败。必须看到插件实际 POST `/api/ai/v1/network/submit`；若被
   本机预检拒绝，记录该边界，另选可实际到达后端的失效连接场景，不能当整链已验。
3. 从这一次插件的 `HTTP request started`/失败日志取 trace（不要取 WSS 握手或旧重连
   trace）。按时间窗采集；远端插件日志导入 NAS 后，在 Grafana 一次查询同一 trace。
   应能按时间看到 plugin 出站/响应、edge 请求、gateway HTTP 与 IAM 验证 RPC、collab
   Submit 完成/拒绝或异常栈。核真实错误状态/代码，业务请求 ID 和 trace 各自一致；
   collab 停机导致没有服务端日志时，要记录 gateway 调用失败及缺源，不能补造 collab 段。
4. 负例：用临时测试客户端发缺失/超长/零 trace、重复 trace 头，检查安全重建；
   trace 不改变原 401/403/预算结果。针对一次后端异常核完整栈代码位置及秘密未泄漏，
   确认账户/tenant 等诊断身份与 usage 字段仍可查。不要往模型回显采集全量文本。
5. 恢复 B，重做真实双向互发、模型 auto/用量和登录退出相关 Beta 项。保存查询截图、
   manifest/日志哈希与缺源，运行 8 小时观察。只有 Beta 六项及 8 小时均完成才能宣布
   Beta 交付；LOG-1 局测、配置校验或面板健康都不等于该结果。

## 配置依据与局测

2026-10-29 核对官方版本源及实际短命 validator：

- [Loki v3.5.5](https://github.com/grafana/loki/releases/tag/v3.5.5)、
  [该版本 Dockerfile（/busybox 工具）](https://github.com/grafana/loki/blob/v3.5.5/cmd/loki/Dockerfile)、
  [filesystem/retention](https://grafana.com/docs/loki/latest/operations/storage/retention/)。
- [Alloy v1.10.2](https://github.com/grafana/alloy/releases/tag/v1.10.2)、
  [该版本 discovery.docker](https://github.com/grafana/alloy/blob/v1.10.2/docs/sources/reference/components/discovery/discovery.docker.md)、
  [validate CLI](https://github.com/grafana/alloy/blob/v1.10.2/docs/sources/reference/cli/validate.md)、
  [HTTP ready](https://grafana.com/docs/alloy/latest/reference/http/)。
- [Grafana 12.1.1](https://github.com/grafana/grafana/releases/tag/v12.1.1)、
  [Docker 文件凭据](https://grafana.com/docs/grafana/latest/setup-grafana/configure-docker/)、
  [面板/数据源 provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/)。

局测入口：`node --test clients/dsh-plugin/test/observability.test.js`、
`node --test scripts/logs/collect.test.mjs`、Go `edge` 的 `TestTrace*` 与 gateway
`internal/observability`、Java `TraceLoggingTest`/`GrpcTest`。它们验证 wire 传播、
负例、脱敏、跨语言 NATS trace 向量及 64 KiB 原预算、固定文件轮转/关闭和采集 fixture，
不冒真实部署证据。
