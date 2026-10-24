# Auriga 可靠通信网络设计

状态：设计建议，尚未批准、实施或压测。核实日期：2026-10-24—25。本文与 [协议](protocol.zh.md)、[故障与容量验收](failure-validation.zh.md) 一起评审。文中的新接口、字段、错误码是待写入 `contracts/` 的提案，不能当成现有 API。

## 一页决策摘要

**要解决的问题**：一个 DSH 内会有许多主会话和子会话。它们需要共享可靠通信，但各自授权；不能每个 agent 维护连接，也不能让总部模型会话承担交换守护进程。

**推荐**：NATS Server（Go 消息交换服务）与官方客户端维护每 DSH 一条 WSS 长连接；PostgreSQL 保存唯一业务真相。平台提交后推送“邮箱有更新”，插件按容量获取消息页，宿主持久接受后回执。会话和子树的授权由 Auriga 负责。总部 DSH 与其他 DSH 一样是执行端。

```mermaid
flowchart LR
  A[多会话 DSH A / 插件] <-->|一条 NATS WSS| N[NATS 原生交换]
  N <-->|一条 NATS WSS| B[多会话 DSH B / 插件]
  N <--> G[Go 授权与邮箱适配器]
  G <--> P[collab / PG 唯一消息真相]
```

**为何先不选其他方案**：Tinode 能省下 IM 归档和 topic 功能，但仍需验证 DSH 内逐会话授权、独立回执及数据迁移，保留为第二候选；OpenIM 的完整 IM 栈带来较多新依赖；MQTT 5 的 QoS 不能替代宿主持久收件，且需要引入 Broker；XMPP 需要额外运行栈和账户映射；Matrix 的房间/联邦模型超出当前需求。详细证据与反选门禁见 §4，不以已有投入排除替代方案。

**主要风险**：真正的执行恢复难点在宿主，网络送达不等于执行完成；IAM 隔离、客户端硬缓冲上限和吊销映射还未验收；PG 留存成本随消息率增长，需限额和正文留存策略。提示丢失允许延迟修复，但不能丢失已提交消息。

**待用户决定的三件事（均有建议默认）**：

1. 首发部署档：建议先验收可恢复单机档，生产有可用性要求再选多故障域 HA 档；单机不承诺 HA。
2. 留存：建议中档采用已接受正文 24 小时、待投消息最多 7 天、执行请求 24 小时、去重/终态元数据至少 30 天，均受容量上限约束；需长期历史时优先授权对象存储。
3. 首次联系是否自动执行：建议默认仅进入受控收件箱，须显式授予自动唤醒/新建会话权限才启动模型。

当前仅供研究与独立审查。下述分期不是实施授权；继续开发、spike 或真实模型验收均等待后续指示。

## 1. 推荐结论与范围

推荐复用 **NATS Server 原生 Go 交换设施及 NATS over WSS 协议 + 官方 JS/Go 客户端 + 已有 PostgreSQL collab 耐久核心**。JetStream（NATS 的持久流与消费者扩展）继续负责既有内部耐久事件，不进入本网络的首发投递路径；默认每 DSH 一条 NATS 连接，以少量固定上下行 subject（NATS 的路由主题） 承载全部有权会话。保留必要的 Auriga 应用信封与 Host receipt，不自写 WebSocket 协议、PING/PONG、订阅交换、集群路由或 Broker。完整 IM 候选以 Tinode 为第二选择，OpenIM 为第三选择，取舍和验证门禁见第 4 节。

这不是因为现有库里有 NATS 就直接采用：对照了 MQTT 5、XMPP/归档、Matrix、Tinode/OpenIM 的原始文档后，NATS 最符合“Go、单机到集群、复用当前 PG/IAM、少加运行组件”的组合。若 IAM 隔离接入 spike 不能证明安全或所需维护改造超过 Tinode 的实测代价，必须回到候选门禁，不以自造完整传输协议兜底。Agent 直接使用工具，Node 适配器管理连接凭据、持久收件与恢复。

总部平台是交换设施，独立总部 DSH 是 LLM 执行入口。管理员只是进入总部 DSH 的操作者；网络不能依赖某个管理员在线、某个会话活着或模型是否响应。这里的“网络”是应用消息网络，不是 VPN，不转发任意 IP 包、端口或文件系统访问。

文中 epoch 指连接世代，用来拒绝旧连接的新操作；cursor 指已持久确认的连续邮箱位置，用来断线恢复。receipt 是宿主回执；ledger 是本地持久收件与回执账本。它们均不等于模型执行完成。

必须同时成立的五条不变量：

1. `platform_stored` 只在消息、收件投递、邮箱序号、幂等记录及既有业务事件 outbox 同一 PostgreSQL 事务提交之后返回。网络写成功不算耐久保存。
2. 投递至少一次；宿主以稳定 `delivery_id` 去重。`host_accepted` 必须有经过核验的宿主持久收件证据，不代表执行完成，更不承诺任意外部副作用恰好一次。
3. 每次源操作、取页、回执和执行准入都核身份和权限；会话树关系不自动授予权限。连接在线不是授权依据。
4. 正常链路是提交触发定向提示、客户端按窗口取页；补偿扫描是低频、批量的服务级修复，不是每 DSH、每会话的短轮询。
5. 所有队列、重试、缓冲、订阅、并发与离线保留都有上限；拒绝、未知与过期可查询、可审计，不伪造成功。

## 2. 哪些是需求，哪些是证据

**当前用户确认**：联网主体是插件/DSH，用户可有多个 DSH，每个 DSH 可同时有多个主会话及递归子会话；获授权的会话共用稳定网络；总部交换层优先 Go；优先比较成熟协议，WSS 仅为候选传输；保留 Codex 原生协作式能力目标，任务历史由 DSH 承载，不再造管理台 Task/AgentBus 编排引擎；Team MCP 是远程工具服务。

历史 Team/Hub 的长期连接、先存后投和离线恢复支持需求方向；旧账号模型和业务编排不继承。锁定候选、真实首失、运行时原型及其限制集中见[验收证据 §1](failure-validation.zh.md#1-已有证据与明确未验证项)。候选能力不等于 main 已实现。

## 3. 拓扑与服务职责

```mermaid
flowchart LR
  U[管理员] --> HQ[独立总部 DSH]
  A[普通 DSH / 多会话子树] --> P[Node 插件 / 持久收件]
  HQ --> P
  P <-->|每实例一条 WSS| E[edge / TLS / 限流]
  E <--> N[NATS Server / Core 原生路由]
  N <--> G[Go adapter / 无状态请求处理]
  P -->|共享 HTTP 池| M[远程 Team MCP]
  G <-->|内部 gRPC| C[collab / 授权与邮箱]
  M --> C
  C --> PG[(PG / 消息 / 序号 / 回执 / 当前连接)]
  C --> I[IAM / org]
  C -->|提交后 Core 邮箱提示| N
  C -->|低频批量修复提示| N
```

长连接、分帧、心跳、订阅匹配、客户端慢消费检测和跨 Broker 路由由独立 nats-server 进程承担。Auriga Go adapter 首发可位于 `infra/ai-gateway` 独立模块，负责 auth callout（Broker 在连接时向权威服务请求鉴权）、应用授权、取页与回执，共用安装运维但隔离队列与熔断器。它不承载模型推理，不为每个逻辑会话创建 goroutine/定时器/DB 连接。压测显示模型代理与 adapter 相互干扰时拆独立 Go 进程；不要把 NATS Server 嵌进模型代理主进程来省一个进程。

collab 拥有通信真相和 RLS，不让 Go 层跨 schema 直读业务表。Broker 保有 socket/subscription；adapter 内存仅保存有界请求队列和可丢缓存，不拥有投递进度。总部 DSH 不比其他执行端天然拥有更多消息读取权限；它的系统主体能力由 org/策略独立赋予。

节点代理（每宿主机）原有 workspace 双向 gRPC/mTLS 管运行生命周期；本网络的“DSH 节点”是逻辑执行实例，不复用宿主机 agent 证书冒充 DSH 身份。云端 DSH 进程已停止时，授权唤醒请求交 workspace；用户电脑离线时只能积压或提示，平台不能凭消息承诺唤醒关机电脑。

## 4. 成熟协议与可改造开源实现

### 4.1 协议本身解决到哪一层

| 协议 | 复用价值 | 对本任务仍缺什么 |
|---|---|---|
| NATS Core + JetStream，外部 WSS | 一连接多 subject，服务器原生路由、queue group、accounts/subject ACL；JetStream 有持久流与消费者。既有项目已用 Go Server/SDK。 | Core 在线消息最多一次，JS ACK 也不等于 DSH 持久入箱。会话树、thread权限、Host receipt、执行未知边界仍在 Auriga；不把全部外部 DSH 直接开放到内部 JS 管理/API。 |
| MQTT 5 | 一 DSH 一个 ClientID、多 topic；Session Expiry/Clean Start、QoS1/2、Message Expiry、Receive Maximum、Maximum Packet Size 与响应关联适合设备消息。 | QoS2 是协议端点交付，不是任意 Agent 副作用恰好一次；持久 session 具体落盘/HA取决于 broker。topic ACL不能表达全部会话树/线程权限；连接接管也不替代执行fence。需另引 broker 或维护当前栈不存在的实现。 |
| XMPP + RFC7395 + XEP0198 + XEP0313 | 成熟 IM 身份/路由、WSS绑定、stanza确认与流恢复、MAM归档。一个 DSH JID/resource 可复用多 thread/MUC。 | 0198 ACK 是 handled stanza，不是宿主落盘执行；恢复窗口与持久归档是不同机制。不要每子agent一个JID连接；子树委派需插件扩展。成熟服务端主要 Erlang/Lua，不满足Go优先，需额外XMPP用户/归档权限映射。 |
| Matrix | room/event、持久历史、成员关系、设备、transaction ID、增量sync已有标准，适合完整协作聊天和联邦。 | room权限不等递归会话能力；标准客户端主要HTTP sync/长等待，不是本任务一条WS交换协议。长等待不是短poll，但新增homeserver/状态解析/账户及历史模型负担大；不需要联邦时收益有限。 |

依据：[MQTT 5 OASIS 标准](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html)、[XMPP WebSocket RFC7395](https://www.rfc-editor.org/rfc/rfc7395.html)、[XEP-0198](https://xmpp.org/extensions/xep-0198.html)、[XEP-0313](https://xmpp.org/extensions/xep-0313.html)、[Matrix Client-Server](https://spec.matrix.org/latest/client-server-api/)。所有方案都可以承载业务信封，但不能因“像聊天软件”就把人类用户、客户端连接、DSH会话混为同一个身份。

### 4.2 实际实现、许可证与最小改造路径

许可证按官方源文件核验；GPLv3 §13 明确允许与 AGPLv3 作品结合，故 Tinode 与本项目按对应义务开源发行不存在这项兼容障碍。商业双授权须另核上游授权，不能把 GPL 代码直接改发自有商业许可。不会把项目自己的商业授权自动扩到上游代码。

| 实现 | 已核事实 | 复用路径、成本与判断 |
|---|---|---|
| **NATS Server / JetStream** | Go；当前仓库锁 `nats-server/v2 v2.12.6`、`nats.go v1.49.0`；该 Server tag 为 Apache-2.0。官方 JS 客户端现统一 nats.js v3 模块，支持 Node/WebSocket。 | **首选**：启用受隔离 WSS listener/account，复用服务端与 SDK，只写 IAM auth callout 及应用适配。不 fork Broker。已有 PG 保存业务，Core 只通知邮箱变化；既有 JetStream 事件不构成收件依赖，不为每会话建立消费者。 |
| **Tinode** | Go 后端，WS JSON 或 gRPC protobuf；单网络 session 可多 topic；PG 可选；server GPL-3.0，客户端 Apache-2.0；上游自称 beta 质量。 | **第二候选，真正 IM 套件**：做 Auriga auth backend 和 DSH adapter，复用 topic、消息归档与收发状态。难点是现有 collab 与 Tinode 消息库选唯一权威、账号模型与 logical binding 隔离；不能每 agent 一账号/WS；GPLv3 §13 允许与 AGPLv3 结合发行，限制的是未经另获授权的商业双授权，不是开源集成。 |
| **OpenIMServer** | Go、Apache-2.0、SDK/网关/RPC/群组/会话及回调；官方 compose 含 MongoDB、Redis、etcd、Kafka 及对象存储。 | **第三候选，完整 IM 需求优先时**：SDK 映射 DSH 节点，业务回调接 IAM/org、增 Host receipt。不是换几个 URL 就复用当前 PG；当前轻量 Windows/单机与既有持久核心下新增组件和双数据模型成本较高。上游规模宣传不当本项目压测。 |
| Mochi MQTT v2 | Go/MIT、MQTT5、TCP/WS、认证/ACL 及多种存储 hook；README 把 cluster 列入 roadmap。 | 标准 MQTT 备选可嵌/独立跑，单机试验小；不能由支持 MQTT 推导已有跨节点持久会话 HA。会话树与 Host ACK 仍自建，选它需承担集群路线。 |
| ejabberd / Prosody | ejabberd Erlang，官方 COPYING 为 GPLv2 文本及 OpenSSL 例外；有 stream management/MAM 及集群文档。Prosody Lua/MIT。 | 若开放 XMPP 互通成为核心需求，成熟度有价值；本任务 Go 优先和现有 IAM/PG 模型下需额外 runtime 与账户/归档改造。各实现恢复持久性及集群功能必须按配置核验。 |
| Matrix Synapse | 当前官方有 AGPL-3.0 许可证文件；主要非 Go homeserver。 | 作为完整房间/联邦协作产品备选，不作为轻量 Agent 内部总线首发；Go SDK≠Go 服务端，也不能拿未核 Go 实现替代成熟度证明。 |
| Centrifugo / centrifuge | Go 实时多路通道；Centrifugo 核心 Apache-2.0，部分能力另分产品；官方 history/recovery 要求 epoch/窗口吻合。 | 适合前端实时推送，短期 history 并非长期可靠邮箱；仍需 PG 与应用 Host ACK。相对已有 NATS 又多一种协议/组件，当前不优于首选。 |
| EMQX 当前 master | LICENSE 标明 5.9.0 及以后为 BSL1.1，有生产/托管/嵌入限制及未来 Change License。 | 不按旧 Apache 印象列成无条件可改造开源发行依赖；本任务不采购许可，也不为避限制锁旧版。 |

原始证据：[NATS v2.12.6 LICENSE](https://raw.githubusercontent.com/nats-io/nats-server/v2.12.6/LICENSE)、[NATS WSS](https://docs.nats.io/learn/websocket/)、[nats.js](https://github.com/nats-io/nats.js)、[Tinode README](https://github.com/tinode/chat)、[Tinode API](https://github.com/tinode/chat/blob/master/docs/API.md)、[Tinode PG 安装](https://github.com/tinode/chat/blob/master/INSTALL.md)、[Tinode LICENSE](https://github.com/tinode/chat/blob/master/LICENSE)、[OpenIM README](https://github.com/openimsdk/open-im-server)、[OpenIM 部署清单](https://github.com/openimsdk/open-im-server/blob/main/docker-compose.yml)、[OpenIM LICENSE](https://github.com/openimsdk/open-im-server/blob/main/LICENSE)、[Mochi README](https://github.com/mochi-mqtt/server)、[Mochi LICENSE](https://github.com/mochi-mqtt/server/blob/main/LICENSE.md)、[ejabberd 模块](https://docs.ejabberd.im/admin/configuration/modules/)、[ejabberd COPYING](https://github.com/processone/ejabberd/blob/master/COPYING)、[ejabberd 集群](https://docs.ejabberd.im/admin/guide/clustering/)、[Prosody 许可](https://prosody.im/source/start)、[Synapse 许可](https://github.com/element-hq/synapse/blob/develop/LICENSE-AGPL-3.0)、[Centrifugo 恢复边界](https://centrifugal.dev/docs/server/history_and_recovery)、[Centrifugo 许可](https://github.com/centrifugal/centrifugo/blob/master/LICENSE)、[EMQX 当前许可](https://github.com/emqx/emqx/blob/master/LICENSE)。mutable 分支在实施 spike 时必须记录精确 commit，不将网页当前状态当永久承诺。

### 4.3 三个候选的硬验证门禁

NATS 优先做小型隔离验证：固定 v2.12.6、两个 Broker/官方客户端、随机测试身份；auth callout 映射精确上下行 subject，拒跨 tenant、跨 ingress/全局通配订阅和任意 reply subject、JS管理/API与服务账号；校验到期断连、撤销后无新业务派发、旧连接与新epoch不能交叉接收、SDK在Windows/Node真实打包可用、Broker故障后恢复。源码已核 `server/auth_callout.go` 对响应的user_nkey/server audience校验及`setExpiration`，但还没有跑本项目映射测试。callout只在连接时准入，不能取代逐操作授权。参见 [官方auth callout](https://docs.nats.io/learn/security/auth-callout) 与 [锁定源码](https://github.com/nats-io/nats-server/blob/v2.12.6/server/auth_callout.go)。

独立运行时兼容已有证据；完整插件、安全权限、硬缓冲与 HA 门禁尚未通过，详见[验收证据](failure-validation.zh.md#1-已有证据与明确未验证项)。

Tinode 对照验证：固定release、PG隔离库、两个网络session、多topic、断线重连补历史、auth backend即时撤销、同一DSH两logical binding不能相互读topic；检查source中的用户级权限路径能否通过一层可信DSH代理满足委派，而无需每binding新账号。至少选定“collab为真相、Tinode仅路由”或“Tinode为消息真相、collab只保授权/receipt”之一再估改造，不能两边各确认成功。

OpenIM 对照验证：固定release和官方依赖最小集，核消息接收/同步时点、用户多端互踢策略、SDK Node宿主可打包性、业务回调失败是否拒绝以及PG持久核心如何迁移。只有真正需要其完整IM产品能力、且接受新增依赖时才优先它；不能裁掉其核心存储后还借其成熟度作保证。

这三项是下一实施阶段的可复现 spike，不在本设计工位启动新服务。首选出现安全/可靠性门禁失败必须保留证据再改选，不能把“库支持”记作已通过。

**不按沉没成本排除Tinode**：collab候选尚未全部合入，允许用真实对照结果推翻首选。NATS路线新增工作为IAM接入适配、PG 邮箱与提示修复、Host receipt、会话树/配额与运维验证；它省的是Broker连接/路由/集群实现和另一套IM账号/消息库集成，并没有省掉邮箱/线程权限业务。Tinode路线能直接省topic/归档/收发状态与IM管理能力，但仍需把其“用户/网络session/topic”的鉴权改造成“tenant/DSH/logical binding”，并把用户级received/read推进与每binding Host receipt分开，核跨DSH精准寻址、子树配额和即时吊销。可先做独立Tinode+PG+薄MCP/DSH adapter，不要求保留collab内部实现；若原生hook即可完成上述隔离，且总新增代码/测试/运维比NATS方案更少，应改选Tinode并做持久数据迁移设计。当前没有两者代码量或工期实测，不能声称NATS必然少写几千行；首选依据是已确认的职责匹配与运行组件，而不是已经花了多少工时。

### 4.4 下层承载选择

| 方案 | 实际收益与限制 | 建议 |
|---|---|---|
| NATS over WSS / HTTP Upgrade | 官方Broker/SDK已有完整协议处理；便于经HTTPS入口部署。仍须实测企业代理、CONNECT与空闲超时；TCP丢包阻塞其逻辑通道。 | 首发默认，直接复用NATS实现；不用Auriga JSON替换NATS wire protocol。 |
| HTTP/2 + gRPC 双向流 | Go、Node gRPC 有双向流和传输流控，protobuf 契约易生成；单 HTTP/2 连接可复用多 RPC stream，但下层 TCP 丢包仍有连接级阻塞。代理须明确支持端到端 h2/长流，负载均衡按长流而非逐消息分配。 | 受控托管环境的后续可选承载；复用本协议 resume/ACK/fence，不以 gRPC write 成功替代耐久。 |
| 自定义 WSS 或原生 HTTP/2 DATA | 对应用完全可控，但需维护帧/订阅/路由/恢复、兼容SDK及运维。 | 相比已有成熟方案当前收益不足，不作为首发；应用Host receipt不能成为重写消息中间件的理由。 |
| QUIC 多流 / HTTP/3 | 独立流避免某流丢包阻塞其他流；具备连接/流级流控与路径迁移。共同拥塞控制、共享带宽和应用队列仍会相互影响。UDP 出口、企业代理、负载均衡 CID/迁移与 Node 当前支持矩阵均须实测。 | 作为大量高延迟/丢包节点的验证路线；不作为当前 Node 跨平台发布前提。禁用有副作用消息的 0-RTT 重放。 |

上述传输事实分别依据 [RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.html)、[gRPC 流控](https://grpc.io/docs/guides/flow-control/)、[Node gRPC 双向流示例](https://grpc.io/docs/languages/node/basics/)、[RFC 9000](https://www.rfc-editor.org/rfc/rfc9000.html)。企业代理是否可用是本项目待测环境条件，不是这些标准的保证。

首发插件显式依赖官方nats.js合适模块并固定版本，不跨包偷用宿主依赖；WSS认证在NATS CONNECT完成，不依赖浏览器WebSocket API自定义Authorization头。SDK的Node/WSS、代理、重连缓冲与TLS行为须按实际版本测试。

默认每 DSH 一条数据连接。仅当压测证明同 DSH 独立热点之间的 TCP 阻塞/ACK 延迟越过 SLO，而限包和公平队列仍不足时，允许显式协商最多两条 lane：控制/小消息与高吞吐消息分离。lane 归同一 node incarnation 和租户预算，按 lane 分配取页预算，宿主持久去重与单写者锁仍只有一份；不能双连同时执行。额外连接意味着额外 FD/TLS/心跳和恢复状态，不能由 agent/会话数自动扩池。

## 5. MCP 与网络的准确关系

MCP是工具协议；数据网络复用NATS协议，WSS只是其下层传输，Auriga业务信封放在NATS payload。首发保留远程标准Streamable HTTP，不把NATS或应用信封叫标准MCP，也不因已有故障声称HTTP API天生不可靠。MCP允许JSON/SSE响应及可选会话/恢复机制，但不能代替应用持久投递。依据：[MCP 2025-06-18 transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)。

| 请求类别 | 首发通道与完成语义 |
|---|---|
| 登录/TOTP/PKCE、刷新、配对、资源授权 | HTTPS 控制面；沿既有可信 Node 流程，普通登录后后台完成连接，不新增可见“登录总部”步骤。 |
| agent 发现 binding、开线程、定向/线程回复、读有权历史 | 插件采集本次 exec 上下文，经共享远程 MCP 客户端/HTTP 池；目录和目标规则见 §6.1。发送事务提交才返回 platform_stored，回复未到不是发送失败。现有列端点工具须先升级契约，不能以 DSH 列表冒充 agent 通讯录。 |
| 平台到 DSH 的消息、请求、执行控制、状态事件、唤醒 | 经NATS WSS的本连接专属下行subject承载应用信封；耐久正文先入 PG；提交触发 HEAD 提示，Node 通过同一 WSS 发 FETCH 取页，再回持久回执。无固定间隔客户端拉取。 |
| 绑定/订阅、投递 receipt、恢复、节点就绪/暂停 | 私有应用消息，非新传输控制帧；仅Node/服务能发送，模型不见此工具。NATS CONNECT/PING/SUB/UNSUB/断线由官方实现，绑定/撤销等持久变更仍经collab授权事务。 |
| 临时进度/在线提示 | 可合并丢弃的非耐久帧，标明 ephemeral；最终结果和决定必须落为耐久消息。 |
| 大文件/产物 | 经 resource 对象上传/下载；网络只传有权限的产物引用、摘要和大小，读取时重新授权。 |
| 内部服务调用、路由/邮箱/存储 | 有界 gRPC 池及 NATS；不会为每个 DSH 建一条到 PostgreSQL 的连接。 |

“一个稳定网络入口”不意味着把既有标准 MCP 改成私有协议：每 DSH 默认一条数据 WSS，控制/MCP HTTPS 另用同一平台 origin 的有界共享连接池（支持 h2 时目标一条；h1 初始最多两条），不是每会话一个 HTTP 客户端/心跳。工具请求是业务驱动，不是周期拉取。未来如提出所有工具也走一条数据连接，须另立自定义承载适配提案并保留标准远程 MCP 对外入口；本方案不隐式更改 ADR 0016。

## 6. 身份、会话树与寻址

`用户 ≠ DSH ≠ session ≠ 活跃 agent ≠ thread ≠ connection`。同用户多个 DSH 分别有 node 身份和收件箱；同 DSH 多个主会话共享连接但互相不自动授权。session 是持久会话，agent 是其当前执行实体，冷会话没有必须常驻的 agent。

最小寻址为 `(tenant_id, node_id, binding_id)`，thread 是显式参与者集合；host_session_id 由 Node 可信宿主读取且为不透明字符串。socket 地址、MCP Session ID、模型自填 sessionId 不是身份。连接世代是临时路由版本，不替代稳定 node/binding/delivery ID。

拟新增授权记录包含 subject binding、可信 parent binding、root binding、授予方、权限交集、目标范围、expiry、最大委派深度与 policy revision。父子关系来自宿主原生 lineage/owner 接口；模型参数不能自报血缘。每个新子会话默认无网络能力，按需要由有权主体请求裁剪授权。子能力是父可委派能力、目标准入、租户策略和子请求的交集；不继承父的设备凭据、全部线程历史或所有工具。fork 复制了文本上下文也不复制 grant。

授权动作至少区分 discover、send、receive、read_history、wake、queue、steer、cancel、fork、delegate、artifact_read；同进程信任不等于跨成员授权。首发跨 tenant 拒绝，不借总部/admin 主体绕过。跨 DSH 的子任务是新远程授权参与者，不可把本地 parentSession 字符串当远程委派凭据。

现有 `thread_participant UNIQUE(tenant,thread,endpoint)` 与 `delivery UNIQUE(tenant,message,target_endpoint)` 限制同 DSH 同线程只能有一个参与会话，必须在契约阶段显式升级为逻辑参与者/target binding 级去重；不能只加 WS 后假称同 DSH 多会话已经支持。未绑定的首次接触落到 node 的授权 provisioning inbox，由策略明确允许新建普通会话或转交某个已授权 binding；不能随机选当前前台会话。绑定持久后不能静默重映射。

冷会话只保留持久 binding/grant 与未处理 delivery，不保 socket、心跳、goroutine 或永久订阅。活跃订阅在通道打开时建立，idle 5 分钟或会话结束释放；需要被离线唤醒的“收件权”单独持久化，不靠在线订阅续命。关闭/归档默认暂停新执行并撤销临时委派；保留历史读权需单独策略。重连按 node 分页恢复有权通道，默认每批 128、最多 4 批在途；不逐个会话握手。

### 6.1 DSH 内多 agent 的寻址与发现

**发送方选的是具体会话的 binding，不是整台 DSH。** binding 是平台给一个获授权宿主会话登记的稳定通信地址；会话内当前运行哪个模型或执行实体可以变化，但不能因此把消息换投到别的会话。同一 DSH 的主会话、子会话分别登记地址，共用一条物理连接。下面是契约设计提案，不表示现有“列端点”工具已经具备这些能力。

**如何找到目标**：通讯录以 binding 为一条记录，至少返回 binding_id、所属 node/DSH、显示名、角色、可用能力、主/子会话类型、热/冷/忙状态和状态观测时间。显示名可重名，只作展示和筛选；最终选择必须携带返回的 binding_id，不能凭显示名、角色或“当前前台”猜目标。发送 agent 用发现工具按任务筛选角色/能力，再选择一个明确条目；匹配多个时返回候选，工具不自动选择第一个。

发现请求的来源 binding 由插件读取本次可信执行上下文；平台按 discover 权限、目标可见范围及 tenant 策略裁剪每条记录和字段。能发现不等于能发消息、读历史或唤醒；通讯录只展示当前调用者可用且宿主已支持的能力。没有授权的会话不出现，也不能通过数量、模糊搜索或错误泄漏其存在。冷热状态只是带时间的提示，不保证执行能力；发送时必须重新核权和检查状态。

**三种明确的投递入口**：

- 直接联系：指定目标 binding，平台解析其所属 DSH 和真实宿主会话，发送方不能覆盖宿主 session ID。一次发送不得同时指定互相矛盾的 binding 与线程目标。
- 已有线程：指定 thread_id 和明确的发送/回复模式，平台从持久参与者集合确定收件 binding。同 DSH 两个会话可以同时参加同一线程，各有自己的 delivery 和回执；同一 node 不合并收件人。群发沿现有 fanout 上限和逐目标授权，不因为同机就扩大接收范围。
- 只知道 DSH：走显式“首次联系节点”入口，唯一目的地是该 node 的受控 provisioning inbox（待分配收件箱）。普通精确发送入口缺 binding/thread 就报缺少目标，不能隐式选会话。首次联系持久保存后返回 awaiting_binding，说明“平台已存、尚无目标会话”；不是 host_accepted，更不会自动投给前台会话。只有获权分配者才能选择一个已有 binding，或经明确的新建权限创建并绑定新会话；分配动作持久、幂等并重新核双方权限，不能暗中改投已明确指定的目标。

**回复如何返回**：开线程时持久记录 originating_binding_id 与每个参与者的 binding。对请求的默认回复引用原请求 message_id，由平台查其来源 binding 并确认仍是有权参与者；因此返回原发起会话，而不是原 DSH 的任意会话。对某条后续消息的定向回复同样以该消息的持久发送者为目标。要向整个线程回复必须显式选择全线程模式；参与者快照和实际目标在提交时固化，不能把“同线程”默认为本 DSH 所有会话都可读。请求者已归档/撤销时返回可解释失败，不改投其同事、父会话或当前前台。

**冷会话与失效地址**：在线 DSH 收到冷 binding 的消息，先进入这个 binding 的持久收件路径。只有具备 wake/执行权限且目标策略允许时，才恢复原 host_session_id，并经过 §7 与协议 §5 的在线执行准入；不重新 create 已绑定会话。只有 receive 权限时可返回 queued_cold，等待该会话在授权后恢复，不能把“冷”当作自动授权。DSH 离线则在期限和配额内返回 queued_offline；托管实例唤醒仍须 workspace 权限，关机电脑不能被承诺唤醒。binding 归档或撤销后拒绝新发送；提交后、执行前发生变更时停止新准入并记录可查询终态，既有副作用边界不变。

**子会话地址与 N2**：远端也只选子会话自己的 binding。目标插件内部保存经宿主核验的 child session 与 durable parent address（原生父路由地址），由专用子 agent adapter 投递；发送方不能自报 parentSession 来借用父权限，也不需要获得该内部地址。父子默认不共享通讯录可见性和收件权。N1 不宣称支持子会话执行；N2 核实具体原生方法、父不在线/归档、冷子会话恢复后，才可把相应能力发布为可用。未通过兼容验收时明确返回 host_incompatible，不回退到父会话的普通 prompt。

**端到端例子**：同一 tenant 内，DSH A 有三个主会话 A1（协调）、A2（核查）、A3（资料）和 A2 的子会话 A2c；DSH B 有 B1（分析，冷）与 B2（审阅，热）。这些是示意别名，不是生产 ID。A1 获权发现 B1/B2，选择 B1 开线程 T；B2 即使正好在前台也不接收这条私信。六个会话共用两条数据连接；子会话的可用能力取决于 N2。

```mermaid
sequenceDiagram
  participant A1 as A1 协调会话
  participant PA as DSH A 插件 / 一条 WSS
  participant C as 平台目录与 collab
  participant PB as DSH B 插件 / 一条 WSS
  participant B1 as B1 分析会话 / 冷
  participant B2 as B2 审阅会话 / 热
  Note over A1,PA: A 还有 A2、A3、A2c；各有独立 binding
  A1->>PA: 发现可联系的会话
  PA->>C: 共享 MCP / 可信来源 A1
  C-->>PA: B1、B2 的有权目录条目与 binding
  PA-->>A1: 返回候选条目
  A1->>PA: 选择 B1，创建线程 T 并发送 M1
  PA->>C: 共享 MCP / 来源 A1、目标 B1
  C->>C: 保存参与者 A1、B1 与目标 B1 的 delivery
  C-->>PB: NATS HEAD；插件按窗口 FETCH
  PB->>PB: 核 target_binding=B1 与 wake 权限，恢复 B1 原 session
  PB->>C: B1 当前世代执行准入
  C-->>PB: 唯一准入成功
  PB->>B1: 有 wake 权限后调用原会话
  PB-->>C: B1 持久接受回执
  Note over PB,B2: B2 不接收 M1，即使它是前台
  B1->>PB: 回复 M1
  PB->>C: 共享 MCP / 可信来源 B1
  C->>C: 从 M1 查原发起 binding=A1
  C-->>PA: HEAD / FETCH，目标固定为 A1
  PA->>A1: 核 binding 与在线准入后交付回复
  Note over A1,PA: 不转给 A2、A3 或 A2c
```

若 T 后续显式加入 A2，则给 A1 与 A2 的消息各产生独立 delivery，即使它们同在 DSH A；A2c 仍不自动成为成员。另一个仅写 DSH B 的首次联系请求停在 provisioning inbox，待授权分配为止。完整错误与事务边界见[协议 §1.1](protocol.zh.md#11-binding-目录与目标解析)。

## 7. 邮箱序号与客户端游标同步

### 7.1 主链路：提交、提示、取页、回执

邮箱按 node 分配单调序号 `mail_seq`；cursor（游标）表示已确认的连续前缀，不表示执行进度。每个逻辑收件人仍有独立 delivery 和授权；共享邮箱不合并会话权限。

1. **提交**：collab 同事务写 message、每逻辑收件人的 delivery、幂等记录并更新目标 node 邮箱 head。序号在锁定邮箱行的事务中分配，回滚不留下未说明空洞。原有业务事件 outbox（与业务同事务的待发事件表）照常保留，但投递不等待它。提交后才返回 platform_stored。
2. **提示**：提交回调尽力向内部 Core subject 发 {node, head_seq}，不带正文。Go adapter 用 queue group（多副本竞争处理同一请求的订阅组）消费。每副本按 node 只保最新 head，以 100ms 合并窗口限速下发；不为每条业务消息保存一条待发 HEAD。发送/合并窗口进行时的新 head 替换待发值，结束后仅在有更新时补一次。集合有全局和租户硬上限，满时可丢提示并记指标，修复扫描兜底，不能挤占正文/回执预算。多副本可能重复提示，每副本仍只保一项，不引入分片所有权。
   adapter 经 collab 查询当前 route，可缓存最多 2 秒；收到接管/撤销事件即失效。缓存仅用于无正文 HEAD，不用于 FETCH、执行准入或回执写入。旧缓存最多产生过时提示，不授新权限；实际取页仍由 collab 权威核 epoch/授权。无可靠事件失效保证时也按 TTL 强制刷新，不能无限续缓存。
3. **取页**：插件在收到 HEAD、READY 成功、重连或已有待取页且容量恢复时，通过同一 NATS WSS 发 FETCH。collab 逐项核权并返回限条数、限编码字节的页面。每 node 默认一个取页请求在途；超时按同游标有界重试，不产生领取租约，也不改变投递状态。head 不变时仍可能有未确认空洞，不能据此停止对账。
4. **处理与持久接受**：网络层先在线登记本次处理尝试，平台按 delivery 持久计数，再把条目交给独立宿主 adapter 的 `deliver(page) → receipts` 接口。宿主按 delivery_id 去重，真正 queue/prompt 前须取得当前 epoch 的在线执行准入 CAS；持久接受后才批量回 RECEIPT。隔离/未知报告不是接受成功。collab 幂等提交接受或明确终态后推进连续确认前缀，返回 RECEIPT_COMMITTED。
5. **继续与隔离**：未确认项不从 PG 删除。分页位置与确认游标分开；卡住的线程记录重试/隔离状态，允许继续扫描其他线程，绝不取“最大已见序号”当确认。隔离不延长原期限，最长 24 小时后按策略终结传输；本线程空洞限额只限制该 thread/binding，新消息不挤占其他线程。连续已确认后缀合并成精确区间，单个旧空洞不要求无限保存稀疏条目；只有 node 总量实际耗尽才 node 级背压。详见协议 §4/§5/§8。

这是一条服务端通知驱动的长连接同步链路。正常路径多一次“提示→取页”的往返，用实现复杂度换取有界背压。首发不做提示内联正文；未来优化不得改变回执和游标语义。Core 的最多一次语义与 JetStream 的持久消费者不同，见 [Core](https://docs.nats.io/learn/core-nats/) 与 [JetStream](https://docs.nats.io/concepts/jetstream)；本方案的恢复依据始终是 PG 邮箱。

### 7.2 提示丢失、离线与低频修复

adapter 先建立内部订阅，再在启动时批量检查在线积压；READY 直接返回 head 与平台确认前缀，不依赖之前是否收到过提示。随后服务端默认每 60 秒扫描“route 有效且存在未确认积压”的索引，分页取最多 256 个 node，定向重发 HEAD。一个有界扫描任务足够首发；重复扫描无害，不需要 owner generation 或 dirty CAS。多副本部署时可用单服务级扫描锁减少重复，丢锁只影响效率，恢复正确性不依赖持有者身份。

扫描用稳定键遍历，一轮开始固定边界并避免热点反复抢占队首；新连接由 READY 覆盖。扫描每 60 秒按起始时刻调度，每轮完成目标 ≤30 秒；一次提示成功到达后的合并/取页预算为 10 秒。在下一轮修复 HEAD 未再次丢失、依赖健康且无积压的条件下，尾提示丢失恢复预算才是 60+30+10=100 秒，测试 deadline 为 120 秒。每轮独立重试：若前 k 轮修复 HEAD 也丢失、第 k+1 轮成功，预算为 100+60k 秒（等价于第 n 轮成功时 60n+40 秒）；再丢一轮是 160 秒，测试 deadline 180 秒。持续丢包/分区或超载没有有限上限，必须告警并限入。没有未确认积压的在线节点不发周期 HEAD，离线节点只存储，绝不全网广播。

既有 outbox/JetStream 保留内部业务事件用途，故障不阻塞本网络提示与 FETCH；事件积压若触及共享磁盘上限仍须拒绝新写。整个 NATS 不可用时只能在配额内保存消息，不能声称仍有在线 push。正常测试关闭修复扫描仍应即时到达；只丢通知测试才依赖低频修复。不得恢复旧每节点短 poll。

### 7.3 两个候选的明确取舍

下表按“须独立证明恢复正确的状态族”计数，属于设计复杂度比较，不是已测代码行数或工期；共同的授权与执行状态机不重复计入。

| 比较 | 原逐条租约 / 分片 dispatcher 候选 | 推荐邮箱 / 游标候选 |
|---|---|---|
| 恢复状态族 | 7 类：通知 outbox、JS 消费进度、dispatcher owner generation、delivery 租约/token、dirty CAS/repair、连接世代、邮箱确认前缀 | 4 类：PG 邮箱/稀疏回执与前缀、客户端分页/ledger、连接世代、无持久游标的提示修复扫描；没有逐条投递租约 |
| 关键竞态 | 租约过期与迟到 ACK；owner 迁移与 claim；dirty 清理与新提交；另有连接/游标恢复 | 并发提交与 head；回执空洞与分页；换连接与在途页。receipt 幂等，不重新领取 token |
| 必须新增的机制 | 分片协调器、租约签发/回收、JS 通知确认、ready 集合与公平调度器、两套恢复协调 | 邮箱 FETCH/RECEIPT、受限客户端扫描、定向 HEAD 与低频批量补提示；复用 Broker 请求路由 |
| 开销与适用性 | 可直接推正文，少一次 RTT；平台持有调度/在途负担 | 每批多一次 RTT；接收端自控窗口，适合单 DSH 复用多个会话 |

**选择邮箱方案**。当前没有必须依赖逐条租约的需求。N3 如压测发现公平性瓶颈，可增加请求调度或扫描分区；不能仅因扩容就加回逐消息 lease/token。PG 本身的锁与 HA 单写者约束仍然必须存在。

### 7.4 当前连接与接管

`connection_route` 保存 tenant/node/incarnation、connection_epoch、ingress_id、down subject、服务端/CID 映射和有效期。epoch（连接世代）是 PG 原子递增的路由版本；它限制新 FETCH、发件与执行准入，不充当每条消息的领取凭证。Broker 负责定位 socket，内存连接表不是数据库真相。

READY 接管与在线 admit 按同一 node/执行记录锁序串行化。旧 epoch 已 RECEIVED 但尚未准入的项，必须在当前有效连接下重新在线核权和准入；离线、旧连接或本地缓存不能授权 prompt。迟到旧页不执行，持久 receipt 可在新连接幂等重送。

若 A 在 epoch 5 收件但未准入，B 接管 epoch 6 后，A 的 admit 被拒；B 可依法取得唯一准入。若 A 已先取得准入，即使没有回执，B 也不能重新获得许可。准入不会随每小时续接、超时或进程重启清空；不确定窗口进入 outcome_unknown 并持久终结自动重试，不能换身份重做。已获准的在途动作仍不能靠 epoch 物理收回。旧 ingress 的有效迟到 receipt 只作受限对账，撤销身份无此例外，见协议 §5。

插件须有 profile 单写者锁与持久 ledger。同凭据双进程、克隆或不同 incarnation 冲突不采用无限“最后连接赢”，进入 conflict 并停止新执行；合法接管需要可信注册记录。旧进程已开始的副作用无法由 epoch 回收。丢失设备先吊销，再注册新 node，对未决执行单独对账。

## 8. 高可用、扩容与发布

首发单 edge/adapter/collab/PG/NATS 可验证恢复，但不是 HA。生产 HA 档采用至少两个 edge/adapter/collab 副本、多个 Core 接入 Broker、独立故障域与同步提交 PG；既有 JetStream 若要求 HA，独立配置三副本，其 quorum 不进入邮箱投递路径。PG 故障切换保持单写者，同步 standby 不足时拒绝新耐久写，不默降异步后仍承诺 RPO=0。依据：[PG 同步复制](https://www.postgresql.org/docs/17/warm-standby.html#SYNCHRONOUS-REPLICATION)。

外部 LB 仅在建连时分配 Broker，无需 cookie 粘性；adapter 经 queue group 分担 FETCH/RECEIPT，不持有 node 投递租约，也不需要客户端 socket 迁移。滚更 adapter 时停止接新请求、有界结束在途事务，客户端重试未获答请求；Broker 按原生 drain/lame-duck 迁移，稳态一连接。恢复只重订固定 subjects、READY 和分页，不逐会话建立连接。

大规模先按测量增加请求副本和修复扫描分区；邮箱热点或 PG 写瓶颈才考虑按 tenant 分库，迁移要停写/追平/切换并隔离旧主。跨分片 fanout 不再假装单事务。route 有效期由 node 健康批量更新，不能每会话新增 DB 心跳。故障域隔离不足时将外部 Broker 与内部 JetStream 分部署，精确导入导出 subject，不开放全通配。

## 9. 分期与接线责任

| 阶段 | 必须实现与过关条件 | 不能据此宣布 |
|---|---|---|
| N0：设计与独立接缝门禁 | 先定 schema、IAM 网络授权上限、FETCH/回执/游标语义；分别核 auth callout 的 server_id/CID 与 KICK、私有 inboxPrefix、SDK 硬缓冲。宿主 adapter 可用 fixture 传输独立验收 create/cwd/bind 与持久恢复，不被 Broker spike 阻塞。 | 本阶段已获实施授权，或只有接口同名就兼容。 |
| N1：最小可靠闭环 | 单 nats-server + auth callout + PG 邮箱/游标 + Core HEAD/FETCH/RECEIPT + 独立宿主 ledger；两真实 DSH 普通会话往返、非默认 cwd、冷恢复、建线程映射竞争、丢回执和崩溃恢复。保留基本限额、binding 级目录/定向与回复、同 DSH 多普通会话参与者、平台持久尝试计数、prompt 前在线准入和失败关闭。 | JetStream 通知/dispatcher 已必需、子 agent 已兼容、HA 或大容量已通过。 |
| N2：授权会话树 | 可信 lineage/parent address、最小委派、递归预算、子 binding 的目录可见性与批量恢复；子 agent 专用适配单独过关，普通会话多 binding 路由在 N1 即须正确。 | 全部 Codex 原生协作能力已复现。 |
| N3：HA 与容量 | 多副本 Core/adapter、PG 故障切换、滚更与容量压测；按瓶颈增加扫描分区/请求公平调度，分别验既有 JS 故障隔离。 | 自动恢复逐条租约方案、未测跨地域多活或无限扩容。 |
| 后续增强 | 视测量选择正文外置、存储分片、双 lane 或其他承载；fork/历史/等待/中断/产物分别按原生能力验收。 | 用网络替代业务调度引擎。 |

宿主接缝必须独立于传输：`deliver(page) → receipts` 只接受经核验的投递上下文，内部负责会话映射、串行调用在线 admit 回调与持久证据；连接、分页、登记处理尝试和发送回执由网络插件负责。fixture 能证明状态机边界，真实宿主和模型验收仍不可省。

实施边界：`contracts/` 先行；`infra/ai-gateway` 承担 Go 网络/远程 MCP；`infra/edge` 增加精确升级路由和 WS 生命周期安全；`services/collab` 负责持久状态/配额/授权；`services/iam`、org 增补主体与委派判定；`services/common` 对 outbox 的任何改进须兼容现有使用者；`clients/dsh-plugin` 承担连接管理与宿主 adapter。`infra/agent|node|egress` 仅在实例生命周期/出口规则确需改变时另派受保护任务。

不得为本网络删掉真实 IAM/TOTP/PKCE、RLS、幂等和持久线程；把现有 pull 标为迁移期路径，N1 端到端通过后移除周期主链路。所有 PR 都需 CI 与 fresh 非作者独审；命中保护路径标【待审查】，作者/主控不自行合并。本设计本身也尚待独立挑战。

## 10. 可调默认值（设计提案，未批准或压测）

这是三篇文档的运营默认值集中入口；容量档位和性能 SLO 另见验收篇。条数与字节限制同时存在时取先到者；不得把硬上限当预分配量。

| 配置 / 作用域 | 建议默认 | 边界 |
|---|---|---|
| 数据连接 / 每 DSH | 稳态 1，换连接短暂最多 2 | 未来双 lane 须另测；不随会话扩池 |
| MCP HTTP 池 / 每 DSH | h2 目标 1；h1 最多 2 | 业务驱动，无定时拉取 |
| 一次性连接票据 | 30 秒、仅兑换一次 | 只存摘要，不是网络长期授权 |
| 网络连接 JWT | ≤1 小时且不超过 IAM 会话/设备授权截止 | 独立于短访问 token；到期前 60–120 秒随机重建连接，逐操作持续核权 |
| 吊销断连目标 | 系统控制面正常时 ≤5 秒 | 权威逐操作拒绝先行；KICK 失败有界重试，不复活授权 |
| 心跳 / route 健康 | 空闲 30 秒；连续两次无响应判异常；route 有效期目标 90 秒 | node 级批量更新，每批最多 256；不放宽鉴权 expiry |
| 重连 / 全站准入 | full jitter，0.5 秒指数至 60 秒；示例限入 100 次/秒 | 服从 Retry-After；429 不立即重试 |
| HEAD 合并 / route 提示缓存 | 每副本每 node 仅最新值，100ms 合并；缓存 TTL ≤2 秒；集合最多 10k node/副本、1k/tenant | 集合满可丢提示，丢失有指标；缓存不用于正文、执行或回执授权 |
| 提示修复 / 服务级 | 60 秒一轮；256 node/页；整轮目标 ≤30 秒 | 下一轮修复提示成功时预算 100 秒、deadline 120 秒；再丢 k 轮加 60k 秒 |
| 单个 NATS payload / 正文 | 64KiB / 16KiB；禁应用分片及压缩 | FETCH_RESULT 整页也受 payload 上限，不是每项各获 64KiB |
| FETCH / node | 最多 128 项、含头 ≤64KiB；默认 1 请求在途；超时 10 秒 | 实际页按剩余窗口缩小；满正文通常一页只能数项 |
| binding 元数据恢复 | 每批 128、最多 4 批在途 | 与正文 FETCH 分开计费 |
| 未确认接收 / node；channel | 256 条 / 1MiB；16 条 / 64KiB | 含已接收未提交回执项，另受本地持久队列限制 |
| 接入 Broker 每连接缓冲 / 写超时 | max_pending=2MiB；write_deadline=10s | Broker 进程级配置；不等于 SDK cap，须计全站总量及内部共用连接影响 |
| adapter 输出 / node | 硬上限 2MiB；回执/恢复控制保留 64KiB、100 条/秒 | SDK/WebSocket 自身还须证明硬上界 |
| 活跃通道 / 订阅 / node | 1,000 / 5,000；临时通道空闲 5 分钟释放 | Broker 仅固定少量订阅；持久收件授权不随空闲删除 |
| 执行准入 / node；session | 4；1 | 接受消息不保证立即执行 |
| 子树 / fanout | 委派深度 8、root 活跃后代 256；原子 fanout ≤32 | 同受 node/tenant 额度，越界拒绝或显式分批 |
| 毒消息 / 隔离截止 | 平台按 delivery 最多自动 5 次；隔离截止为原 expires_at 与首次隔离+24 小时中较早者；到期扫描目标 60 秒 | 调用 deliver 前持久计数；重启不退款；隔离/未知报告不冒 host_accepted |
| 空洞 / (thread, target_binding) | 首个未决项后最多 1,024 个本线程位置或 256 个稀疏确认区间 | 只拒该逻辑收件人新增消息；不把其他线程序号算作本线程跨度 |
| 稀疏确认 / node | 4,096 个精确区间，另保留 256 个在途回执增量 | 连续后缀压缩；未决位置不能被区间覆盖；仅总量耗尽才限制 node |
| FETCH 排除提示 | held_ids 最多 256 项，暂停 thread/binding 最多 128 项 | 本次请求有效，整包仍 ≤64KiB；不确认、不删数据 |
| 积压 / node；tenant | 10k 条或 64MiB；100k 条或 1GiB | PG 未交接及宿主已交接未处理队列均设限，先到先拒 |
| 留存 / 中档 | 待投 ≤7 天；执行请求默认 24 小时；已接受正文 24 小时 | 需 7 天正文历史时显式提高预算或外置；未接受正文不提前删除 |
| 幂等与终态元数据 | 至少 30 天；producer_epoch 最长 7 天 | 覆盖未决空洞与重试窗口后才回收；旧世代永远不能新写 |
| 磁盘阈值 | 70% 告警、80% 限 fanout/大正文、90% 拒新耐久写 | 预留 WAL、回执和回收空间；本地也适用 |

数值可在独立评审和压测后调整，不改变“持久确认、最小授权、有界资源”的语义。
