# N1 网络部署接线

这是公开的单机 Compose 配方，沿用 M2 的 combined/dev 验收栈和制品准备流程。
它不是生产加固配方：既有数据库、JetStream 测试口令及端口维持原行为。

## 启用前提与当前缺口

必须先包含 NETN1-GWIAM（PR #138）的网关运行入口与 IAM RPC 实现，
否则 `network-gateway` 会因不认识 `--network-config` 而退出。
IAM 现有安装令牌导出器只签发 `svc:edge`；本配方不拿该令牌冒充网关。
**当前尚缺公开的 `svc:ai-gateway` 服务 JWT 签发与轮换出口，须由主控补齐后才能完整起栈。**
bootstrap 不持有 IAM 签名私钥，也不伪造 JWT；下面的启动步骤以该出口已经完成为前提。
这项缺口不能通过复制 edge JWT、设备 token、手工修改业务数据库解决。

网关必须从专用只读目录读取 `service.jwt`，其 `sub=svc:ai-gateway`、
`aud=auriga.internal`、`ptyp=internal_service`。签发端在宿主机的专用目录中原子替换该文件，
并在过期前持续轮换；网关逐 RPC 重读目录挂载中的文件。
可用绝对路径环境变量 `AURIGA_NETWORK_TOKEN_DIR` 指向这个目录，默认是
`deploy/compose/var/network-tokens`。缺目录、缺文件或空文件会拒绝启动；
错主体、过期或错签名由 IAM 拒绝。此目录不能与 edge 令牌目录共用。

## 拓扑与生成材料

`nats` 继续只承担已有 JetStream 业务，使用 `nats/nats.conf`。
`network-nats` 固定为 NATS 2.12.6，使用 bootstrap 生成的 `var/network/nats.conf`，
没有宿主机发布端口、没有 JetStream、没有匿名回退。

- 外部客户端 → 门户精确路径 `/net/v1/ws` → edge → `network-nats:8443`（WSS）。
- 网关 → `network-nats:4222`（双向 TLS），auth 与 ops 为不同控制账户。
- 网关 → `host.docker.internal:9091`（IAM 双向 TLS + 专用服务 JWT）。
- `AUTH` 承担 callout；`SYS` 只允许 CONNZ/KICK 及私有回复订阅；
  控制用户只允许 STANDARD 连接，不能通过 WSS 登录。
- `CLIENTS` 固定 64 KiB payload、8 subscriptions，和 `SVC` 只导入导出网络 up/down/reply 主题。
  SVC 的业务适配器不在本次部署任务中；本网关仅执行鉴权和吊销检查，不能据此宣称消息业务闭环完成。

bootstrap 随机生成账户签名 nkey、curve nkey、auth/ops 口令和 Broker/网关客户端证书。
`var/network` 为 0700，私钥、口令、Broker 配置为 0600；不向终端打印密钥。
Broker 与网关挂载同一个受信配置目录，网关从其中读取唯一的 `authorization.timeout: 2`；
WSS 继承该值，redeem 超时为 500ms。不要分别修改两份 timeout。
edge 通过 `SSL_CERT_FILE` 信任该安装的 CA，验证 Broker 证书的 `network-nats` 名称。
WSS 和控制连接均不跳过证书验证。

材料全部纳入原安装回执。相同输入重复运行只核验，不轮换；
缺失、损坏或换输入会拒绝，并保留原材料。旧安装不能直接补生成这些文件，需另行规划升级；
不要删除正在使用的安装。证书期限沿用现有 90 天策略，自动续期不在此配方中。
`var/` 已被 git 忽略；备份时按秘密保护整个安装目录，不把内容复制进源码或报告。

## 域名与纯 IP

入口仍由同一份经过契约校验的部署 `access.json` 决定。两种模式都只允许门户的精确 Origin，
包含协议与非默认端口；管理台、实例 Origin 不自动获得网络访问权。
无 Origin 的原生客户端仍须完成 NATS CONNECT/IAM 鉴权。

- 域名模式：例如门户 `http://portal.example.com:8080`、管理台
  `http://admin.example.com:8084`，实例使用 `i.example.com` 的独立子域；
  网络入口为 `ws://portal.example.com:8080/net/v1/ws`。
- 纯 IP 模式：例如门户 `http://192.0.2.10:8080`、管理台
  `http://192.0.2.10:8084`，实例模式为 `path`、前缀 `/i/`，
  必须明确 `trustedTeamAcknowledged: true`；网络入口为
  `ws://192.0.2.10:8080/net/v1/ws`。这是 ADR0020 的可信团队弱隔离档，不放宽 CORS 或身份校验。

当前 Compose 的 access loader **只接受 direct HTTP**（包括域名和 IP），
需要 `insecureHttpAcknowledged: true`。内部 WSS 不会把公网 HTTP 自动变成 TLS；
公网 HTTPS/WSS 的完整入口配置需要另行扩展现有 access/edge 配方，不能把本配方当作已支持。
不要绕过 access 校验、改写 Host/Origin 或临时放开 CORS 伪装 HTTPS 已完成。
上述地址均为文档占位符，部署时由操作者提供自己的参数。

## 本机准备与验证

在 Linux/WSL 工位根目录执行；先只读确认 Docker context/endpoint 确实是本机 Unix socket，
不要连接远端 daemon：

```sh
docker context show
docker context inspect
docker info --format '{{.Name}} {{.OperatingSystem}}'

# -access-file 内容须符合 contracts/deployment/access.schema.json；
# edgeContainer 必须与 COMPOSE_PROJECT_NAME 对应的 <project>-edge 一致。
bash deploy/compose/scripts/prepare.sh --access-file /absolute/path/access.json

# 以下以公开的服务 JWT 签发/轮换出口已经完成且已生成 service.jwt 为前提。
export AURIGA_NETWORK_TOKEN_DIR=/absolute/path/ai-gateway-tokens
docker compose --env-file deploy/compose/var/compose.env \
  -f deploy/compose/docker-compose.yml config --quiet
docker compose --env-file deploy/compose/var/compose.env \
  -f deploy/compose/docker-compose.yml up -d --build
```

prepare 会构建并收集本机 fixture 制品证明，然后调用公开 bootstrap；
网络材料无需私有脚本或现场手工生成。
验证应覆盖 Broker 健康、网关进程存活、域名/IP 两种入口的真实 WebSocket Upgrade、
合法一次性票据 CONNECT、坏票据/重放/错 audience/错 Origin 拒绝、JetStream 原业务仍可访问。
仅 `healthz`、配置解析或一次 Upgrade 均不能替代 IAM 鉴权及双 DSH 业务验收。

不启动服务的静态验证：

```sh
(cd deploy/compose/bootstrap && go test ./... && go vet ./...)
(cd deploy/bootstrap && go test ./... && go vet ./...)
```

测试用同版本 NATS 解析生成配置，并校验 WSS/mTLS、账户隔离、密钥匹配、连接类型及证书名称；
域名/IP CLI 用例检查安装回执、幂等以及网络材料损坏/缺失的拒绝行为。
