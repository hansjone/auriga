# NAS 独立测试栈（deploy/nas）

面向家里 NAS 的**隔离测试部署包**：与 CI 的 `deploy/compose`（host 网络 + 假 DSH）并行，不替换旧 Hub，不改 DNS/防火墙/路由。工人只准备模板与脚本；**不能访问或修改 NAS**。真实域名、LAN IP、目录只写在私有现场笔记，公开仓一律用 `example.com` / `192.0.2.x` / `/srv/container/auriga-test`。

本包 Compose 项目名**固定**为 `auriga-nas-test`（脚本忽略 `stack.env` 里的其它项目名，防止误删别的栈）。在 W3 交付真实 DSH 摘要与签名模板之前，**不得宣称真实验收完成**。

## 网络拓扑（H1）

| 网络 | 类型 | 成员 | 作用 |
|---|---|---|---|
| `auriga-nas-test_internal` | bridge，`internal: true` | postgres、nats、platform、registry、edge | 组件互访；**无**宿主端口映射 |
| `auriga-nas-test_publish` | bridge，非 internal | platform、registry | 仅用于把 7443/8081/5000 映射到宿主 `127.0.0.1`，供 **host 网络 agent** 连接；不接 LAN |
| LAN（external） | 现场已有网络 | **仅 edge**（静态 IPv4） | VPS/局域网入口 |

Docker 对「只挂 internal 网络」的容器不创建宿主端口映射；因此 NodeControl/registry 的 loopback 发布必须落在 `publish` 网上。后台仍不接 LAN：从 edge 的 LAN IP 无法直接打到 PG/NATS。

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
  ingress/ingress.sock        # 独立目录；edge 只挂这里，不挂 data/agent
  enrollment/
  join/token
  results/
```

Agent 数据根 `$AURIGA_DATA_ROOT/data/agent` 保持 **0700**；ingress 单独 **0750** 且属主 65532。

## Agent / 宿主权限边界（M7）

agent 使用 host 网络 + root + `docker.sock`，被攻破约等于 NAS root（含其它容器与卷）。这是单机测试栈的已知取舍，不是生产加固模型：

- 实例隔离仍靠 agent policy（镜像前缀、CPU/内存/PID）与现有 Docker 驱动参数（`cap-drop ALL`、只读根、`no-new-privileges`）。
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
sh deploy/nas/scripts/start.sh     # 已运行时跳过「外方」端口/IP 冲突检查
sh deploy/nas/scripts/smoke.sh     # 无 E2E hook 时 exit 2（PARTIAL）
sh deploy/nas/scripts/stop.sh
```

规则：

- 数据根 basename 必须以 `auriga-` 开头，路径 ≥3 层，拒绝符号链接与 `..`。
- 已有非空目录且无 `.auriga-initialized`（除 `stack.env` 外）→ 拒绝。
- 随机口令失败不写空文件；已有 secrets 不覆盖。
- 私钥 0600；init/render 以 root 运行，`chown` 失败即退出。
- `AURIGA_PLATFORM_SEED=true` 仅首装。

### 角色分离限制

- **workspace**：迁移 / 运行角色已分开。
- **iam / org**：combined 仍单角色兼迁移与运行（服务限制，不私改）。

### NATS 权限

- 无匿名登录；监控只绑容器 loopback。
- `$JS.API.>` 已收窄到 sessions/routes 相关流；`jetstream` 设 `max_memory_store` / `max_file_store`。
- edge 因共用一个 `natsUrl`，口令模式下为会话+路由并集；JWT 分用户待主控扩展。

## 镜像与 CI 工件

`.github/workflows/nas-bundle.yml`：仅 `workflow_dispatch`，输入完整 40 位 SHA；第三方 action 钉到提交；`persist-credentials: false`。

产出：

- `images/*.tar` + `SHA256SUMS` + **一份** `manifest.json`（commit、镜像 Id、tools、stack_env_hint）
- `versions.txt`、`auriga-nas-bootstrap`

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
- 停止：只停本项目；`--with-registered-instances` 仅停挂载落在 `data/agent` 下且（优先）带实例 label 的容器。禁止 prune / `down -v` / orphans 清扫。
- 备份：`secrets/`、`data/postgres`、`data/agent`、`enrollment/`、`stack.env`。

## 弱隔离（纯 IP）

无独立 Host 时 cookie/Origin 隔离变弱；验收应使用门户/管理台/实例父域三个来源。

## 证书轮换（L6）

bootstrap 叶子证书约 825 天；到期前需重新 init/签发并滚动 edge/platform，本包无自动轮换提示。

## 本地验证

```sh
sh deploy/nas/test/run-tests.sh
```
