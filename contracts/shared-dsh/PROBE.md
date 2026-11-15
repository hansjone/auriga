# 候选分配正常入口探测协议 v1

K3 返工1补充 W、E、节点 ingress 与 P 的共同载体。机器结构在 `probe.schema.json`，契约 oracle 和真实 Ed25519 正负例在 `probe-ports.mjs`、`probe.test.mjs`。

## 请求签名与当前操作

W 在 committing、placement 为 pending/switching，且必要收据及资源准备完成后，为当前候选完整 Binding 生成32字节随机挑战，编码为43字符无 padding 的规范 base64url。W 将 challenge、请求 jti、候选 Binding、platformSessionId 与 operationId 一起持久化到当前探测尝试。每次重试生成新的 challenge 和 jti，并原子替换当前尝试。各跳按 operationId 查询 W 的受信内部操作权威，核对上述字段、当前阶段及成员/平台会话有效性。查询权限只授予参与本次探测的服务身份，权威不可用返回503。

W 使用专用 Ed25519 探测密钥签紧凑 JWS，header 为 `ProbeHeader`，typ=`auriga-shared-probe+jwt`，claims 为 `ProbeRequest`，issuer=`auriga.workspace`，audience=`auriga.shared.probe.chain`。服务配置按 kid 取得专用公钥，并核对签名用途。Canonical JSON、签名输入、base64url、重复键和未知字段的处理沿 shared-dsh README。请求 method 固定 GET，canonicalPath 固定 `/`。issuedAt/expiresAt 为 UTC 秒安全整数，`issuedAt <= now < expiresAt` 且有效窗口最大60秒。W 的普通票据与 E 身份密钥继续独立配置。

操作权威检查走 W 内部监听面 `POST /internal/placement-probes/check`，沿现有内部服务 mTLS/auriga.internal 认证，body 同为 `ProbeDispatch`。W 验自己的专用探测签名后，在成员及操作锁内核对持久化当前尝试和以上全部前提，成功返回204空 body、`Cache-Control: no-store`。edge 与承载该候选的节点 ingress 获权检查，其他主体403。P 的受信宿主适配器通过已认证的本机 ingress 控制接缝调用，由 ingress 代表该宿主向 W 检查，且须匹配所在节点和实例，普通会话不能调用该接缝。各跳每次准入及 P 页面完成前重新检查，不缓存204。检查不消费探测 jti，消费由随后处理请求的各跳完成。失败沿下文公共 Problem。oracle 的 operation 参数由这个权威结果提供，业务请求不能自填。

## W→E 调度

W 经 E 的内部监听面以 HTTPS mTLS 调用 `POST /_auriga/internal/placement-probes`，Content-Type 为 application/json，body 为 `ProbeDispatch`，即单个 `probe` 字段携带上述 JWS。该路径只在内部监听面注册，peer 必须经现有服务身份认证映射为 workspace，受信的 auriga.internal 服务凭据仍按部署认证规则核验。公网、portal/admin 会话和普通节点主体访问返回403。

E 校验签名、期限及当前操作后，原子消费本跳的请求 jti。E 从受信部署配置和候选物理实例查正常页面入口与所在节点，核对租户、physicalInstanceId 和候选路由，禁止使用请求提供的 URL/Host。domain 模式选择该实例的正常 `/`，IP 模式选择 `/i/<physicalInstanceId>/`，使用正常页面转发与响应映射流程。E 清空本次内部请求的 Cookie、Authorization、Auriga-Identity 及外部转发身份，按本协议建立受限的候选探测上下文。

## E→ingress→P 转发

E 通过既有到目标节点的 mTLS ingress 通道发送 GET `/i/<physicalInstanceId>/`，带单值 `Auriga-Placement-Probe`，值为 W 原 JWS。ingress 核 mTLS peer 为获准 edge、W 专用签名、当前操作和自己的节点调度归属，核对路径内实例与签名 Binding。然后消费 ingress 本跳的 jti，并通过该物理实例现有的受信本机转发通道，把同一个头及去前缀 GET `/` 交 P。支持的本机通道为受 ACL 保护的 Unix socket/命名管道或由节点代理控制且普通请求不能直达的实例私有监听面。P 仅在该已认证 ingress 来源接受探测头。部署不能证明这一来源时返回503。

所有普通外部入口均大小写不敏感地清除 `Auriga-Placement-Probe` 与 `Auriga-Placement-Probe-Receipt`。多头、逗号拼接、CR/LF、query 和 Cookie 中提供的探测材料均拒绝。重定向不跟随。各跳校验通过后只允许本操作候选 Binding 的 GET `/`，其余 HTTP 方法、路径、WS 升级及业务动作返回403。各跳消费存储按本跳用途+issuer+jti 原子持久化到 expiresAt，重放409，存储不可用503。E/ingress/P 各自拥有消费记录，节点转发不会复用上跳记录而误拒合法首次请求。

P 重核当前操作、成员与平台会话，消费 host 本跳的 jti，将候选上下文交正常页面 handler。此上下文只授予只读根页面处理，页面处理完成前必须复核操作和会话。任何创建会话、企业材料、模型、消息、工具或任务执行请求均拒绝。普通身份与票据路径仍执行 ready 准入检查。

## P→W 回执

仅当正常页面 handler 完成、状态200、媒体类型解析为 text/html 且无 Set-Cookie、无重定向时，P 计算页面实体原始字节的 SHA-256。探测链固定 `Accept-Encoding: identity`，中间层保持实体字节，Transfer-Encoding 不属于实体内容。P 将摘要、method、canonicalPath、statusCode、contentType、业务观察秒数，以及原 operationId、完整 Binding、platformSessionId、challenge、requestJti 写入 `ProbeReceipt`。回执使用新的 jti，observedAt 不早于请求 issuedAt，且 `observedAt <= issuedAt <= now < expiresAt <= 请求expiresAt`。

P 以该物理实例专用的 Ed25519 探测回执密钥签名，typ=`auriga-shared-probe-receipt+jwt`，issuer=`auriga.shared.host`，audience=`auriga.workspace.probe-result`。公钥由受信部署控制面按 tenant+physicalInstanceId 配置给 W，W 不从未验签 claims 或请求头选择信任集合。回执密钥只挂给受信页面探测适配器。

P 把紧凑 JWS 放在单值响应头 `Auriga-Placement-Probe-Receipt`，沿原响应链返回。ingress/E 将该头限定在本次受信内部请求上，普通响应清除该头。E 校验正常页面状态、媒体类型、无 Set-Cookie，并独立计算收到的实体摘要。E 返回 W 的调度响应为200、application/json、`Cache-Control: no-store`，body 为 `ProbeResult`，包含 receipt JWS 和 E 实测 contentSha256。错误统一沿公共 Problem，400为结构错误，401为签名/期限错误，403为来源/绑定/授权错误或当前尝试失效，409为重放，503为权威、通道或消费存储不可用。各内部端点对错误方法返回405，并附 Allow。错误与成功均不记录或回显签名材料。

W 验回执专用签名、用途、时间、requestJti、挑战、全部绑定以及 P 摘要与 E 摘要相等。随后在成员/操作锁内再次确认持久化的当前探测尝试、候选绑定、收据、租约及有效会话，并原子消费回执 jti、CAS ready、记录 succeeded 与 outbox。锁内任一条件变化则拒绝提交。未收到有效回执、超时或重启后均重新发起新尝试。旧请求及回执因 jti/challenge 与当前尝试不匹配而失效。

## 交接

此批提供跨线协议、严格 schema 与替身测试。W/E/ingress/P 按上述来源认证、头处理及正常页面处理接线后，由主控从正常入口联合验收。
