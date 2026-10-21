# NAS 独立测试栈（deploy/nas）

面向家里 NAS 的**隔离测试部署包**：与 CI 的 `deploy/compose`（host 网络 + 假 DSH）并行，不替换旧 Hub，不改 DNS/防火墙/路由。工人只准备模板与脚本；**不能访问或修改 NAS**。真实域名、LAN IP、目录只写在私有现场笔记，公开仓一律用 `example.com` / `192.0.2.x` / `/srv/container/auriga-test`。

本包 Compose 项目名**固定**为 `auriga-nas-test`（脚本忽略 `stack.env` 里的其它项目名，防止误删别的栈）。在 W3 交付真实 DSH 摘要与签名模板之前，**不得宣称真实验收完成**。

操作脚本均以 `sh deploy/nas/scripts/<操作>.sh` 调用；首装、重入与启动链也显式使用 `sh` 调用子脚本，支持 Git checkout / 解包后脚本没有执行位的文件系统。Linux 验证须在原生目录使用普通文件权限运行，不能只依赖 Windows 挂载上的可执行权限。

## 网络拓扑（H1）

| 网络 | 类型 | 成员 | 作用 |
|---|---|---|---|
| `auriga-nas-test_internal` | bridge，`internal: true` | postgres、nats、platform、registry、edge | 组件互访；**无**宿主端口映射 |
| `auriga-nas-test_publish` | bridge，非 internal | platform、registry | 仅用于把 7443/8081/5000 映射到宿主 `127.0.0.1`，供 **host 网络 agent** 连接；不接 LAN |
| LAN（external） | 现场已有网络 | **仅 edge**（静态 IPv4） | VPS/局域网入口 |
| `auriga-net-<实例 id>` | agent 创建的 owned bridge，`internal: true` | 该实例 + 受信 edge | 实例通过 edge 内部门户入口发送真实插件心跳，不接组件控制网 |

Docker 对「只挂 internal 网络」的容器不创建宿主端口映射；因此 NodeControl/registry 的 loopback 发布必须落在 `publish` 网上。后台仍不接 LAN：从 edge 的 LAN IP 无法直接打到 PG/NATS。

### 实例内部心跳入口

edge 的稳定容器名固定为 `auriga-nas-test-edge`，带 `auriga.role=edge` 标签；Compose 用同一标量 anchor 将该名字传给 agent 的 `AURIGA_AGENT_DOCKER_EDGE_CONTAINER`，并让 agent 等 edge 启动。驱动仍验证 edge 身份、网络成员与实例网络 ownership，再仅向对应实例的 internal bridge 挂接 edge 端点，并设置门户 Host 的 DNS alias。实例不加入 `auriga-nas-test_internal` 或 `publish`，后端不增加 LAN 或宿主发布。

`render.sh` 从现有 `AURIGA_PORTAL_HOST` 生成 `agent.env` 的 `AURIGA_AGENT_INSTANCE_PLATFORM_URL=http://<门户 Host>:8080`；同一个 Host 也写入 edge 的 `portalHost`，驱动仅取 URL 的 hostname 作为内部 DNS alias。心跳使用 edge 的容器内 HTTP 8080，不经公网 HTTPS、宿主回环端口或 gRPC 7443。现有 `AURIGA_INSTANCE_PARENT_DOMAIN` 同时传给 agent 的 `AURIGA_AGENT_INSTANCE_PARENT_DOMAIN`，与 edge 的实例父域一致，供 renderer 生成实例 `trustedHosts`。

这里准备的是源码配置与本机测试接线，不代表已在 NAS 部署或能够登录；没有继承开发栈的应急远程登录开关、观测代理或测试凭据。最终整合镜像、签名模板、现场网络和真实生命周期仍须由主控独立验收。

## 架构要点

| 组件 | 网络 | 端口（容器内） | 说明 |
|---|---|---|---|
| edge | LAN + internal | 8080 | 唯一对 LAN/VPS 入口；TLS 通常在上游终止；uid 65532 |
| platform | internal + publish | 8081、7443 | uid 10001；loopback 映射给 agent |
| postgres / nats | 仅 internal | 5432 / 4222 | NATS 监控 `127.0.0.1:8222` 不映射到宿主 |
| registry | internal + publish | 5000 | 宿主 loopback 供 agent `docker pull` |
| agent | `network_mode: host` | — | Docker socket + 同路径 data bind；容量 `MAX_INSTANCES=1` |

公网只反代到 edge；浏览器不直连 8081。一个 edge 入口仍对应完整后端（platform/NATS/DB），只是不对公网另开站点。

资源预算（建议）：基础服务合计约 ≤ 3.5–4 GiB RAM、约 2 CPU；实例策略上限 500m CPU / 512 MiB / 128 PIDs。

## 目录布局

```
$AURIGA_DATA_ROOT/            # 例：/srv/container/auriga-test（≥3 层，basename 为 auriga-*）
  stack.env                   # 可预置；无口令
  .auriga-initialized
  secrets/                    # 0700
  config/
    pki/                      # CA + workspace（platform 10001）
    pki-edge/                 # 仅 ca.crt + edge.crt/key（edge 65532，:ro）
  data/{postgres,nats,registry,agent}/
  socket-share/              # root:65532 0750；agent/edge 只共享这个父目录
    ingress/ingress.sock      # agent 创建叶子与标记，edge 只读挂载父目录
  enrollment/
  join/token
  results/
```

Agent 数据根 `$AURIGA_DATA_ROOT/data/agent` 保持 **0700**；共享父目录和 agent 创建的 ingress 叶子均为 **root:65532 0750**。叶子不是 bind mount，允许 W2H 原子创建/迁移；init/start 不预建叶子、不改成 edge uid。祖先必须归 root 所有且不可组/其他写（高层 root sticky 临时目录例外）；预检只诊断，不修改 NAS 既有共享目录权限。

## Agent / 宿主权限边界（M7）

agent 使用 host 网络 + root + `docker.sock`，被攻破约等于 NAS root（含其它容器与卷）。这是单机测试栈的已知取舍，不是生产加固模型：

- 实例隔离仍靠 agent policy（镜像前缀、CPU/内存/PID）与现有 Docker 驱动参数（`cap-drop ALL`、只读根、`no-new-privileges`）。
- agent 自身仅补回 `CHOWN/DAC_OVERRIDE/FOWNER`，用于分配实例 uid/gid、更新实例文件和共享 socket；实例容器仍丢弃全部 capabilities，不启用 privileged。
- registry **无认证**，且经 loopback 发布：NAS 上其它本机用户也可能向该 registry 推镜像；白名单前缀是主要约束。
- `platform.env` 内嵌 DB/NATS 口令与加密主密钥：任何能 `docker inspect` 的用户都能看到（服务当前读 env；`_FILE` 待服务侧支持）。

## 初始化与幂等

```sh
mkdir -p /srv/container/auriga-test
cp deploy/nas/env.example /srv/container/auriga-test/stack.env
# 编辑：LAN、Host、镜像 sha256:<id>、loopback 端口
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh deploy/nas/scripts/init.sh      # 需 root（chown）；再次只 render
sh deploy/nas/scripts/preflight.sh
sh deploy/nas/scripts/network-preflight.sh
sh deploy/nas/scripts/start.sh     # 逐端口/网络成员确认归属，允许本项目精确占用的重入
sh deploy/nas/scripts/smoke.sh     # 无 E2E hook 时 exit 2（PARTIAL）
sh deploy/nas/scripts/stop.sh
```

规则：

- 数据根 basename 必须以 `auriga-` 开头，路径 ≥3 层，拒绝符号链接与 `..`；管理配置/密钥/共享 socket 树内也拒绝符号链接，实例用户目录不扫描。
- 已有非空目录且无 `.auriga-initialized`（除 `stack.env` 外）→ 拒绝。
- 随机口令失败不写空文件；已有 secrets 不覆盖。
- 私钥 0600；init/render 以 root 运行，`chown` 失败即退出。
- `AURIGA_PLATFORM_SEED=true` 仅隔离测试首装，render 显式选择 `combined,e2e`，以通过 W2H 种子门禁；设回 false 后恢复 `combined`。不启用 dev profile。
- PostgreSQL 选用固定摘要的 **Debian postgres:17（uid/gid 999）**，含口令的初始化 SQL 归 `999:999`、权限 0600；不可直接替换为 uid 不同的 Alpine 镜像。

### 角色分离限制

- **workspace**：迁移 / 运行角色已分开。
- **iam / org**：combined 仍单角色兼迁移与运行（服务限制，不私改）。

### NATS 权限

- 无匿名登录；监控只绑容器 loopback。
- `$JS.API.>` 已收窄：platform 可首启创建 AURIGA_EVENTS/AURIGA_AUDIT、发布业务/审计事件及消费这两条流；routes_rw 只写 routes KV；edge 只读 routes、管理 sessions，并仅创建/拉取/确认 `edge-session-revoke` 撤销消费者。无任意流删除权限；`jetstream` 有内存/磁盘上限。
- edge 因共用一个 `natsUrl`，口令模式下为会话+路由并集；JWT 分用户待主控扩展。

## 镜像与 CI 工件

`.github/workflows/nas-bundle.yml`：仅 `workflow_dispatch`，输入完整 40 位 SHA；第三方 action 钉到提交；`persist-credentials: false`。

产出：

- `images/*.tar` + `SHA256SUMS` + **一份** `manifest.json`（commit、镜像 Id、tools、stack_env_hint）
- `versions.txt`、`auriga-nas-bootstrap`

GitHub Actions 工件下载后，文件执行位可能丢失（[官方说明](https://github.com/actions/upload-artifact#permission-loss)）。
在解包后的工件根目录核对 manifest 的提交和校验和，并在首次初始化前恢复预构建 bootstrap 的执行位：

```sh
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap
```

源码 checkout 对应 `deploy/nas/bin/auriga-nas-bootstrap`。其余操作脚本均以 `sh` 调用，无需执行位；NAS 无需 Go。

NAS 上 `load-images.sh` 后，把 `AURIGA_IMAGE_PLATFORM/EDGE/AGENT` 设为 manifest 里的 **`sha256:<id>`**（本地 Id，compose 不 pull）。基础镜像仍用 `name@sha256:…`。

## 网络与 VPS

- `network-preflight.sh`：外部网络名/driver/IPAM、IP 落在子网、非网关/广播、成员占用；本项目 edge 已占用时允许重入。ping 无响应不能证明空闲；不确定 exit 4。
- VPS 示例：`deploy/nas/templates/caddy.fragment.example`。通配证书须覆盖实例父域。

```sh
curl --resolve portal.example.com:8080:192.0.2.10 -H 'Host: portal.example.com' http://portal.example.com:8080/
```

## 冒烟

`smoke.sh`：健康、SPA（`text/html` + root/title）、未登录拒绝。无 `AURIGA_E2E_HOOK` 时输出 `SMOKE PARTIAL` 并 **exit 2**（除非 `AURIGA_SMOKE_ALLOW_PARTIAL=1`）。完整生命周期：

```sh
export AURIGA_E2E_HOOK=/path/to/w2v-or-w3-runner
sh deploy/nas/scripts/smoke.sh
```

## 升级 / 回退 / 备份

- 升级：换摘要镜像；迁移不可假设换旧镜像即回退。
- 停止：只停本项目；`--with-registered-instances` 对发现和登记的 ID 均要求 `auriga.managed=true`、实例 label 和规范化后严格位于 `data/agent/` 下的 bind。外方 label、路径前缀碰撞和链接逃逸均拒绝。禁止 prune / `down -v` / orphans 清扫。
- 备份：`secrets/`、`data/postgres`、`data/agent`、`enrollment/`、`stack.env`。

## 弱隔离（纯 IP）

无独立 Host 时 cookie/Origin 隔离变弱；验收应使用门户/管理台/实例父域三个来源。

## 证书轮换（L6）

bootstrap 叶子证书约 825 天；到期前需重新 init/签发并滚动 edge/platform，本包无自动轮换提示。

## 本地验证

```sh
sh deploy/nas/test/run-tests.sh
# Linux root 可验证真实 chown/mode，使用 CI 预构建的 bootstrap 二进制：
NAS_ALLOW_NONROOT=0 sh deploy/nas/test/run-tests.sh
(cd infra/edge && go test -count=1 ../../deploy/nas/test/nats_policy_test.go)
```

具备 Docker Compose 与 Python 3 时，脚本测试还会只读归一化实际 Compose 配置，检查默认及自定义门户/实例父域的心跳 URL、稳定 edge 名称/标签及既有网络、权限边界；不启动容器或占用端口。测试凭据均在新临时目录随机生成，退出清理，不读取现场材料。
