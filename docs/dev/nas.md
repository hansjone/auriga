# NAS 独立测试栈（deploy/nas）

面向家里 NAS 的**隔离测试部署包**：与 CI 的 `deploy/compose`（host 网络 + 假 DSH）并行，不替换旧 Hub，不改 DNS/防火墙/路由。工人只准备模板与脚本；**不能访问或修改 NAS**。真实域名、LAN IP、目录只写在私有现场笔记，公开仓一律用 `example.com` / `192.0.2.x` / `/srv/auriga-test`。

本包默认 Compose 项目名 `auriga-nas-test`。在 W3 交付真实 DSH 摘要与签名模板之前，**不得宣称真实验收完成**。

## 架构要点

| 组件 | 网络 | 端口（容器内） | 说明 |
|---|---|---|---|
| edge | 外部 LAN（固定 IPv4）+ 私有 bridge | 8080 | 唯一对 LAN/VPS 入口；TLS 通常在上游终止 |
| platform | 仅 bridge | 8081 HTTP、7443 NodeControl | 不对公网发布 |
| postgres / nats | 仅 bridge | 5432 / 4222 | NATS 监控 `127.0.0.1:8222` 不映射到宿主 |
| registry | bridge + 可选宿主 loopback | 5000 | 供 host 网络 agent 拉镜像 |
| agent | `network_mode: host` | — | Docker socket + **同路径** data bind；容量 `MAX_INSTANCES=1` |

后台组件不接 LAN。DSH 实例不接 LAN。公网只反代到 edge；浏览器不直连 8081。

资源预算（建议）：基础服务合计约 ≤ 3.5–4 GiB RAM、约 2 CPU；实例策略上限 500m CPU / 512 MiB / 128 PIDs。按现场启动测量再调。

## 目录布局

```
$AURIGA_DATA_ROOT/          # 例：/srv/auriga-test
  stack.env                 # 由 env.example 复制；含镜像摘要，无口令
  .auriga-initialized       # init 成功标记
  .auriga-release           # 加载的 CI 工件清单
  secrets/                  # 0700；口令与主密钥，0600
  config/                   # 渲染后的配置（含口令的文件 0600）
  data/{postgres,nats,registry,agent}/
  enrollment/               # 平台写入的应急凭据（勿进日志）
  join/token                # 入网令牌（空文件直到签发）
  results/                  # 冒烟日志、registered-instances.txt
```

Agent 数据根为 `$AURIGA_DATA_ROOT/data/agent`，宿主与容器绝对路径必须相同。

## 初始化与幂等

```sh
cp deploy/nas/env.example /srv/auriga-test/stack.env
# 编辑 stack.env：LAN 网络名、edge IP、Host、镜像摘要、宿主 loopback 映射端口
export AURIGA_DATA_ROOT=/srv/auriga-test
sh deploy/nas/scripts/init.sh      # 首次生成密钥；再次只 render
sh deploy/nas/scripts/preflight.sh
sh deploy/nas/scripts/network-preflight.sh
sh deploy/nas/scripts/start.sh
sh deploy/nas/scripts/status.sh
sh deploy/nas/scripts/smoke.sh
sh deploy/nas/scripts/stop.sh      # 默认保留数据与密钥
```

规则：

- 首次随机生成 DB/NATS 角色口令、加密主密钥、CA/签名钥；标记存在则只重新渲染。
- 已有 postgres/nats 数据却缺密钥 → **拒绝**，绝不覆盖。
- 私钥/应急相关文件 0600；目录不用 0777。
- `AURIGA_PLATFORM_SEED=true` 仅首装；启动成功后改为 `false` 并再 render。
- 不把 CI 假签名、假 DSH、`no_auth_user`、edge `publish/subscribe >` 带进本包。

### 角色分离限制

- **workspace**：迁移角色 `auriga_workspace_owner` 与运行角色 `auriga_workspace_app` 已分开。
- **iam / org**：当前 combined 服务仍用单一角色兼迁移与运行（与现网服务能力一致）。要拆开需改服务，不在本任务范围。

### NATS 权限

- 无 `no_auth_user`；监控只绑容器 loopback。
- `platform` / `routes_rw` / `edge` 分用户。edge 因现二进制对 session 与 routes **共用一个 `natsUrl`**，口令认证下使用会话+路由只读的并集；若要用完全分离的 JWT `.creds`，待主控扩展 edge 或签发凭据。

## 镜像与 CI 工件

工作流：`.github/workflows/nas-bundle.yml`（仅 `workflow_dispatch`，指定精确提交）。

产出（无凭据）：

- `images/{platform,edge,agent}.tar` + `SHA256SUMS` + `manifest.json`（提交、镜像 ID、工具版本）
- `deploy-nas/` 模板副本与 `auriga-nas-bootstrap`（linux/amd64）
- edge 镜像内含 portal/admin 静态资源（`/srv/auriga/static/...`）

NAS 上：

```sh
sh deploy/nas/scripts/load-images.sh /path/to/bundle
# 按 manifest 把 stack.env 里的 AURIGA_IMAGE_* 改成带 digest/本地 ID 的引用
```

基础镜像（postgres/nats/registry）也须写死摘要；由主控在现场笔记或 bundle 附注中给出。

真实 DSH 镜像摘要来自 **W3**；未交付前可完成模板与静态冒烟，但验收文档须标明未完成。

## 网络与 VPS

- 外部网络必须已存在：`AURIGA_LAN_NETWORK` + `AURIGA_EDGE_LAN_IP`。`network-preflight.sh` 检查名称/driver/IPAM/成员占用；**不新建** QNAP 父网络。ping/TCP 无响应不能证明地址空闲；无法确认时退出码 4。
- 宿主只检查 **实际映射** 的 loopback 端口（NodeControl / registry / platform HTTP），不因 NAS 上已有 5432/8080 而改容器内端口。
- VPS 反代示例：`deploy/nas/templates/caddy.fragment.example`（`example.com` / `192.0.2.10`）。通配证书必须覆盖实例父域（如 `*.i.example.com`），浅层 `*.example.com` 不够。Caddy/DNS-01 由主控现场准备。

验证三个来源（不改真实 DNS）：

```sh
curl --resolve portal.example.com:8080:192.0.2.10 -H 'Host: portal.example.com' http://portal.example.com:8080/
curl --resolve admin.example.com:8080:192.0.2.10 -H 'Host: admin.example.com' http://admin.example.com:8080/
curl --resolve id.i.example.com:8080:192.0.2.10 -H 'Host: id.i.example.com' http://id.i.example.com:8080/
```

`smoke.sh` 已按此方式检查门户/管理台静态资源与未登录拒绝。

## 冒烟与完整生命周期

本包 `smoke.sh`：健康、静态资源、未登录拒绝、数据目录仍在。完整登录→真实实例→闲置/唤醒→吊销依赖 W2V/W3 公开脚本：

```sh
export AURIGA_E2E_HOOK=/path/to/w2v-or-w3-runner
sh deploy/nas/scripts/smoke.sh
```

不要复制旧假阳性断言。审计/诊断脚本不得打印展开后的口令、完整 Env、cookie、令牌或应急文件内容。

## 升级 / 回退 / 备份

- 升级：新摘要镜像 + 同数据根；先停项目再换镜像；数据库迁移不可假设“换回旧镜像即回退”。
- 回退：保留 `secrets/` 与 `data/`；只换 release 镜像与配置渲染。
- 备份：至少备份 `secrets/`、`data/postgres`、`data/agent`、`enrollment/`、`stack.env`、release marker。
- 停止：`stop.sh` 只停本项目；可选 `--with-registered-instances` 读 `results/registered-instances.txt`。禁止 prune、`down -v`、按宽泛名字删容器。

## 弱隔离边界（纯 IP）

若暂时只用 IP、无独立 Host 名，门户/管理台/实例的 cookie 与 Origin 隔离会变弱。生产验收应使用三个独立 Host（及实例父域），经 edge 分流；文档中的 IP 方案仅作连通性调试。

## 本地验证（开发机）

本机通常无 Docker。可跑：

```sh
sh deploy/nas/test/run-tests.sh
```

覆盖：路径拒绝、初始化幂等、缺密钥拒绝、停止范围、NATS 模板约束、无私有地址泄漏。Compose/镜像以 `nas-bundle` 工作流的 `config --quiet` 为准，不伪造 NAS 成功。
