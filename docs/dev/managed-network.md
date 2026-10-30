# 托管实例接入网络

协议及风险边界见 [设计](../design/managed-network-credential.zh.md) 和
[凭据契约](../../contracts/auth/managed-device.md)。本页描述接线，不代表真实验收通过。

combined 部署由 `WorkspaceManagedBindings` 提供当前实例绑定，IAM 通过本服务公开 API
在线检查，workspace 通过 IAM 的 `ManagedDeviceCredentials` API 签发及吊销。下发在独立
重试循环中进行，不持有 workspace 行锁调用 IAM。停止/删除先改变权威代次，再异步撤销
IAM 旧记录；换取、设备在线检查与网关批量存活检查均核当前代次，不能凭旧 JWT 存活。

部署者提供私密文件 `auriga.workspace.managed-network-installation-file`，内容恰为
`platformOrigin`、`issuer` 和 `network`（沿桌面 ConnectNetwork 的 realm/WSS/根公钥定义）。
该文件只含安装材料，无设备机密。公开 compose 的 HTTPS + collab bootstrap 自动从批准的
collab 根生成 `network-platform/managed-network.json` 并启用配置；文件纳入完整性材料。
旧封存部署不自动补文件，使用公开 prepare 全新生成。镜像须包含本次插件和 agent。
HTTPS DNS 部署中，agent 从同一受信 Access 派生门户别名，将其与控制别名一同挂到已校验的
edge 内部网络端点，供凭据换取和 WSS 访问；仍保持原内部网络和成员校验。旧端点缺少新别名
会失败封闭，需用新栈/新实例网络验证。外部端口转换或 IP 路由需按部署的可达性单独验收。

split 部署还需配置 workspace→IAM 和 IAM→workspace 的 mTLS 证书/key/CA，分别为
`auriga.workspace.iam-client-certificate-file`、`iam-client-key-file`、`iam-ca-file`，以及
`auriga.iam.workspace-client-certificate-file`、`workspace-client-key-file`、`workspace-ca-file`；
目标使用既有 `auriga.remote.iam.target` / `auriga.remote.workspace.target`。common 内部服务
JWT/租户委托须分别授权 svc:workspace、svc:iam，不能使用 edge 或 instance token。

插件使用当前宿主 profile 的私有目录，按 device id 保存同一套网络账本。文件变动或移除
会关闭旧连接及工具，访问令牌到期前自动换取；已证明吊销的材料不会自动恢复。暂时故障
失败封闭并在1–30秒退避后重试。浏览器 consent 和接收控制仍走原 `/auriga/local-login/`
子路由，由 managed 身份门与精确 Origin 检查保护；写操作必须携带匹配 Origin。

真实验证须使用 HTTPS/WSS 和共享栈锁。测试 CA 仅通过测试进程的受信 CA 文件载入，
不改系统信任，不关闭 TLS 校验。健康状态、模块测试和工具出现不能代替严格 READY；
双端互发须另留真实会话证据。
