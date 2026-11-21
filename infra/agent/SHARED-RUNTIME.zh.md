# 共享节点生产装配（待审查、待联合部署）

基线 `9fa7a5272a45978fcb48b233ad01d23b0be8068c`。本批只修改 Agent、W 必要节点接线及公开安装配置。未操作 NAS、真实 PG/DSH、企业凭据或付费模型。

**完整生产闭环仍有外部接口阻塞，不能把此候选标为已经可用。** N 的 send/receive 尚无公开生产入口；资源服务只有 Java `artifact(binding, leaseId, artifactId)`，当前联合头没有 Node 可调用的下载服务。已向主控报告，未越界修改 N、IAM、Edge、Host 或资源权威。下面资源 HTTP 路径是供另一作者接线的提案，不能宣称为已批准的共有契约。

## 已装配的路径

- NodeControl 的 quota、资源验证、容器准备通过每 Session 对象唯一 worker 执行，最多 64 个排队项；单项两分钟超时。重复完整消息只排一次。断线取消上下文并清除旧待办，旧收据带连接上下文，不会投递到新连接。应用退出有界等待 worker；普通 Apply/Delete/Command 继续使用原调度路径。
- W 在既有节点会话租约、mTLS 和租户事务内分配池节点、重发 pending/ready 成员的实际 ResourceView、接受所属节点的 ResourceReport/GuardianImpact；后者验证全池 binding 并 fence 会话。生产池要求 prepare-v1 与 persistent-v1 两个既有能力声明，目录准备节点不能抢占。持久回执支持重传、状态更新和停止证据不回退。W 的 stop-pool 意图也通过原 NodeControl 发送并确认。
- 共享池投影进入既有 NATS `auriga-routes` KV，使用原 CAS 防止旧写覆盖新状态。池没有个人 owner；路由使用池自身作为受管 subject，入口权限仍由实时 W placement 决定。pending 路由可通固定 profile 探测，普通入口仍要求 ready。
- Node Ingress 安装 W 实时 GetPool/Check/Watch 与独立 bbolt 重放账本。注册权威不可用、或与本机进程类型不一致时拒绝访问；即使旧 registry 把实例标成个人实例也不能回退 owner/file/log 路径。`shared` 与显式 `dedicated` 走同一 placement 权威，精确 mode 进入不可变配置。
- 固定 `GET /i/<pool>/_auriga/shared-profile` 在 Edge mTLS 校验后、普通身份检查前，仅访问已登记池的真实 Host。拒绝查询、请求体、Cookie、Authorization、Upgrade；只向 Host 发送固定 Accept/Accept-Encoding，限制响应 16384 字节。W/Edge 已有的十秒专用探测凭证及 Host 真 profile 检查保持原实现。
- Node 创建池专用 Unix HTTP listener。宿主路径 `<DataRoot>/pools/<pool>/control/host-relay` 只放公开 Host 配置和 socket，以只读目录挂到容器 `/var/lib/auriga/pools/<pool>/control`；这样 Agent 重启重建 socket inode 后容器仍能连接。用持有目录 fd 的 `/proc/self/fd` 短路径绑定真实 inode，避免长 NAS 数据根超过 sockaddr_un 限制。目录不含 Node 账本或服务令牌。Linux `SO_PEERCRED` 必须等于该池 UID，socket 为 0600。
- 固定 POST `/_auriga/shared-rpc/<operation>` 调 W/IAM 真 gRPC；拒绝重复 JSON 键、未知顶层字段、超深/超大 JSON。Host JSON 使用 safe integer 和 `shared`/`dedicated`，不会直接泄漏 protojson 的 int64 字符串和枚举名。
- Begin/Issue/Finish 绑定池、W 登记会话和本机任务登记；capability 只接受本机从 IAM 收到的令牌映射。每次使用实时检查 W/IAM，旧版本直接 authorize 拒绝；同一运行任务可重新 Issue。取消 Begin 时使用独立上下文发送 Finish 墓碑补偿。企业材料只沿单任务请求返回，不写进程全局环境。
- `sharedModel` 使用独立 CA 和节点证书，仅 POST `/internal/shared/model/invoke`，发送已定四个单值头，稳定 callId 作 Idempotency-Key，body 为 OpenAI chat JSON。N SSE 转为公开 DSH block/text/tool/usage/finish NDJSON；取消关闭上游，不自动重放。
- PoolMaterializer 从完整 typed snapshot 验证每个成员绑定、签名引用、lease、撤销表、原始摘要与发布/安装水位，复用 UnifiedProtocol、UnifiedManager 的签名检查和安全技能归档安装器。共同内容相同即可，各成员 lease 分别验证。物化到只读 active 根；已安装字节被修改会触发 guardian。续租不改变已安装内容，高水位在清理后保留。

## 安装材料与产品命令

Agent 镜像已包含 e2fsprogs、util-linux。继续使用 `deploy/nas/shared-runtime-compose.yml` 的 ext4 文件镜像路径，不修改 NAS 原盘 mount，不要求原盘 prjquota/XFS。每池镜像在 Agent 和 Docker daemon 各自 namespace 挂载同一 loop device，成员数据用 Docker volume；公开配置和只读资源根在镜像外。

安装方需提供已签 runtime.json、完整展开 profile.yml、资源信任材料，以及既有 IAM/Edge 的**用途公钥**。profile 中 `auriga-dsh/shared-ports` 必须先于 `auriga-dsh/shared`，skill filesystem 只用 `/run/auriga/skills-active/current`，关闭默认根。生产不再接收手写 ports.mjs。

通过 Agent 产品命令把公开 JWKS 转成 Host 所需 SPKI 公钥配置，无需手抄模块或私钥：

```sh
auriga-agent render-shared-host-config \
  --identity-keys /install/identity-keys.json \
  --enterprise-keys /install/shared-enterprise-public.json \
  --model-keys /install/shared-model-public.json \
  --message-keys /install/shared-message-public.json \
  --enterprise-config /install/shared-enterprise-policy.json \
  --model '<安装授权模型 ID>' \
  --output /install/host.json
```

输入都是本来就需要分发的公开配置，公钥文件为 `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"…","x":"…"}]}`；工具拒绝私钥字段与跨用途复用。企业策略只含 allowRawAgentToken、outboundAllowedHosts、empNoHeader、authValueHeader。输出文件必须新建，不能覆盖运行中的配置。

按 `shared-runtime-agent.example.json`、`shared-runtime-services.example.json` 接入安装方真实服务地址及 CA 文件。示例 UUID/域名只是合成占位值。`agent-service.jwt` 为既有 `svc:agent` 内部令牌文件，Linux 必须 regular、0600；客户端每次调用重新读取以支持文件轮换。IAM 私钥留在 IAM。Node 的节点证书来自既有 enrollment，N 依据 `auriga://node/<UUID>`、专用 CA 和 allowedNodeIds 校验；N tenant 取其配置，Node 不给模型 body 填身份。

主控在依赖接口接通、审查通过后按已有产品 compose 启用 overlay，先执行 compose config，再串行部署 Agent。工人没有执行这些部署命令。Host health 只列实际装配操作；当前缺消息 transport 时不会伪称 send/receive 可用，Host 将拒绝提供共享服务。

## 待主控协调的接口

1. **N 消息**：需要实际 send/receive 传输、消息目录与 typed-local/source_binding/producer_epoch 映射。Node 已有用途、任务、会话、messageId 校验和有限操作 dispatch，但没有虚构 transport 或目标授权。此处仍须 N 作者给真实入口，再安装到 Relay.Message。
2. **资源下载**：Node 客户端提案为独立 mTLS `POST /internal/shared/resource/artifact`，内部 `svc:agent` Bearer JWT、单值 `Auriga-Tenant-Id`，JSON `{binding,leaseId,artifactId}`；manifest 时 artifactId 为 null，其他为已签引用的 artifact ID。返回原始字节、可信 Date、200，禁止跳转与范围响应。资源侧必须每次调用既有权威 `artifact(binding,leaseId,artifactId)`，复核当前 binding/授权/发布/撤销/lease；不能复用个人 nodeDownload。**尚未得到该端口确认，联合头中无服务端实现。** 下载不可用或校验失败会拒绝启动，不替换为空视图。

上述两项未接通前，只能报告“本批代码候选已实现、完整闭环受接口阻塞、待独审/联合部署”。实际配额写满、正常门户、两成员同 PID 常驻、mTLS 跨服务和恢复验收仍由主控在独占槽完成。
