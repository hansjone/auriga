# Network 部署材料维护

`Generate` 只接受新目录或空的真实目录。失败时仅清理本次创建的文件；已有空目录保留原 inode，以兼容 NAS 安装器持有的目录句柄。清理失败返回 `cleanup_failed`，须在重试前检查隔离目录。已有非空目录、符号链接和安装材料一律不覆盖。

`network-materialize OUTPUT OPTIONS.json` 失败输出固定 `category`：`usage`（退出 2）、`options_read_failed`、`options_invalid_json`、`invalid_options`、`directory_refused`、`generation_failed`、`cleanup_failed`（其余退出 1）。错误不输出参数、路径或材料内容。

## 权限与挂载

- platform 只按文件只读挂载 `platform.properties`、`gateway.secret`。
- network-broker 只按文件只读挂载 `broker.conf`、`broker.pem`、`broker.key`、`server-ca.pem`。
- network 使用完整目录，因为运行时校验 broker 配置、读取账户凭据、签名种子、提交端 TLS 及健康探针的客户端证书。
- 所有源文件继续列入原安装回执；按文件挂载不改变生成路径、配置中的容器路径或回执列表。NAS 保持源目录 uid/gid10001、0700，文件 0600，由现有元数据守卫逐项验证。
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
