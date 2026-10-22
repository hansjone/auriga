# Edge → IAM 受信入口 hop（IP1）

本协议仅装配既有内部身份通道，不新增公共 API、权限或 JWT claim。入口部署身份仍唯一来自
`access.schema.json`，edge 和 IAM 必须加载相同安装配置；未装配本通道时新入口模式的 IAM 请求失败关闭。

## 传输与安装配置

edge 基础运行配置 `upstreams.iam` 定位 IAM HTTP 服务，`iamHop` 提供
`certFile`、`keyFile`、`caFile`、`serverName`。必须是 HTTPS，按 CA、有效期、serverAuth 和固定
DNS `serverName` 完整校验服务端证书；客户端使用专用带 clientAuth、安装固定 edge DNS SAN 的证书。
不复用仅 URI SAN 的节点 ingress 凭据，不跳过 TLS 验证，不采信环境 HTTP 代理。

combined 部署的 Servlet connector 对 IAM/org/workspace 统一 `client-auth=need`。
三者 upstream 若为完全相同规范 scheme+authority 的 HTTPS origin，edge 仅复用此专用 TLS
transport；org/workspace 不附加 IAM 入口元数据，也不接收 IAM cookie 白名单。
split 的不同 HTTPS origin 尚无独立认证 transport 配置时返回 503，不把 IAM serverName/客户端
证书盲套到不同目标；不能据此声称 split 模式已验。显式开发例外中各组件也只能使用固定数值
loopback HTTP，禁环境代理。内部 self/heartbeat 向 workspace 的转发同样受此传输约束。

IAM 的 Servlet 容器必须校验该 CA 并设置 `client-auth=need`；
`auriga.iam.edge-hop.dns-san` 是固定 edge 客户端 DNS SAN，
`auriga.iam.edge-hop.peer-addresses` 是实际 TCP 对端精确 IP 白名单，二者同时核验。
普通请求头不能伪造证书属性，外置 TLS 卸载若丢失容器已验证证书，不满足本协议。
两端默认 `allowLoopbackHttp` / `auriga.iam.edge-hop.allow-loopback-http` 为 false。
开发例外须两端显式启用，目标和真实对端均为固定数值 loopback，IAM 同时要求其 peer 白名单；
禁止 DNS 名解析后回环以及非回环明文。该例外不适用于 CSRF gRPC 客户端。

IAM 加载入口配置的属性是 `auriga.iam.deployment-access-file`。edge 的入口文件来自
`--access-config`、`accessConfigFile` 或 `AURIGA_EDGE_ACCESS_CONFIG`；命令行优先于后两者。

## 单值内部元数据

edge 先验证实际 listener、TLS/受信代理、完整 authority、Origin，再删除外部所有同名头及
`Forwarded` / `X-Forwarded-*` / 客户端身份头，逐请求写入：

- `X-Auriga-Entry-Origin`：该 listener 安装配置的 serialized portal/admin Origin，默认端口省略。
- `X-Auriga-Entry-Role`：仅 `portal` 或 `admin`，来自实际 listener。
- `Host`：已校验的完整浏览器 authority，保留非默认端口；不能替换成 IAM 内部地址。

无 tenant 头；IAM 根据同一部署配置查租户并复核两个内部头均为单值、角色/完整 Origin/Host
相符。请求头不能选择 scheme、tenant、audience；安装配置中的内部 alias 不允许进入此通道。
`X-Auriga-Client-IP` 仍由 edge 按实际对端重写，其作用仅为限流审计，不用于入口鉴权。

## Cookie 最小转交

edge 仅向 IAM 转交下列精确操作所需的当前入口 cookie，重名歧义拒绝；
HTTPS 使用 `__Host-` 前缀，HTTP 无前缀，绝不把另一 audience cookie 或旧 `auriga_login_tx` 转交。

- 精确 `POST /api/iam/v1/admin/session/csrf-tokens`：仅本次实际消费的 admin session。
- 已登记 login transactions 创建、查询、advance、events、DELETE 和 provider callback：仅当前
  `auriga_portal_login_tx` / `auriga_admin_login_tx`；创建/advance/DELETE 为 link/stepup
  另转当前 session，以及 portal/domain 原 CSRF cookie。IAM 在线复查 purpose、会话、CSRF 和事务绑定。
- 精确 `GET|POST /oauth2/authorize`：当前 session 和 portal/domain CSRF cookie；IP admin
  旧同意表单未具备正式内存 token 流程，因此拒绝，不能读取门户 CSRF。
- 精确 `POST /api/iam/v1/logout`：当前 session、当前入口事务 cookie，以及 portal/domain CSRF cookie。

其它 IAM 操作不转 Auriga cookie；普通业务使用当前入口会话换得 JWT。org/workspace 业务、实例
上游均不得接收上述原文 cookie；内部 self/heartbeat 只使用实际 ait 令牌。
标准 token/device_authorization 的 Basic 客户端凭据、userinfo Bearer 仅在各精确操作保留，
不构成 `/oauth2/**` 整段放行。当前没有正式登记的 provider 表单 POST 能力，IAM 对其失败关闭；
不能把任意表单参数保存为 resume 或当成已认证回调。

## 管理 CSRF 在线 RPC

edge `iamRpc` 固定 `address`（host:port）、`serverName`、`serviceTokenFile` 和
`timeoutMillis`（默认 2000，最大 5000）。RPC 复用 `iamHop` 的专用 edge cert/key/CA，
始终 mTLS；`authorization: Bearer <svc:edge JWT>` 来自安装密钥文件，
`auriga-tenant-id` 来自已核对部署租户的实际 cookie 会话，不采信浏览器元数据。
服务 JWT 的签发/轮换和证书安装由部署者完成；edge 不拿实例 Ed25519 身份头密钥伪造服务 JWT。
edge 启动时校验文件，每次 RPC 重新读取固定 `serviceTokenFile`，支持部署者原子替换已续签凭据而无需重启；
文件必须是普通文件且不超过 16 KiB，缺失、空白或非法内容失败关闭。安装侧实际发行与刷新生命周期尚待补齐，
单份短时 JWT 不能作为长期可用装配。

`VerifyAdminSessionCsrf` 使用正式 proto，发送实际消费的 admin cookie 和 token；
每次请求独立调用、短 deadline、不缓存。返回 session 必须与当前 cookie 会话的 sid/account/tid
一致、audience=ADMIN，且绝对/闲置到期均有效。无效会话 401、有效会话但 token 不匹配 403、
超时/错响应/控制面不可用 503。IP admin 永不退回原 `csrfHash` 比较。

本协议不证明 IP2 实例 path/cookie/Location/SW 边界或 IP3 真实浏览器验收完成。

## SPA 最小公开配置

仅门户、管理台 SPA 的 index HTML（含合法本地路由的 index fallback）注入
`<meta name="auriga-access-mode" content="domain|ip">`，值唯一来自已验
`DeploymentAccess.Mode`；不公开整份配置、tenant、内部 alias、PKI。普通静态资产不注入，
index 使用 `Cache-Control: no-store`，不复用另一角色或模式的响应缓存。
源 index 可无该标签，由 edge 添加；可已有一个合法 enum 标签，由 edge 覆盖为安装值；
重复或非法标签失败关闭 503，不输出有歧义的页面。无新公开 API。
前端只读取这一个正式 enum；缺失、重复或无效时管理写失败关闭，不能从 hostname 猜测 IP 模式。
