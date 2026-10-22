# M2 Compose 的正式入口与 IAM mTLS 装配

本页描述一次性公开 M2 测试栈，配置生成器在 `deploy/compose/bootstrap`。
先前 [m2.md](m2.md) 中的单 edge 监听、明文 8081、回环 HTTP JWKS 和
明文心跳观察代理步骤不能直接套用到这套配置。这里的 domain M2 仍使用
Node HTTP 客户端手填虚拟 Host；它不是实际 IP 浏览器验收，也不证明 IP2/IP3 完成。

## 共享入口配置

从 `deploy/compose` 运行 `./scripts/prepare.sh`，公开 bootstrap 使用契约生成的
`deployment.Access`，序列化后调用生产 `deploymentconfig.Load`，通过后才写入
`var/access.json`。edge 与 IAM 挂载同一文件，不再各自手填 Host 列表。
固定租户为 dev seed 的 `00000000-0000-0000-0000-000000000001`。

- 门户及实例：`http://portal.example.com:8080`、`http://<instanceId>.i.example.com:8080`，
  edge portal socket 为 `0.0.0.0:8080`。
- 管理台：`http://admin.example.com:8084`，独立 admin socket 为 `0.0.0.0:8084`。
- 实例内控：`http://auriga-instance.internal:8083`，独立 socket 为 `0.0.0.0:8083`，
  不发布宿主端口；只能接正式 self/heartbeat 白名单。

这些是本套 M2 的端口，不是产品硬编码。浏览器 HTTP 有明确
`insecureHttpAcknowledged=true`；issuer 固定为完整门户 Origin，包含 `:8080`。
生成的 `access.env` 将相同 issuer、team Origin 和内部平台 URL 交给 Compose。
dev profile 用于 seed，Compose 显式覆盖其 Servlet TLS、监听地址和明文 hop 例外，
非回环 HTTP 不获放行。combined 的 JWT 解码使用 IAM 本地公开 JWKSource，
不为自取 JWKS 再开未认证 HTTP 监听。

`COMPOSE_PROJECT_NAME` 必须在 prepare 和 Compose 启动时一致；prepare 将实际
`<project>-edge` 传给 `-edge-container`，写入受信 edge 身份。直接调用 bootstrap 时
也应传这个参数。控制网络使用本套临时子网 `172.29.240.0/24`，固定 edge 地址
`172.29.240.10`。如果测试宿主已有冲突路由，应换干净测试环境或统一修改这套公开
装配后重验，不能只手改 peer 白名单。

新一轮应同时设置独立 `COMPOSE_PROJECT_NAME` 与 `AURIGA_M2_DATA_ROOT`（例如
`/var/lib/auriga-m2-new-run`）。后者将宿主和容器的 agent 数据根保持同一路径，edge
只读挂载相同位置；e2e 的 `AGENT_VOLUME` 也使用这个值。不要复用封存测试的数据目录。

## 专用 IAM HTTP-hop 证书

bootstrap 复用现有测试 CA，额外生成以下两份独立证书和私钥：

- `pki/iam.crt`：仅 serverAuth，唯一 DNS SAN 为 `platform`。
- `pki/edge-iam.crt`：仅 clientAuth，唯一 DNS SAN 为 `edge.auriga.internal`。

旧 `workspace.crt` 与 URI SAN 的 `edge.crt` 继续用于原节点链路，不借它们认证 IAM。
IAM、org、workspace 三个 upstream 全部指向同一 `https://host.docker.internal:8081`，
edge `iamHop.serverName=platform` 固定验证服务证书 DNS。CA、EKU、有效期及客户端证书
校验保持启用；不使用 `-k`、代理环境或 `InsecureSkipVerify`。

Servlet connector 对 combined 三个 API 统一要求客户端证书，IAM 还同时核对固定
edge DNS SAN 与 TCP 对端精确白名单。M2 白名单为 edge 的 `172.29.240.10` 与本地
健康检查的 `127.0.0.1`，不使用整个 Docker 网段。prepare 仅对本轮生成的
`edge-iam.key` 设置 group 65532、模式 0640，供固定 distroless edge 组和拥有者读取。
这要求本机 root 或可非交互执行 `sudo chgrp`；失败就停止，不放宽为可写或改全局 ACL。
这是一次性测试材料权限方案，不代替正式安装的秘密分发设计。

同一生成器还输出 `iam-client.json` 和 `iam-healthcheck.sh`。容器健康检查与 CI 等待
都调用后者；e2e 的直接 fake 心跳调用读取前者的 CA、客户端证书、私钥及固定 serverName。
手动确认健康应运行：

```sh
cd deploy/compose
sh var/iam-healthcheck.sh
```

脚本使用 `--resolve platform:8081:127.0.0.1` 保持实际回环连接与正确 DNS 校验，
不会修改系统 hosts 或信任库。返回健康只证明进程健康，不替代登录、生命周期或隔离验收。

## 合成客户端与尚未完成的链路

`e2e/m2.mjs` 从共享 access 文件读取完整入口与端口，将门户请求投到 8080、管理请求投到 8084，Host 保留外部端口，
cookie 写操作与实例 WS 使用完整 Origin。登录事务分别使用
`auriga_portal_login_tx` 和 `auriga_admin_login_tx`；cookie jar 按 hostname 处理
作用域，不假设不同端口隔离 cookie。原真实心跳、空闲回收、告警唤醒、双会话撤销、
WS 关闭时序及实际 Docker 网络隔离断言继续保留。

当前 agent 的内部 URL 可派生 DNS alias，但 renderer 的 `TrustedHosts` 仍只接
`instanceParentDomain`，尚缺独立完整浏览器 Origin 的正式消费。这属于 IP2；不能把
内部 alias 当浏览器入口，或往 profile 填未知字段绕过。

## 真实心跳观察模式

real 模式继续要求 `RAW_HEARTBEAT_FILE` 与原有真实心跳断言，观察器不能代发心跳。
公开准备步骤显式选择模式，随后启动观察器，再启动 Compose 栈：

```sh
cd deploy/compose
export DSH_FIXTURE=real
export AURIGA_M2_HEARTBEAT_OBSERVER=1
./scripts/prepare.sh
export RAW_HEARTBEAT_FILE="$PWD/var/raw-heartbeats.jsonl"
node scripts/heartbeat-observer.mjs &
observer_pid=$!
trap 'kill "$observer_pid" 2>/dev/null || true' EXIT
docker compose --env-file var/compose.env build
docker compose --env-file var/compose.env up -d
```

不要复用旧证据文件；观察器仍以 `wx` 创建 0600 新文件。后续依照原 M2 等待健康、
获取节点数据路径，回到仓库根运行 `DSH_FIXTURE=real node e2e/m2.mjs`，保留原全部真实空闲窗口、
门锁、隔离及撤销断言。这段装配命令本身不构成验收通过证据。

该参数传给 bootstrap 的 `-heartbeat-observer`，生成 `heartbeat-observer.json`，
并把三类 API **一起**切换到同一个 `https://host.docker.internal:8082`。
不需要手改 workspace upstream，也不将普通 HTTP 代理加入正式 transport。
观察器入站要求 CA 验证的 clientAuth 证书及固定 `edge.auriga.internal` DNS SAN；
出站仅连数值回环 IAM 8081，使用专用客户端证书、CA 及固定 `platform` DNS 校验。
它透明保留完整 Host、入口 metadata、请求体和上游状态，不强改成门户 Host。
只有实际 workspace 的 200 心跳才采集原白名单字段，认证原文及未知字段不写证据。
缺证书、同 CA 错 DNS 身份、错 CA、错服务 DNS 和出站证书角色均有真实 TLS 负向回归。
观察器只是本机本轮测试工具，不是生产安装组件。

## 仍需后续装配的边界

本配置没有伪造服务 JWT。现有 `iamRpc` 要求正式 mTLS、`svc:edge` 服务 JWT 的签发与
轮换；短期 JWT 写死到文件不能代替生命周期刷新。Introspect/服务 JWT 未完整装配的
部分仍按失败关闭处理，不能因为 domain 合成客户端或健康检查通过宣称 IP 管理链完成。
最终结果必须由包含冻结 JC1、IP1b、IP1c 前置的精确 CI 或审核后的全新独立栈提供。

## 针对性回归

在 `deploy/compose/bootstrap` 使用固定 Go 1.27.3，执行：

```sh
GOWORK=off go vet ./...
GOWORK=off go test -count=1 -v ./...
GOWORK=off go build ./...
```

测试运行实际 bootstrap CLI，加载实际输出并检查 seed/issuer、三角色 socket、
内控 alias/端口、三个 HTTPS upstream、专用证书 SAN/EKU/CA 与原节点身份保持。
共享 socket、issuer 漂移、缺 HTTP 确认、浏览器/内部 alias 冲突和内控端口漂移均须拒绝。
仓库根另跑 `node --test e2e/lib/*.test.mjs`，保留全部原断言。
