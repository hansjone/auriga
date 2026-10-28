# Network 部署材料维护

`Generate` 只接受新目录或空的真实目录。失败时仅清理本次创建的文件；已有空目录保留原 inode，以兼容 NAS 安装器持有的目录句柄。清理失败返回 `cleanup_failed`，须在重试前检查隔离目录。已有非空目录、符号链接和安装材料一律不覆盖。

`Options.WebsocketHost` 独立控制 broker WSS 监听地址，省略时兼容 `BrokerHost`。compose 明确指定固定 Docker 网桥网关，保留 NATS TCP 回环监听；NAS 使用隔离 internal network 的容器监听，不发布宿主端口。WSS 始终使用 broker TLS 叶子证书，edge 固定校验 `broker.network.internal` 与独立 `server-ca.pem`，不使用明文上游或跳过证书验证。

两套 edge 配置均启用精确 `/net/v1/ws`，`allowedOrigins` 只包含已验证 `PortalURL()` 的完整 Origin（含非默认端口），不加入管理台、不追加路径、不推断 HTTPS。edge 复用现有只读公开 CA：compose 的 `network-edge/server-ca.pem` 或 NAS 的 `pki-edge/network-server-ca.pem`；不获得 broker 私钥、密码或平台凭据。submit 使用同一公开 server CA，固定 SAN 分别为 `submit.network.internal` 与 `broker.network.internal`。

compose 回执输入版本为 `network-wss-v1-platform-dir`，NAS 为 `network-wss-v1`（含无正式 runtime 模板模式）。旧回执重入失败且不修复材料，不会静默保留缺少 WSS 的旧配置；新安装重入仍验证全部封存文件与证书有效期。保留旧安装与备份，在新的专用测试目录重新生成，勿手改配置或回执。

内部 WSS 接线和 submit 健康就绪不能替代设备正式入网验收。插件只接受外部 `wss://`，HTTP profile 不自动升级；TLS server CA 仅用于传输认证，不能作为插件 Ed25519 安装信任根。realm 必须来自 IAM 分配，安装信任根必须来自真实签名链。外部 HTTPS 支持边界见 [M2](../../../docs/dev/m2.md) 与 [NAS](../../../docs/dev/nas.md)。

部署包测试 `TestWebsocketListenerIsolation` 可通过 `AURIGA_NETWORK_BROKER_TEST_BINARY` 指向从 `infra/ai-gateway` 的锁定依赖构建的 `github.com/nats-io/nats-server/v2` 二进制；未指定时具名跳过。它仅启动随机回环端口的 broker 子进程，验证独立 TCP/WSS 地址、正确 SAN/CA 可达、错误 SAN/CA 拒绝及反向接口不可达，结束时清理进程。此探针不运行 IAM callout，不宣称插件默认 factory 已传正确 Origin 或正式设备已入网；这些需要独立全链正控。

`network-materialize OUTPUT OPTIONS.json` 失败输出固定 `category`：`usage`（退出 2）、`options_read_failed`、`options_invalid_json`、`invalid_options`、`directory_refused`、`generation_failed`、`cleanup_failed`（其余退出 1）。错误不输出参数、路径或材料内容。

## 权限与挂载

- platform 只读挂载独立 `network-platform` 目录（仅 `platform.properties`、`gateway.secret`），源目录保持生成者 UID、0700，文件0600；容器挂载路径仍为 `/etc/auriga/network`。生成的 `installation-owner` 与目录真实 UID 一致，不依赖 Docker 为逐文件挂载创建父目录。
- network-broker 只按文件只读挂载 `broker.conf`、`broker.pem`、`broker.key`、`server-ca.pem`。
- network 使用完整目录，因为运行时校验 broker 配置、读取账户凭据、签名种子、提交端 TLS 及健康探针的客户端证书。
- 所有源文件继续列入安装回执；两平台凭据改列 `network-platform/`，运行时材料仍列 `network/`。更换挂载方式不改变配置中的容器路径。NAS 保持 network 与 network-platform 源目录 uid/gid10001、0700，文件 0600，由现有元数据守卫逐项验证。
- compose 的 network/broker 使用 uid/gid10001、`cap_drop: [ALL]`、只读根文件系统及 `no-new-privileges`。broker 未启用 JetStream，runtime 状态在内存，无需写入路径。prepare 保留材料的安装者所有权，只给 gid10001 目录遍历与运行时文件 0640 读取权限；平台凭据仍为 0600。network 额外加入 gid65533 读取动态服务令牌，令牌卷只读挂载。

文件 bind mount 固定 inode，替换源文件后必须重新创建相关容器。不得以修改回执、复用旧私钥或直接删除线上源目录的方式续期。

## 证书到期告警

独立 network server/client CA 与 submit、edge、broker、broker-client 叶子证书均有效一年。到期前 **30 天（含边界）**输出 `network_certificate_expiring`，带固定文件名及 UTC `not_after`。已过期输出 `network_certificate_expired`，缺失、损坏或尚未生效输出 `network_certificate_invalid`，检查退出 1；正常和临近到期退出 0。检查不修改任何证书或私钥，也不自动续期。

启动时重新执行 compose prepare 或 NAS start/render 会在验证原安装回执后检查证书；network 容器健康探针每 3 秒重复检查，检查失败使容器 unhealthy。首次安装的材料是新签发的；后续直接 `compose up` 也会由探针检查。

运行中的状态检查（在各自 compose 目录，带正常的 env 文件参数）：

```sh
docker compose --env-file var/compose.env exec -T network \
  /auriga-network-probe --certificates /etc/auriga/network
# NAS：sh deploy/nas/scripts/status.sh 也执行上述证书检查。
```

监控须每天采集该命令输出并对 `network_certificate_expiring` / `expired` / `invalid` 告警。证书状态来自实际挂载材料；容器停止时 NAS status 显示 `unavailable_or_invalid`，不能视为证书正常。Docker healthcheck 结果也可从 `docker inspect` 的 `State.Health.Log` 读取。临近到期仍可服务，运维应在窗口内安排整套 network 信任与密钥更换、保留离线旧材料及回退方案；本次仅提供告警，不提供在线轮换或滚动双信任切换。普通重入始终保留旧私钥。
