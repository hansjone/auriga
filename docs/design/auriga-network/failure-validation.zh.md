# 容量、故障注入与交付验收

状态：设计目标与待执行计划。这里没有已测吞吐/HA数字；只把源码事实、主控隔离原型和未来目标分别列出。推荐方案见[总览](README.zh.md)，身份/ACK/宿主边界见[协议](protocol.zh.md)。

## 1. 已有证据与明确未验证项

- 设计工位只读锁定源码、官方文档和当前安装DSH ASAR，没有启动服务、调用真实模型、读取用户凭据或改profile。
- 当前安装DSH为0.2.1-rc.3，build `be72761ee1a6736ebcdf4f3739363b083ae53c98`；普通session恢复、子agent拒普通prompt、JSONL sync路径是源码核验，不是整个网络已通过。主控后续R5候选`8ac1c2b`修正普通bound会话重复create，报告原生专项7通过；本工位未重复执行，仍不等完整双DSH闭环。
- 主控独立隔离Go原型报告：pinned nats-server 2.12.6/nats.go 1.49.0；校验证书的WSS、2条物理连接承载4逻辑订阅；FileStore正常关闭后以同目录新建Server可恢复离线消息；故意不ACK能以同stream sequence重投；AckSync后pending为0。报告测试全部通过、耗时0.33秒，数字是该功能测试时长，不是吞吐数据。
- 本工位已只读核对主控原型日志与exit=0。该原型**没有认证ACL**，只在隔离回环上验证自签证书；没有nats.js、真实IAM/DSH、OS强杀/断电、网络分区、HA或压测。它只证明Broker原生功能可复用，不能证明本方案PG+Core下行的恢复正确，也不能证明应用Host ACK。
- 主控另外仅在私有probe安装官方`@nats-io/nats-core@3.4.0`作源码核验：同步重连回调、flush阈值不是硬buffer cap、WebSocket send无字节硬cap。这些已作为协议门禁；没有因此认定JS背压通过，也没有修改产品依赖。
- R2：主控运行时首失源码/log已保留，其fixture把无IsCA/CertSign的自签leaf当CA，不能归为DSH/SDK故障。修正为真正CA根签发leaf后，实际DSH Electron44.7.0/Node24.21.0 + 官方@nats-io/nats-core3.4.0，在原生W3C WebSocket路径（wsFactory仅返回new WebSocket、无TLS绕过）及显式ws8.22.0指定CA/rejectUnauthorized=true路径，均一连接两订阅通过；各轮Go Broker离线/正常关闭重启/漏ACK重投也通过。本工位只读核两组日志及exit=0，功能测试分别0.81秒/0.82秒，不是吞吐。**独立运行时兼容通过，完整插件、安全权限、硬buffer、HA未测**，并非真实IAM/DSH会话或强杀耐久验收。修好的fixture错误不再列作上游兼容门禁失败。
- 真实双DSH历史试验已有真实IAM/模型/工具/入库证据，但原完整往返首次失败，不能拿WS可连、模型“已发送”或单侧host_accepted替代闭环。

### 1.1 需求与锁定代码的来源

R11（2026-10-25，仅设计）：依据本轮用户确认区分单播/组播/广播与 human/agent，独立总部广播高危授予，组播/广播强制只入收件箱；服务器签名成为首发必需，端到端签名仍预留。仅只读核实 [RFC 8032](https://www.rfc-editor.org/rfc/rfc8032) 的 Ed25519/64 字节签名与测试向量、[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) 的 JSON 规范化/重复键和长整数要求，以及 [RFC 8725](https://www.rfc-editor.org/rfc/rfc8725) 的算法/发行者/受众验证原则。Auriga 授权记录、密钥轮换、用户意图、撤回/冻结、签名封装与时限都是本轮设计，未实施或运行密码/安全探针；这些 RFC 不证明本项目安全功能已通过。

R9（2026-10-25，仅设计）：按用户确认采用顺序 uint64、去前导零十六进制文本，重装/克隆新 node；增加插件本地给出关系、接收策略、墓碑拒收、统一错误/时延、扫描自动屏蔽与不可信内容边界。R9 当时仅预留端到端签名；R11 另要求服务器签名，不能继续用旧描述豁免验签。新规则与阈值未运行验证，以下历史源码/原型不证明这些本地防护已实现。

R8（2026-10-25，仅设计）：依据任务第 8 轮用户确认补首次联系的对方用户授权、两层本地默认档位、总部待授权收件箱、组织策略优先和回执隐私。个人规则下沉、请求状态 CAS 与防骚扰阈值是待审设计；本轮未实施、未运行服务/模型或新探针，不将下表计划写成已通过。

R7（2026-10-25，仅设计）：按用户确认替换 R6 binding 通讯录为 DSH 系统目录、64+64 位两段地址、对话内交换会话地址和插件本地会话授权。分工、首联创建幂等、兼容与限流均为本轮设计推导，未实现、未运行新探针或宿主/模型验证；下列历史证据不作为 R7 已通过的证明。

R5 补充（2026-10-25，仅文档）：依据第二轮独审补充隔离终态、平台处理次数和执行前在线准入；未重跑任何服务/宿主试验。只读官方 nats-server v2.12.6 及现有模块缓存确认：`const.go:102` 的默认 MAX_PENDING_SIZE 是每客户端 64MiB，`:132` 的 DEFAULT_FLUSH_DEADLINE 为 10 秒。本文推荐显式改为 max_pending=2MiB、write_deadline=10s，是待压测的配置建议，不是已应用配置或已证明的进程总内存上界。[锁定源码](https://github.com/nats-io/nats-server/blob/v2.12.6/server/const.go#L102)。

R4 只读核验（2026-10-25）：本轮不重跑历史原型或宿主测试。以下新增事实以锁定源码为依据，其余沿用注明出处的历史证据。

- nats-server v2.12.6 官方源码及本机 Go 模块缓存一致：`server/events.go:62–63` 定义 KICK/LDM；`:1484–1494` 注册系统订阅；`:3195–3236` 以 CID 调用 DisconnectClientByID/LDMClientByID。`auth_callout.go:376,456–464` 将客户端 CID 填入请求；`:322` 设置 JWT expiry。源码能证明控制接口存在；CID/server_id/运行世代持久关联、重启误踢防护和权限隔离仍须 N0 实测。[events.go](https://github.com/nats-io/nats-server/blob/v2.12.6/server/events.go#L3203)、[auth_callout.go](https://github.com/nats-io/nats-server/blob/v2.12.6/server/auth_callout.go#L456)。
- nats.js v3.4.0 的 `ConnectionOptions.inboxPrefix` 是官方响应前缀配置，可支持受限 request()；本轮仅核接口定义，未验证 Auriga 的 mux/noMux、ACL 和断线清理。[官方 core.ts](https://github.com/nats-io/nats.js/blob/v3.4.0/core/src/core.ts)。
- Tinode server 的 GPLv3 §13 允许与 AGPLv3 作品结合并发行，须分别遵守原条款及网络交互义务；开源集成无此许可冲突，不能据此把上游改为自有商业许可。[上游许可证](https://github.com/tinode/chat/blob/master/LICENSE)。
- R4 设计推导：以 PG 邮箱/游标取代逐条租约与分片 dispatcher；JetStream 不在新投递路径。该简化尚无实现和性能验证，不沿用历史 JetStream 原型成绩冒充通过。

**历史需求证据，不是当前实现证明**：旧 Team 设计第 3—4 节要求参与端主动 WSS、平台先存库再投递、ACK 后确认、离线补投和有界重连；第 8 节要求跨端沟通、追问和参与者权限。这支持长期通信的方向。历史“一个 DSH 就是一个 agent”“连接在就是在线”“约两百行自写 WS”和总部任务工作流不能原样继承。旧文档的临时存文后删除策略只作为留存决策依据，不能悄悄覆盖当前持久线程设计。旧 Hub/Team 只读作需求来源，运行时不依赖旧服务、旧插件或旧数据库。

**当前代码已核**：基线 `45fcacf1c3415fddad7a5707ded3276e4d703897`。另只读以下未合候选，不能把它们描述为 main 已具备：

| 证据 | 已确认及限制 |
|---|---|
| TEAMMCP1 `f3c76eb90bc0e79747c042be804a204182358d04`，TEAMREMOTE2 `3998c91397dbc4c072423c64b95175db0eaee12b` | `services/collab/.../V1__team_communication.sql` 有 endpoint、binding、thread、message、delivery、request_dedup，含 RLS/租户外键；`CollabService` 分配线程序号、持久幂等、30 秒租约与 ACK。没有可靠 WS 网络。 |
| TEAMREMOTE2 的 `infra/ai-gateway/internal/team`、`docs/dev/team-mcp.md` | 标准远程 Streamable HTTP `/ai/mcp`；四工具；Node 私有 register/bind/pull/ack/revoke；设备在线 IAM/org 核验。托管实例和总部系统身份尚未开放。 |
| LOCALCONNECT2 `841ee54a6d19317b2b44712b70db6f11ae2ee409`，`clients/dsh-plugin/src/local/team.js` | Node 周期 pull；receive 对已有会话仍 create；prompt 的 requestId=delivery.id，flush 后 ACK。主控真实双 DSH 验收发现非默认工作目录 create 冲突，完整往返未通过。 |
| IAM `60cdad46e17e432b2fcbc93df8946ec3912812af`；edge `e9ad9ae1b1d961c086a5fb6f2da2e54eb923abeb` | 保留真实登录/TOTP/PKCE、设备认证、退出吊销及入口隔离；这不证明新增网络身份与路由已开放。 |
| `services/common/.../events/{OutboxEventPublisher,OutboxForwarder,NatsEvents}.java` | 同事务 outbox、afterCommit 唤醒、SKIP LOCKED、JetStream publish ACK 已存在；forwarder 默认 500ms 周期补扫，发布等待处于 outbox 事务中；不能直接称作最终高容量 dispatcher。 |
| ADR 0016、0017、0020、0014、0008 | 每请求可信宿主上下文、共享 MCP 客户端、浏览器与 Node 凭据隔离、独立实例源、内部 gRPC/mTLS 与 NATS 事件是既有约束。 |

主控提供的脱敏真实失效摘要证明真实登录、注册、MCP 初始化和部分消息入库/宿主接受；不证明整个方案或双向执行通过。本设计不重复调用真实模型，不改变当前运行服务。

### 1.2 宿主发行源码与接缝证据

只读安装包 `resources/app.asar`，未启动/修改用户 profile。package.json 显示 **0.2.1-rc.3**，build **be72761ee1a6736ebcdf4f3739363b083ae53c98**，dirty=false。这与 ADR 0016 的旧 rc.2 核验是不同证据。以下路径相对 ASAR 中 `dsh/node_modules/@deepseek-ai/`，行号为其发行 JS 文件。

| API/接缝 | 当前实际行为与设计要求 |
|---|---|
| `dsh-api-session-controller/lib/index.js:755` create | cwd=workspace.path 或 request.cwd 或 defaultCwd；已有不同 cwd 的 explicit session ID 会抛 session/conflict。首次创建需明确位置；已有绑定不得无条件再次 create。 |
| 同文件 `:210,370,392,1068` resolveAgent | 普通 cold session 可从 sessionQuery 观察后 agents.resume；prompt 内部调用此路径，不需以 create 冒恢复。 |
| 同文件 `:925,949,1128,3218` prompt | queue→followup，steer→steer；requestId 写 source.rpcId；去重检查 live inbox 与 user/message 历史。并发文本 admission 不自动等同平台的事务幂等，插件须按 session 串行。 |
| 同文件 `:128,140,394,1693` 子agent | origin=subagent 或受父拥有的会话拒普通 API 恢复/准入，要求 subagent delivery 与 durable parent address。网络必须保留 parent address、验证实际子agent归属；不得绕过拒绝使用普通 prompt。 |
| 同文件 `:849` fork | 默认最近完整 turn 前缀，或显式 atSeq，生成新 session、记录 parentSession/seed；不等于继承网络授权。 |
| `dsh-session/lib/index.js:1951` flush | 等所有 listener 完成，有参与者才返回 true；false 或异常都不能 ACK。返回 true 本身不证明参与者就是正确耐久 provider。 |
| `dsh-session-persistence-jsonl/lib/index.js:426,3194,3299` | flush listener drainLive/flush；JSONL 初建与 append 有 sync；Windows namespace 使用 write-through，POSIX 有目录 sync。源码提供耐久路径依据，不能替代目标文件系统断电试验。 |

发行文件 SHA-256：session-controller `a9801acb577b03053c5d5a3191ff555c5f1d791d0acddec113bea4511e33b386`；session-persistence-jsonl `973dd78679b2661d6f492343bc22b124ae958aa597d3b36c698a2c650f909984`。保留摘要便于未来版本比对，不把本机安装路径或用户数据写进公开文档。

适配分期：普通现存/冷会话是 N1 必测；首次建会话的 cwd/preset/bind 是 N1 必测；recursive subagent delivery 的确切方法、参数、父存活约束、冷子agent恢复与取消是 N2 必核，目前只核到普通入口拒绝，**尚未证明专用子agent API 全链路可用**。授权历史、等待/订阅、中断恢复、fork、产物引用分别跟随宿主真实 API 编写 adapter 测试，不从 mock 或同名方法推断。支持矩阵要写宿主 build/hash；升级后必须重跑，不笼统声称“API 不稳定”。

R10（2026-10-25，仅设计）：地址扩为 aur1:[realm.]node.local，首发仍无跨域联邦；新增总部组、组织广播、每 node 一条邮箱项与逐 local 回执、大组可恢复分页。以下新增估算/矩阵是待实现验收要求，不是已测通过；R7 的“两段”历史结论由本轮规范替代。

## 2. 容量模型：先数对对象

用变量区分用户U、物理DSH节点D、历史会话H、活跃授权逻辑通道A、应用订阅S、当前实际运行的agent R、消息发布率λ、平均目标 node 数 Fn、平均目标 local 数 Fr、正文B、未确认积压Q。物理网络连接C默认=D，和H/A/R不成一一对应。用户可有多个DSH；冷会话映射/grant 在插件持久保存，PG 只保不透明对话/投递记录；不保连接/心跳/执行 worker。

以下是可调的三档**示例估算**：每用户2个DSH、每DSH20个活跃逻辑通道、H=10A、S=2A，活跃通道平均每50秒发1条（λ=0.02A），为保留原基线数字，假设每条消息每目标 node 恰好一个 local，即 F=Fn=Fr=1.5，B=2KiB。组播须用下面的独立 Fn/Fr 公式，不能将此基线当大组容量。并非承诺每个agent都能同时跑LLM；R和模型容量独立。

| 参数 | 小档 | 中档 | 大档 |
|---|---:|---:|---:|
| 活跃逻辑通道/agent量级A | 1,000 | 10,000 | 100,000 |
| 用户U / DSH节点D | 25 / 50 | 250 / 500 | 2,500 / 5,000 |
| 历史会话H | 10,000 | 100,000 | 1,000,000 |
| 应用订阅S | 2,000 | 20,000 | 200,000 |
| 数据网络物理连接C | 50 | 500 | 5,000 |
| 平均发布λ（消息/秒） | 20 | 200 | 2,000 |
| node 邮箱项率 λFn（此基线等于 recipient 率 λFr） | 30 | 300 | 3,000 |
| 首次下行纯正文带宽 λFnB | 60KiB/s | 600KiB/s | 5.86MiB/s |

NATS Broker实际每DSH仅少量上下行subject订阅，不是表中每个应用订阅一个consumer。MCP/HTTPS有界池另计，h2目标1条、h1最多2条/DSH；因此不能把数据连接C写成全系统所有TCP连接数。TLS/协议/ACK和重投带宽初估再加50%，突发另测5倍，不将压缩收益写进预算。

**敏感性**：100k逻辑会话若集中在500个DSH而非5,000，物理连接少10倍但每DSH公平性、宿主磁盘和事件循环压力增10倍；若每DSH只有1个活跃会话，则100k逻辑会话意味着100k连接，必须另测该物理连接档。统一用户身份不能把这几种形态混报为“支持10万agent”。

### 2.1 10 台 DSH、100 个会话的正文与状态成本

同一正文 B=2KiB，10 个目标 node、100 个不同 local：message 表始终一份正文，10 条 node 邮箱项各携本 node 的 local 列表，100 个 recipient 状态/成员实例不可省略。副本、备份和宿主会话历史是另外的物理复制，不声称全系统磁盘只有 2KiB。旧 collab 已将 message 与 delivery 分表，因此“正文一份”不是从现有 100 份改成 1 份；本轮新增收益是邮箱从逐会话改逐 node，网络不向同一 DSH 重发 10 次正文。

| 对比口径 | PG 正文份数 | 邮箱项数 | 独立 local 状态数 | 首次总部到 DSH 纯正文 |
|---|---:|---:|---:|---:|
| 朴素逐会话复制正文 | 100 | 100 | 100 | 200KiB |
| 一份正文、仍逐会话投递 | 1 | 100 | 100 | 200KiB |
| 本轮一份正文、按 node 聚合 | 1 | 10 | 100 | 20KiB |

用纯示例元数据宽度 Mnode=256B、Mlocal=128B，忽略共同 message 行头、索引、WAL、副本和快照额外元数据：旧逐会话投递为 B+100×(Mnode+Mlocal)=39.5KiB，新为 B+10×Mnode+100×Mlocal=17KiB；朴素复制为 100×(B+Mnode+Mlocal)=237.5KiB。它们只是相同假设下的算术，不是实测表宽，真实快照/状态行通常更大，须按 schema 重算。正文下行 200→20KiB 仅指首次全部在线、无重投、不含地址列表/回执的情况；内网 adapter fanout、TLS、重投、每会话上下文和模型输入另计。PG 已单份正文时不宣称正文存储再省 100 倍。

一般式：正文存储率 λB；邮箱行率 λFn；成员状态与校验率 λFr；首次下行正文 λFnB；逻辑积压元数据约 Qn×Mnode+Qr×Mlocal，派发快照另加。执行准入/回执仍按 recipient，不能随 node 数缩减。全部目标离线 T 秒时，源正文 λBT、邮箱 Qn=λFnT、成员 Qr=λFrT；共享正文按 tenant 计一次实体存储，目标 node 配额按它占用的正文/列表逻辑字节计，配额统计可以重复引用但不是物理复制。

## 3. 资源预算与公式

这些单位成本是选型假设，后续用heap/pprof、RSS、GC、FD、WAL与延迟替换，不是引用上游营销吞吐。下列旧 F 数字仅用于 Fn=Fr=1.5 的单播基线；R10 组播须分别重算 mailbox/recipient，分页计划/快照、组授权和聚合计数事务另加，不直接沿用原 TPS。

- **内存**：每物理连接空闲128KiB（TLS/库/缓冲/goroutine等合计占位），每活跃logical channel 512B、每应用subscription 256B。大档连接约625MiB，channel约48.8MiB、subscription约48.8MiB。若每DSH平均在途buffer64KiB，再加312.5MiB，合计约1.01GiB；尚不含进程基线、GC余量、NATS路由/JS缓存、PostgreSQL和宿主LLM。按每连接硬窗口1MiB全部占满则另需4.88GiB，不能按典型值配置硬上限或一次性预分配。多进程总量与单副本都要测。Broker max_pending 显式建议 2MiB，大档 5,000 连接全部触顶仅这一项约 9.77GiB；adapter 2MiB/node 又是约 9.77GiB，不包含在前述典型 buffer 估算中。SDK、TLS、内核 socket 还有独立开销，不能以单连接 cap 当整机上限。
- **单接入Broker规划起点**：2k连接时空闲预算约250MiB、平均buffer125MiB；先配置4vCPU/2GiB测试机、FD上限至少16k，测50%与75%负载点。此为测试起点，不是额定规格；2k 连接的 Broker pending 若都达到 2MiB 已约 3.91GiB，不能在这台 2GiB 测试机上承诺该极端档。须按总内存预算降低准入连接数/缓冲上限或增配，并测试慢消费者断连。大档建议至少3接入副本、任一故障后余下节点仍≤已测安全上限70%；是否能共用内部JS节点由隔离压测决定。
- **FD**：Broker约`C_local + cluster routes + internal clients + stores/logs + margin`；edge反代通常每数据连接占客户端/上游两端FD，约`2C_local`。5k连接edge是约10k基础FD，另算HTTPS池与滚更双连接。不能只调Broker ulimit。
- **CPU**：若待测纯adapter处理每recipient为0.2ms，大档3000/s仅此项约0.6核；TLS/JSON/校验/GC、IAM/org与DB另算，不能据此宣称1核够。握手成本、正文 16KiB 上限和大fanout分别测，不用平均2KiB掩盖极限。
- **身份依赖**：源发送按 2 次权威调用，每 recipient 取页核权与回执各 2 次，基础为 `2λ+4λF`，大档约 16k 次/秒。处理尝试和执行准入是新增必经检查；若每项都触发宿主执行且两步各需 2 次 IAM/org 调用，另加 `4λF=12k`，合计约 28k 次/秒。重试可进一步放大，最多 5 次是每项累计上限，不是可无限维持的稳态吞吐。批量核验或安全复用校验结果的收益必须实测，不用全租户缓存绕过吊销。
- **PG 操作**：FETCH/RECEIPT 各平均每批 16 条（编码 ≤64KiB），基础 `λ+2λF/16` 为约 2,375 次/秒，其中写 2,187.5、读 187.5。每项第一次处理尝试登记和执行准入各增加一个短写事务；全部 delivery 均执行时再加 6,000，达到约 8,375 次/秒。仍未计提示查 route、配额维护、到期任务与既有 outbox，不能把基础数当完整容量。
- **提示路径查询**：令 H 为各 adapter 合并后的 HEAD 下发总率，R 为 adapter 副本数、D_active 为活跃 node 数、缓存 TTL T=2 秒。稳定路由且无失效时，route 查询率近似受 min(H, R×D_active/T) 限制；接管/失效和冷缓存会升高，保守不计缓存时预算按 λF=3,000 次/秒。100ms 合并只减少同 node 的密集提示，平均低频流量可能几乎无收益；不能假定所有提示被合并。三类操作合计大档保守约 11,375 次/秒（写 8,187.5、读 3,187.5），不含重试。
- **修复与到期查询**：大档全部 5,000 node 在线积压时，256 node/页，一轮至少 ceil(5000/256)=20 个批查询；每 60 秒约 0.33 次/秒，单扫描器才是此数，多副本重复扫描乘实际数量。修复页批量带 route/head，避免另按 node 查 route；每轮仍可能发出 5,000 个 HEAD。到期任务对 Pending/Quarantined 的索引批处理、稀疏区间合并及局部限额更新另测。提示查询无需逐条调用用户 IAM，但只能返回内部受限路由与无正文提示；FETCH/admit 权威校验不缓存。线程/邮箱热点锁可能比总 TPS 更先限速。
- **心跳**：NATS keepalive目标空闲30s一次、两次未应答判异常，约`C/30` PING/s；大档约167/s，加PONG约334条/s。由官方客户端/Broker统一配置，不另给每会话心跳。另有node 级应用健康更新可合并进有业务的状态消息，最多同30s级预算；PG route续期每批256，避免每node一事务。
- **刷新重连**：按连接凭据一小时、平均提前 90 秒续接的 `C/3510`，大档约 1.42 握手/s，须计票据/IAM/TLS。全站重启重连full jitter 0.5s指数至60s；限入100次/s为示例，大档至少50s才能全部重建，目标120s内99%恢复需留故障与授权余量。429/Retry-After控制节奏，不能同时回零等待。访问 token 刷新独立计算，不应触发每四分钟 WSS 重连；若实际 IAM 网络授权更短，必须按真实剩余期限重算。
- **积压**：离线1小时、全部接收端离线时 Qn=λFn×3600、Qr=λFr×3600；基线大档均为 10.8M，但源 message 为 7.2M，正文只存一次约 14.75GB（十进制），不是把每 delivery 引用再当正文复制；行/索引/快照/去重/WAL 另算。默认node队列10k条或64MiB取先到，tenant100k条或1GiB取先到，只是保守初始配额；大档持续离线会明确拒收，不能同时声称无限留7天。
- **磁盘**：按每源消息正文2KiB +消息/dedup/既有事件元数据约1.5KiB + F×1KiB delivery，合计约5KiB；索引/膨胀初估×2为10KiB/源消息。持续全天小/中/大档分别约17.7GB/177GB/1.77TB每日逻辑占用；7天约124GB/1.24TB/12.4TB。再加PG副本、WAL/PITR、备份，既有 JS 事件另计，不把它当本方案正文备份。该 7 天情景不是运营默认值。
- **中档留存与缓解**：首选缩短已接受正文留存至 24 小时；有长期历史需求则将正文外置到授权对象存储，PG 保留摘要和引用。上述中档 1.24TB/7 天中，正文按 ×2 分摊约 495GB，其余约 743GB；正文改 1 天、其余暂按 7 天，仅该口径约 814GB，说明外置正文也不能解决全部成本。真实默认去重/终态元数据至少 30 天，须按实际行宽另算，不能把 814GB 当磁盘采购总量。大档正文分摊约 4.95TB/7 天；缩短或外置优先于无界扩大 PG。外置要先完成对象耐久写，再事务提交引用；校验 digest、读取重授权、孤儿回收和备份一致性，不能让 PG 确认指向未保存对象。待投未接受正文仍保留至截止或明确终态，不能随 24 小时策略提前删。

R11 额外预算：human 意图签发/消费和 agent source_binding、广播 grant/冻结版本检查、撤回状态更新计划、回应统计都是新事务与存储成本。签名数量按 node 下行项及页头/HEAD/回执/准入等响应计算，不仅按源消息：令 S≈λFn+Sfetch+Sreceipt+Shead+Sstatus+Sephemeral，重取/重签再放大；验签也按实际收帧，不能假定 group 只做一次签名就能适配所有 audience。Ed25519 原始签名 64B（base64url 为 86 字符），其他服务器头/清单/授权引用另计，全部计入 64KiB 页面与缓冲上限；CPU 单价须 Go/Node 实测，旧 0.2ms/项估算不含密码成本。密钥集每 node 最多 5 分钟新鲜度，5,000 node 均匀刷新约 16.7 次/秒为预算起点，附随机抖动、同机合并与更新推送；这属于安全元数据刷新，不恢复消息短轮询。撤回 10,000 node 同样至少 313 页加更新 ACK，需独立预留控制容量，不能与正文队列争满后永久无法撤回。群发 100 收件人最多产生 100 个首个业务回应，源 inbox/汇总可承受但模型自动调用数必须为 0；重试/最多 3 次显式修订另计，按用户去重 UI 不降低真实网络/状态成本。

R10 额外预算：按 node 聚合后，受信插件正文 ledger 可共享，但每 local 接收、尝试/准入、关系撤销、回执与宿主历史仍消耗资源。每个大组计划保存完整快照；每页最多 32 node 的短事务、费用预留/释放、聚合状态/退出处理和内部 Core 临时扇出必须单独测。广播 10,000 node 至少 ceil(10000/32)=313 页；只是页数下界，完成时间还受授权/邮箱背压与公平调度限制，不能套用小批 P99。普通组最多 1,024 成员/256 node，每 node ≤256 locals 且编码 ≤64KiB；一台收大量 local 仍受本地 1,024 个待处理 recipient 与执行并发限制。

R7 额外预算：每次获准首联至少增加一次 CONTACT_ASSIGN 写事务，首回复提交还更新一次关系；初始 system delivery 和首回复 delivery 均计入 λ/F，不能算作零成本控制帧。R8 另增每次授权决定/撤销的状态事务、源端隐私 submission/幂等记录和审批提示流量；R9 再计本地关系/拒绝去重/扫描计数/审计写入、有界统一拒绝队列及安全摘要上报，不能假设拒收没有存储/CPU 成本；未授权等待不产生模型负载，但占总部待决存储。待批准 contact、local 高水位/tombstone、本地 grant/lineage 存储另测；上表旧参数未含这部分新业务分布，不承诺原吞吐。

扩容触发看最先到达的资源：adapter/授权CPU持续>60%、P99越标、线程锁/IO延迟、pending年龄、存储>70%、连接断开率、单租户公平份额被侵占。扩Broker不能修PG写瓶颈；加CPU不能修无界重试。

## 4. 可靠性与性能目标分别验收

**正确性门禁（无可容忍计数）**：在声明故障模型内，已platform_stored且未过期/显式终态的消息丢失数=0；跨 tenant/无关系完整地址或本地无权会话交付=0；无有效服务器签名的业务处理=0；无广播 grant 发送=0；agent 以 human 发送=0；组播/广播/其 inbox_only 回应触发模型或工具=0；旧 epoch 成功的新 FETCH/发件/执行准入=0（受限回执对账不是新操作）；重复宿主request准入=0（限定受支持adapter与持久日志可核场景）；未知外部副作用被自动重做数=0。任一失败不以P99或“99.9%成功”掩盖。

**首发性能目标**：在同区域RTT≤50ms、payload≤2KiB、Fn≤2 且 Fr≤2、资源低于70%、身份与存储正常且未积压时，提交响应P95≤250ms/P99≤750ms；commit→在线 Node 收到 FETCH_RESULT 正文 P95≤500ms/P99≤2s（不能只量 HEAD 提示）；收到→host持久接受P95≤1s/P99≤5s，不含LLM排队/运行。应按多个tenant同时测，不只平均。

**恢复目标**：单连接网络恢复后，在无大积压条件下P99≤10s恢复READY/业务游标；丢通知修复默认 60 秒周期、一轮扫描 ≤30 秒、取页恢复预算 10 秒；仅在下一轮修复 HEAD 未再次丢失、依赖健康且无大积压时，恢复预算 100 秒、用例 deadline 120 秒。若前 k 轮修复提示继续丢失，第 k+1 轮成功，则预算 100+60k 秒；再丢一轮预算 160 秒、deadline 180 秒。持续丢失无有限上限；网关/Broker滚更或全站重启后，目标120s内99%节点恢复，剩余有明确错误。积压清空时间下界`Q/(可用消费率-新到达率)`，分母≤0时永远清不完，必须限入/扩容，不能给固定恢复承诺。

**HA目标**：生产HA档月度平台消息准入可用性99.9%，分别计自身故障、IAM/org、PG、Broker依赖；用户终端长期离线另计，不能通过全排除依赖使数字变好看。单机档不继承HA可用性承诺。同步PG档已提交数据目标RPO=0（限定一故障域失效且同步副本幸存）；异步备份灾难恢复必须披露可丢窗口。PG failover目标RTO≤60s、整服务≤120s均待演练，未知故障不承诺这些数值。

## 5. 故障注入矩阵

每项用随机fixture、固定种子、故障点钩子与有界deadline；记录接受ID集合、PG状态、Host ledger及实际副作用计数。隔离网络代理注入丢包/延迟，禁止修改用户系统网络/防火墙。

| 故障/操作 | 必须观察的结果与过关条件 |
|---|---|
| 系统目录跨 tenant/无 discover/重名/状态过时 | 只列有权 DSH 系统地址、显示名、归属用户与在线观测；不列会话地址/数量/角色/冷热/父子结构。重名不自动选择，状态缓存不授发送权。 |
| 猜地址 / 总部误放行 / 复制别人的地址或 thread_id | 即使总部错误放行，插件没有 target_local 对该可信来源的地址给出记录也拒收、审计；正文不进会话，错误 session 执行数 0。节点/user 范围须显式授权，换 local 或 thread 不扩大关系。 |
| 伪造 from / user_id / source_context、跨 tenant 或错误 delivery | 仅信平台受保护响应中与消息/摘要匹配的盖章身份，payload 自报不能覆盖；缺可信上下文或字段矛盾拒绝，不将探测计到被冒充用户。受损源插件的自身 local 伪造与总部身份权威受损仍是披露的信任边界。 |
| 两个目标均有关系，但发送方选择错误联系人 | 响应列出实际完整地址，仅投所选 local；不从正文或角色猜真实意图。 |
| node/local 计数器提交前后断电、并发分配、备份回退 | 从 1 递增、先耐久预留再暴露；重启不回退不重复，创建失败烧号可跳号。不能证明高水位则停止分配，禁止按当前会话最大 ID 回填；uint64 耗尽拒绝、不回绕。 |
| 重装/克隆试图复用旧 node/local/凭据/关系 | 必须注册新 node，旧地址和消息不改投新身份；新 node 的 local 可从 1 起，完整地址不同。原安装普通重启保原身份；意外凭据复制仅作为故障按 fencing/conflict 阻止双执行，不作为合法迁移。 |
| 同用户云端与本地两 DSH 同时在线 | 两个不同 node/系统地址、邮箱、epoch 和一条各自 WSS；登录一个不互踢另一个，吊销仅影响授权范围，同 local 数字不串会话。 |
| 同 DSH 两会话同组线程、同 message 经组快照发往两个 local | 两个显式同意的参与者、一个 delivery/mail_seq 内两个 recipient_id 与独立回执；一个撤销/接受不影响另一项，HEAD 合并不合并目标，新成员不自动读历史。 |
| 回复到错误会话 / 切换前台 / 伪造 reply_to、in_reply_to | 默认 to 等于原消息经核 reply_to（v1=from）；B1 回复 M1 只到 A1，A2/A3/A2c 不收。第三方重定向、跨线程引用和无权回应拒绝；全线程广播须显式。 |
| 首次联系默认“每次询问” / 对方用户四种选择 | 正文与权威请求留总部，contact_pending；本地提示/预览不产生 host_accepted，不推进游标，create/prompt 为 0。允许本次仅建该对话；总是允许按真实源 node/用户范围；拒绝终结并冷却，屏蔽后不再提示。 |
| 两层默认授权的所有组合 / 新会话地址介绍 | 第一层允许、第二层待问/拒绝时最多创建无模型会话与标明 host 来源的确定性地址回信，模型/工具次数 0。每层四档独立；第二层允许不绕工具 grant，重投同执行请求不重复询问/调用。 |
| 本地档位/名单被远程消息、模型或普通总部 UI 修改 | 一律不能修改；决定仅来自目标真实用户的本机插件（总部系统 DSH 为管理员本地策略）。规则/名单留本地，总部不保存未来默认授权副本，不借 body/flags/ext 提权。 |
| 总是允许按 node/用户、源归属变化、同用户多目标 DSH | 来源用户由 IAM 核实；node 范围不扩到其他源节点，用户范围按当前归属匹配。源转属重评，目标另一 DSH 不自动继承，屏蔽优先于允许。 |
| 请求洪泛、批量换 local/node、同键重复、被拒后冷却 | 源 node→目标待决≤1、源用户→目标≤3，速率/目标总额同时生效；同键不增额度，新 local 不重置。拒绝后 24 小时冷却、本地屏蔽无新提示；无状态查看权者外观统一，内部仍有界。 |
| 未授权者比较不存在/组织禁联/屏蔽/在线/离线目标 | 源 submission 耐久后均“已提交请求”，查询无目标存在/在线/拒绝时间/目标限流提示差异；不存在目标无虚假 delivery。源自身鉴权/格式/入口资源失败照常报错，不谎报已提交。 |
| 有状态查看权者查询等待/拒绝/过期 | 分别 contact_pending/contact_denied/contact_expired，等待不算发送失败、不自动重发、不显示已送达；撤销查看权后按统一外观处理，不能靠旧查询缓存看新私有状态。 |
| 离线期间过期 / 上线迟到批准 / 提示重复 | 总部到期任务写 contact_expired 与未交接投递终态，无须目标在线；上线不再提示过期项，迟到允许/分配拒绝。未过期项上线读取本地规则再决定，不由总部缓存自动允许。 |
| 组织策略覆盖个人允许 / 同组织但部门禁止 / IAM 故障 | 组织禁止优先于 allow_once、总是允许和 same_org，create/prompt/工具为 0；组织故障失败关闭，不允许通过个人 UI 扩权。 |
| CONTACT_DECIDE 提交前后崩溃、响应丢失、乱序 | 本地决定和 outbox 持久后提交，按 decision_id/版本幂等；未提交不创建，旧允许不能覆盖新拒绝/过期，状态正确且不重复建会话。 |
| 授权撤销竞态：与 create/admit/工具开始或回复提交并发 | 本地新 revision 先于动作则不调用；总部请求/关系 CAS 防迟到旧允许恢复。已建会话后续消息按新策略待问/拒绝；旧准入不重发，已开始动作准确报告无法收回。 |
| 撤销未同步就断网/重启、个人策略库丢失 | 目标本地立即停新执行，重连先补撤销再处理新投递；总部可有界存后续消息但不代批。规则丢失回到每次询问，不从总部历史 allowed 恢复永久许可。 |
| CONTACT_ASSIGN 响应丢失 / create 成功后崩溃 / 意外克隆接管故障 | 同 contact 固定唯一 assigned_address/创建许可，查询原预留 native ID；查询无原会话证据则 outcome_unknown，只对账不重新获取创建许可；不把本机幂等当跨克隆幂等，不换 ID 重建。旧 epoch 不获新许可，同 contact 创建数最多 1，可能 0。 |
| 新会话回复丢失后重试、A 首联同键重发 | B 持久 outbox 重发同 reply key，平台回原 message/delivery；A 只得同 B1 地址，contact/local/native session 数不增加。不用“没收到回复”判创建失败；新键按新联系另限流。 |
| 首联 system receipt 后重启/取消/到期/清理 | 只在获准后允许 system receipt；本地已接收项不重入箱，未决 contact/分配/正文仍可恢复；授权前总部积压不得靠预览 ACK 释放；只给同 assigned_address 一次执行准入。到期禁止新创建/准入，传输 cursor 推进不清执行状态，不按普通接受后 24 小时提前删未决证据。 |
| 首联回复伪造新地址 / 另一 node 介绍 / 任意添加参与者 | 仅原目标 node 对有效 contact 的唯一 assigned_address 可随首回复原子建立关系；其他介绍拒绝。邀请第三方须该地址所属 node 的有权对话确认，系统联系不授全节点/全历史权限。 |
| 冷 local 有/无 wake、DSH 离线 | 只有仍开放且本地关系/接收模式允许时恢复原 session 并在线 admit；否则等待或统一拒绝，无成功假象。归档/关闭/删除不属于可唤醒冷会话。 |
| 归档/关闭/已删除会话被投递或伪唤醒/control、平台尚未知 | 插件在恢复前检查墓碑，拒收全部新入站，resume/create/prompt 次数 0；对外与不存在/无权/屏蔽统一 target_unavailable，相近时延。已有回执只对账，不重开；在途副作用不伪称收回。 |
| 归属用户显式重开 / 原会话已不可恢复 / 旧拒绝消息重放 | 原 native ID 可恢复才重开同身份并重核关系；不可恢复则新 local，旧墓碑不复用。旧终态消息不自动复活，远端文字不能代替用户操作。 |
| 子 local / parent address 错配、父不在线或 N2 未通过 | 先核本地关系和生命周期，拒绝统一 target_unavailable；再核原生 lineage/独立 grant，未支持才返回 host_incompatible，不走父普通 prompt，目录不因 type=subagent 暴露树。 |
| 三段/省略 realm、uint64 极值、补零/大写、类型改写 | 本域 2 下 aur1:3f.1a2 与 aur1:2.3f.1a2 及 JSON realm="2"/node="63"/local="418" 归一，同键只形成一消息；三段均精确 uint64，拒补零/大写/符号/空段/溢出。sys local=0；node=0 仅 group/local≥1，不允许 group 作 from。 |
| 外 realm、四段以上、子层级伪装地址、aur2 | 外域明确 realm_not_supported，无外部查询/连接；v1 拒绝更多段与不支持版本，不把子层级截掉当合法地址。子会话独立 local、父子只在插件，realm 分配高水位同样断电不回退/不复用。 |
| 20 次无效命中 / 10 个不同 local / 去重与重启 | 滚动 60 秒阈值触发本地持久自动屏蔽，合并提示用户；同 delivery 重投只计一次，换源 local 不清零，重启仍屏蔽。误封可由用户解除并审计，不因平台解除自动放开。 |
| 分散到多个 DSH 的低速扫描 / 单目标恶意报告 | 总部结合跨 DSH 摘要及认证/路由审计识别，按组织策略隔离源 node；不能仅凭一份未经核实报告全局封禁。上报不含正文/local 清单/个人名单。 |
| 总部安全报告通道中断 / 上报或日志满 | 本地关系/墓碑/屏蔽继续拦截，安全队列有界合并并记录丢失统计，恢复后补报；审计写失败禁止新交付，不以“总部没答复”为放行依据。 |
| 本地拒收上报丢失 / 同 delivery 重投 / 平台已接受后迟到拒绝 | 正文不再入会话，有界拒绝元数据重试；平台按 rejected 幂等写该项 Revoked tombstone 后才推进游标，不伪造 host_accepted。已接受/已执行事实不被覆盖，其他 local 继续服务。 |
| 不存在/归档/无权/屏蔽的批量差异探测 | 错误码/文案/字段/重试语义一致，无细分 reason、host_request_id 或状态查询旁路。相同负载下比较四类延迟分布，统一 100–200ms 调度目标（待测），不能仅同字符串却一个立即返回；超载无无限定时器。 |
| 会话 replies_only / thread_messages / receive_none 与关系撤销 | 仅回复模式核原请求、精确来源、线程及期限，旧请求不能无限续发；不接收含 control 全拒。用户撤销先本地版本生效，下一条不得进入会话；自动模型授权不覆盖接收策略。 |
| 首联 B1 地址介绍早于 A 本地关系激活、回包丢失或伪装首次回复 | 仅原 contact 的待回信记录可接受已认证 B 的唯一 assigned_address；查原提交结果补齐本地记录，不从普通成员表补授权。没有原 contact、错 node/local/thread 的介绍拒绝，重放不建第二关系。 |
| 提示注入类正文/附件/审批预览冒 system、管理员或已批准 | 保留外部来源标记，正文不变成高优先级指令、设置或可执行工具参数；危险工具本地审批未通过时副作用 0，消息不能替自己批准。不自动执行脚本/打开链接。 |
| control 伪装普通回复 / 有联系权但无动作 grant | 仍核独立控制动作/对象/期限及本地权限，受 receive_none 和墓碑约束；不能靠 kind/flags/ext 或联系允许绕过。 |
| e2e_signature 空/非空伪签名、端到端必需能力 | 此扩展仍可选且无信任效力，不能替代必需 server_auth；没有服务器签名一律不处理。若另要求端到端验签能力则拒绝协商，不冒已提供端到端完整性。 |
| N/N-1 未知可选字段/flag、扩展超限、未知必需能力 | 存储转发保留未知可选字段且不执行其含义，摘要含扩展；未知必需能力/kind/控制语义拒绝。超限、重复 key、已知字段错型拒绝；不借“忽略”绕授权或重置 TTL。 |
| send提交前崩溃/回滚 | 不存在platform_stored；同键重试只形成一条消息。 |
| PG提交后、响应丢失 | 同键返回原message；异体同键冲突，message/delivery/dedup数量不增。 |
| commit 后初始 HEAD 全丢，且无后续新消息 | PG head/未确认项仍在；下一轮修复 HEAD 成功时，仅靠 60 秒扫描恢复。扫描 ≤30 秒、合并/取页预算 10 秒，健康且无积压时预算 100 秒、deadline 120 秒；无客户端周期空 FETCH。 |
| 初始提示丢失，第一轮修复 HEAD 也丢失 | 第二轮独立重新提示；在其成功且依赖正常时预算 160 秒、deadline 180 秒。连续 k 轮失败按 100+60k 预算，持续丢失只告警，不伪称 100 秒必达。 |
| 热 node 连续提交、客户端停读、adapter 合并集合满 | 每副本每 node 最多保留一个最新 head；100ms 合并生效，多副本重复有界。集合满丢提示有指标，PG 不丢业务；Broker pending 按 2MiB 配置触发慢消费保护，内核/SDK 额外缓冲也有观测。 |
| 提示 route 缓存命中、失效事件丢失/换 epoch | TTL 最多 2 秒；只可能错发无正文提示。旧 ingress FETCH/admit 拒绝；测出冷缓存与稳态查询率，不把缓存当授权真相。 |
| 提示重复/乱序，FETCH_RESULT 丢失或超时 | 取页不改变 delivery 状态；同位置重试、同 delivery 去重；head 取单调最大提示但确认前缀不跟随它推进。 |
| adapter 收 HEAD 后崩溃/启动与提交并发 | 重启先订阅再扫描在线积压，READY 直取 PG head；即使尾部提示永久丢失仍由修复覆盖。 |
| Core 页面丢弃/客户端缓冲溢出 | 无 receipt 不清 PG pending；同位置重取，超窗拒绝或断连，本地持久去重无空洞。 |
| WSS单向黑洞/半开/代理空闲断连 | native heartbeat/timeout发现；bounded jitter重连，原request身份不变。 |
| 两Broker中接入节点崩溃 | 官方客户端重新接入，重新认证/READY、补投；不粘死原节点。 |
| adapter 滚更/queue group 副本退出 | socket 可继续；未答 FETCH/RECEIPT 有界重试到其他副本，不需要转移 delivery 租约；重复回执只提交一次。 |
| 意外凭据克隆故障：A(epoch 5) 已 RECEIVED，B(epoch 6) 接管先于 A admit | A 的在线 admit 返回 stale_epoch，不调用 prompt；B 取得唯一准入。A 离线/仅持本地 ledger 同样不得执行，副作用计数最多一次。 |
| A admit 先提交，B 随后接管；A 响应丢失或 prompt 前崩溃 | B 不获第二许可；A 重启查询不再次发起 prompt。无法确认原执行则持久 Unresolved；允许零次执行，不以“至少执行一次”掩盖重复风险。 |
| 每小时续接 / 旧 epoch 页面或 RECEIVED 项 | 无准入项用新连接重新在线 admit；已有 admission 只对账，不因换 epoch 清除。旧 ingress 回执仅受限补录，conflict 检测不是唯一防重手段。 |
| admit 与 cancel/expiry 并发、IAM/连接不可用 | 同一执行记录版本 CAS 决定胜者；未获准只保留 RECEIVED。取消先提交则不执行；admit 已提交的在途动作不能宣称被网络撤回。 |
| 101 毒消息、102…N 属其他正常线程 | 101 隔离期间其他项继续接受，连续后缀合并精确区间，不能覆盖 101；隔离到期/撤销/有权 skip 提交后前缀跨过。其他线程不因 A 的局部限额停止。 |
| 隔离项无人处理、node 离线、原期限早于隔离期限 | 平台按 min(expires_at, quarantine_at+24h) 禁止新准入，60 秒目标批处理持久终态；原 TTL 不延长，不依赖客户端回连。 |
| deliver 连续使插件崩溃 / ATTEMPT_BEGIN 响应丢失 | 平台次数在进入 deliver 前提交；本地 entered 持久化后才调用，重复响应不二次进入。重启不退款；无执行准入的崩溃最多自动 5 次，下一请求隔离，不靠 FETCH 次数或本地重置绕过。 |
| 本线程跨度 1,024 / 稀疏区间 256 与 node 总区间 4,096 | 局部阈值只拒该 thread/完整目标地址 新消息；node 全局未满时其他线程仍服务。区间精确合并，保留 256 个在途回执增量预算，任何阈值不得拒绝必要终态回收或伪造已接受。 |
| 本地 outcome_unknown、平台 Unresolved 提交前后 | 本地报告不推进游标；平台原子写执行未知、停止再准入与传输 tombstone 后才跨过空洞。查询仍显示未知，迟到结果可补录，不触发自动重做。 |
| held_ids / 暂停标记重复、越权、过大或重启丢失 | 有权有界集合只抑制本次重复正文，不清 pending；越权/超限拒绝。遗失排除集可重发，已持久项去重，其他线程可继续扫描。 |
| 撤销后 KICK 成功/失败、CID 复用、旧 KICK 延迟 | 后续准入立即按权威校验拒绝；系统面可用时断连目标 ≤5 秒。错误/旧 server 运行世代映射不得踢错连接；失败有界重试、到期兜底，不恢复授权。 |
| 私有 inboxPrefix 的 request()/mux/noMux/任意 reply | 仅当前 ingress 私有响应订阅可用；跨 ingress、全局 inbox、系统 subject 均拒绝，超时/重连释放订阅，身份不能由 reply 推断。 |
| 网络 JWT 与 HTTP 访问 token 分别到期 | 访问 token 正常刷新不切断健康 WSS；网络 JWT 到期前随机续接且原生过期关闭；IAM 会话撤销即拒新操作，不以一小时剩余期放行。 |
| 重用已兑换ticket/伪造up subject/任意reply | Broker或adapter明确拒绝；不能伪造其他 node 的 from；可信插件阻止模型覆盖本地来源，模型无法获得票据。 |
| 订阅其他tenant、`>`、`$SYS`、`$JS.API` | 全部拒绝，日志无凭据；不能从错误中泄漏他人正文/存在性。 |
| IAM/org故障、到期、退出、设备/成员/父grant吊销 | 总部对 IAM/node 故障失败关闭；插件对本地 grant/父链撤销立即拒绝执行并持久上报，子 grant 不自动恢复；在途边界准确报告。 |
| PG主故障/同步副本不足/读到旧route | 无新成功耐久ACK；单写者failover与epoch正确；不同DB主不能并发接受同node。 |
| 既有 JetStream 不可用但 Core 正常 | HEAD/FETCH/RECEIPT 正常；新投递不依赖 JS。既有事件有界积压，若共享存储阈值触发则明确限入，不能掩盖资源耦合。 |
| 整个 NATS 集群不可用 | 只有 PG 有界存储，不能声称在线 push；恢复后 READY/游标取页，按窗口恢复。 |
| 磁盘满/WAL失败/只读文件系统 | 不返回Host/PG成功ACK；保留已确认数据，ACK/回收预留不能被正文吃完。 |
| Host prompt后flush前kill | 恢复查原request；已发生副作用且结果不明进入unknown，不把无ACK当未执行。 |
| Host flush后ledger前kill | 从宿主持久历史/inbox重建同receipt；不能随机换requestId。 |
| Host ledger 提交后回执或提交响应丢失 | 当前有效连接重送同 receipt，不需新 token、不二次 prompt；PG 幂等回原状态。 |
| 同session多thread并发/重复消息 | session准入串行；每thread入队顺序正确，其他session不被锁死。 |
| 非默认 cwd/冷 session/开线程回包前首条消息 | 同 local→binding 恢复，不 create 冲突、不另造会话；预留映射与响应原子合并，同一逻辑请求只有一个宿主 session。 |
| recursive subagent/父不在线/归档 | 先核生命周期和本地关系，归档统一拒收；通过后才走真实 parent-address adapter。未支持明确 host_incompatible，不绕普通 API/父权限。 |
| poison/不支持schema/超大包 | 隔离对应recipient/channel、有错误详情；不无限重投拖全tenant。 |
| 慢DSH占满窗口/一个热root | 内存稳定有界，其他tenant P99不越正常目标2倍；同用户其他DSH仍服务。 |
| JS socket不读/断线持续publish/认证票据过期 | SDK内部、WebSocket bufferedAmount和应用队列都有可证明上界；断线停止入SDK，超限明确失败。同步callback不做异步获票、不throw；不能用pendingLimit名字冒硬cap。 |
| fanout 原子边界 32 node/256 local、33 node/257 local、一目标无权 | 小批已知授权/限额错误全回滚；超上限未声明 paged 则 requires_paged_dispatch；显式分页只保证计划耐久与逐页幂等，不保证全体接收/执行原子。 |
| cancel与start竞争/过期与迟到receipt竞争 | CAS只形成可解释终态；已started不能报“保证未执行”。 |
| 时间偏差±60s/证书和JWT轮换 | 不放宽有效期，凭据轮换后恢复，安全错误可诊断。 |
| 客户端 cursor 回退、超前、超保留窗口或伪造 continuation | 回退对账 PG 已确认前缀，不重执行；超前不删 pending；伪造分页位置拒绝；过期明确 gap/resume_unavailable，不能按 max seen 确认。 |
| 删除过期去重后重放老producer_epoch | 拒绝新写，不重新执行；元数据清理验证最大重试窗口。 |
| 10 node/100 local 同文组消息（仅入箱） | PG 1 份正文、10 邮箱项、100 recipient 状态；首次正文下行 10 份，逐成员校验/回执 100 份；同 node 两 local 共用 delivery_id/mail_seq，不共用 recipient_id/requestId；不对原组消息授执行许可。 |
| 单项 A 接受、B 拒绝、C 离线/未处理，回执丢失重发 | 逐 local 事实不覆盖；缺席 C 保持 pending，A 不重执行。只有该邮箱全部成员接受/终态才过 mail_seq；部分接受不显示全部成功，系统接受不冒会话接受。 |
| 大组分页第 k 页提交前后崩溃、响应丢失、并发 worker | 原快照/正文/预算/页进度可恢复；唯一约束保证一个 message/node 邮箱项和稳定 recipient，mail_seq 不重复增长、不漏页。platform_stored 可带 dispatch pending，未派发成员不从总数消失。 |
| 组规模 1,024/256 node、每 node 256 locals/64KiB、广播 10,000 node 边界 | 任一硬上限/实际编码超限首次保存前拒绝，不截断成员、不拆同 node 多项；超原子规模只经显式分页。源 node 数/字节预留与每页结算幂等，终态未派发部分退款，快照/计划数量有界。 |
| 加入/退出与快照、FETCH、ADMIT 并发，退出重入 | 加入只收后续新快照；退出本地立即停新交接/执行，总部获悉后停派发/读取/准入；重入新 membership_id 不收旧积压。当前有权新成员收到 reply_all 不因此能读旧引用正文。已在途动作不承诺召回。 |
| 伪造邀请/成员清单、owner 强拉入组、组被删除/撤销发言权 | 必须目标本地显式同意或预设授权才能激活，组许可不授直接私信/历史/模型/工具；组织禁令优先。删除/撤销后停止受影响未交接项，不能以旧快照继续放行。 |
| 组消息遇 replies_only/receive_none、归档/本地屏蔽、提示注入 | 未显式允许该组 thread_messages 的普通消息拒收；墓碑和屏蔽照常，正文不获工具权。逐成员统一 target_unavailable，详细原因留本地，不通过汇总暴露生命周期。 |
| 部分 node 离线/邮箱满、IAM 故障、计划到期/取消 | 在线有权 node 独立继续，满队列保留有界 pending/backpressure；身份故障不放行，重试不重取成员快照。到期/取消关闭未派发成员且保留计数，其他成功事实不倒退，不无限持锁/占预算。 |
| 仅一台 DSH 成功后清理共享正文 | 其他未派发/未接受成员与未决首联仍能恢复，直至所有相关生命周期与留存条件满足才删除；不能按首个 receipt 回收共享正文。 |
| 伪造整项 ACK、遗漏成员、held_ids 只含一个 local | 不接受客户端自报全收，按平台完整集合核对；held_ids 只抑制指定 recipient，同项其他 local 继续取，缺席成员仍 pending。已接受 local 去重不阻塞兄弟成员。 |
| 回复默认私信 / 显式 reply_all / 转发者伪私信 / 第三方 reply_to | 默认只回原 from，核原消息快照和源插件窄待回复许可；reply_all 重新核当前发言权并取新快照。新成员/转发者不继承旧私信许可，第三方重定向拒绝。 |
| 非管理员广播、授权源突发/换 local 绕限额、插件静音/屏蔽 | 非授权者零派发；每源 1/分钟突发 1、组织 5/分钟和目标 node/字节限额生效；仅目标 sys 收件，模型/新会话数 0。静音仅不通知，屏蔽明确统一拒收；普通成员 reply_all 不得变相广播。 |
| ephemeral Core fanout 丢失/重复/断线、退组后旧订阅、伪 subject | 无 PG 正文/邮箱/receipt、不补旧进度、不声称 platform_stored；真实成员表仍权威，adapter 与插件当前校验，DSH 无共享组订阅。重复/旧 revision 丢弃，最终结果/control 拒绝 ephemeral；队列/字节有界。 |
| 人/agent 同样目的的单播、组播、广播 | 总部按目的生成唯一 message_class；同类别规则一致，human 来源固定 sys，agent 来源固定获准会话，真实身份有签名，UI 不把类别或接受状态当执行许可。 |
| 普通用户建组、默认成员发言、仅管理员模式、非成员/agent 借用户入组 | 默认用户小组上限 32 成员/8 node、10 活跃组，配置审批时未经批准零创建；默认当前成员可发，admins_only 还须组内管理员，非成员一律不能发。用户 sys 入组不使其全部 local 入组；创建/管理权限不授广播。 |
| 无授予的总部管理员/普通用户/agent 广播；DSH 或用户 grant 被 agent 继承 | 默认所有主体无广播发送权，角色/同 node 不足；仅总部显式 user/node human 授予或精确 agent 授予可发，agent 不能借人权限，未授权正文/计划数量 0，返回 broadcast_forbidden 并审计。 |
| grant 到期/撤销与 send、分页、FETCH、重新签发竞争 | 同权威版本锁排序；撤销先提交零新广播，发送先提交保留保存事实但后续未交接项终结。过期不自动续，签名旧授权引用不放行，IAM 失败关闭；在途已读边界如实记录。 |
| agent 冒 human/sys，伪造/偷换 user_intent、复用过期/不同正文意图 | 无隔离的真实用户意图不能成为 human；意图绑定用户/node/目标/摘要/线程/键、60 秒有效、仅一次消费，同键重试查原结果，不得签出错误 sender_kind。受损宿主/用户凭据泄露不冒完美人机判定。 |
| 伪造 from 为广播/普通组/他 node，篡改 message_class/sender_kind | 字段角色解析与总部认证/来源绑定均拒绝，不能靠覆盖源地址发系统广播；目的为广播却声明单播/组播仍按广播权检查且拒矛盾字段。审计归责到认证入口，不信自报受害者。 |
| agent 工具传 from/class/sender_kind/server_auth/授权引用，正文嵌信封 | 工具 schema 额外字段拒绝；正文保数据，不递归当信封；插件组装来源，总部二次核权，直接调接口不能绕过。回复引用由可信当前上下文绑定，不能伪造窄回复许可。 |
| NATS ACL 误放后直接 publish 无签/坏签、替换正文/目标/成员列表/类别/时间 | 插件验服务器签名失败，业务入箱/ACK/cursor/模型/工具变更为 0；不为伪 message/recipient 写终态，不封被冒充用户。签名不能修复 ACL 读泄漏或 DoS，分别计风险。 |
| 真签名跨 node/epoch/nonce/request 重放、回执/页头被替换 | audience、请求关联、时效、状态版本逐一核验；旧 recipient 仅去重对账，不因新签名执行；页头/清单未签或条目被删改时不得推进游标。 |
| JCS 字段顺序/Unicode/uint64、重复 key、算法混淆、未知扩展 | 标准规范化测试向量跨 Go/Node 一致；重排对象键不改含义，改正文/扩展必须验签失败；重复 key/坏 Unicode/alg=none/换算法拒绝，数值不截断，签名算法选择不由消息自由决定。 |
| 未知 kid/恶意密钥 URL、轮换窗口、密钥集回退/过期/不可达 | 只从预置信任的固定端点取根签名清单，限频合并刷新；失败关闭，不下载任意 URL、不信消息自带公钥。新钥先发布、旧钥重叠可核且不重执行，过期封装重新 FETCH，不更换业务 ID。 |
| 业务签名密钥泄露/吊销推送丢失/签名服务故障 | 更高版本根签名清单吊销后旧钥不得授权新处理；刷新窗口目标 ≤5 分钟而非零，离线不新交付。停签失败保 PG pending、不发无签名正文；旧接受事实和审计保留。 |
| 组播/广播夹带 control/wake/cancel/执行 schema、设置自动允许模型 | 总部与插件都拒 dangerous_multicast_forbidden；信息/请求及其回应只能 inbox_only，prompt/EXECUTION_ADMIT/工具/设置变化 0。字面命令仍外部文本，不能从签名合法推导可执行。 |
| 100/10,000 成员同时回应、重复/修订/超时、诱导自动 reply_all | 默认单播 reply_to，按原 recipient 一份最终回应（最多 3 修订）；重试不加总。回应统计与收件/执行分开，总数覆盖原快照，源端模型自动调用 0；限流/背压生效，无“回复回应”循环。 |
| 同用户云端/本地两 DSH 收广播、单台 ACK/回应、重复显示 | 两个真实 sys recipient/邮箱项，各自回执与费用；界面按 realm/message_id 合并显示，一台成功不冒两台都成功/都回应，用户视图与成员总数分开。 |
| 冒用“总部/管理员/系统”显示名、Unicode 变体、正文伪徽标 | 普通名称按统一保留词校验拒绝，历史不合规显示中性名；真实来源/地址/sender_kind/角色徽标在可信 UI 固定展示，正文不能覆盖，词表不冒可穷尽相似字形。 |
| 广播撤回与派发/读取/接受/响应并发，撤回先于正文到达 | 权威版本先提交则停止派发/读/重新签发；每 node 可靠更新，插件先写 tombstone 再隐藏，乱序旧页不复活。已收 receipt 保事实，展示为撤回，不能宣称从未读过。 |
| 撤回通知丢失、目标离线/磁盘满、重连先展示旧缓存 | PG 更新/游标可恢复，状态同步优先；无最新状态屏障旧广播正文保持隐藏，磁盘失败不 ACK 更新，重启 tombstone 不丢。过保留窗口重新同步，不用已过期缓存绕过撤回。 |
| 一键冻结用户/node/agent、解除冻结、伪造管理状态更新 | 管理 API 核权限/作用域/审计，当前发件与未交接项按版本停止；解冻不复活 grant/终态。普通发件工具无管理更新能力，签名用途/固定动作验证，不允许借管理通道执行任意广播控制。 |
| 100%连接同时重启/身份服务限流 | full jitter/入场预算生效，恢复达目标或清楚说明资源不足，无无限CPU/内存增长。 |

## 6. 可落地的自动化与真实验收顺序

以下仅为用户后续授权后的验收顺序，当前不运行任何 spike、服务或模型。宿主接缝与网络接入分开推进，没有前者必须等待后者的依赖。

1. **宿主 adapter 独立门禁 + 接入 spike**：先用 fixture 传输调用 `deliver(page) → receipts`，核持久计数器断电不回退、本地给出关系、墓碑拒收、统一错误时延、扫描屏蔽、外部内容/危险工具隔离，以及仍开放会话的冷恢复与无模型 create/地址介绍；独立接入侧核 NATS官方Go/JS客户端、真实Broker、隔离IAM/org fixture；负向 subject 矩阵、私有 inboxPrefix、server_id/CID/KICK/LDM 与原生 expiry；另用Tinode+PG做相同身份模型对照，记录新增代码/依赖与不能薄适配之处。不是只跑ping。
2. **持久协议套件**：新增三类/两种来源授权矩阵、human 意图与 source_binding、广播 grant 撤销/到期、服务器签名标准向量与逐字段篡改、轮换/吊销、撤回乱序/离线/满盘、只入箱零模型及回应风暴门禁；增加 realm 归一/段数边界、组 10 node/100 local 一份正文、逐 local 部分回执、退组重入、分页崩溃/配额结算、广播拒权/静音和 ephemeral 丢弃用例；真实PostgreSQL/NATS、随机端口、两个fixture Host adapter，有状态机随机操作模型；枚举提交/ACK/kill点，以消息ID集合和副作用计数作oracle。不用 mock 数据库/邮箱和回执事务；不用 dispatcher 租约作为 oracle。
3. **宿主兼容套件（N1 普通会话，N2 子 agent）**：固定发行DSH build，两个隔离profile；普通现存、冷恢复、首次建会话、同DSH多session、recursive子agent、同recipient重复、缺provider、磁盘错误分别测试。fake模型只用于可复现故障；不得将其冒成真实模型闭环。新增同用户本地 A 与云端 B 的寻址 fixture：A1/A2/A3/A2c，B2 已有、B1 经系统首联新建；用每个 host_session_id 的收件/准入记录核对实际目标，N1 验每次询问、个人四种动作、本地 replies_only/thread_messages/receive_none、归档拒收、两层默认档位、同组织与白名单、撤销后已有会话重核、普通多会话与对子目标的明确拒绝，N2 再验证 A2c 专用路径，不把子会话占位地址当已兼容。
4. **负载/chaos**：逐档跑30分钟预热+2小时稳态；大档至少一次24小时浸泡。50%流量一个热点tenant、20%慢节点、1%重复/ACK丢失、5倍突发60秒；再做单节点滚更、Broker/PG故障和全连接重启。通过后不无理由重跑全部，只对相关变更重验。
5. **真实两DSH与真实模型闭环**：另行使用已授权预算，不在设计任务计费。A与B真实独立登录、不同工作目录；A1 查到 B/sys 并首联→B 系统按批准策略新建 B1→B1 持久接受并真实工具 reply 携完整地址→A1 原 local 会话收到→权威PG与两边Host证据相符。额外消息如模型重发必须如实计数。再验证断网/ACK丢失/进程恢复不重复已接受request。默认先验证总部待授权/本机用户允许与独立执行授权，未经允许模型调用数为 0。必须分别记录 contact 状态、platform_stored、Host receipt和实际工具回复，不能只看模型自然语言。N1 追加首联前目录不泄露 B1/B2、建会话后 B1 与非目标 B2 并存、A1/A2 同 DSH 的小样本，切换前台后回复仍回 A1；核其他 session 无该消息/准入记录。同线程多 local、首联限流、回复丢失和 local 高水位恢复也按矩阵验证，N2 子会话另行验收。
6. **发布门禁**：schema生成/兼容、单元/真实依赖集成、安全负向、HA档演练、容量证据、安装与回滚文档齐备；CI后fresh非作者独审，保护路径标【待审查】，本工人不提交/推送/合并。

后续脚本宜分`broker-admission`、`mailbox-model`、`host-compat`、`load-chaos`四类，各自输出版本/hash、随机种子、设置、原始首失、指标与exit code。避免测试专用开关放进生产安全路径；故障点用依赖/网络代理/进程边界注入。模拟driver不调LLM，真实模型只在最后小样本验收。

## 7. 观测和运维闭环

必须有广播 grant 授予/使用/拒绝/撤销/过期与冻结审计、sender/class 不匹配、human 意图重放、验签失败/未知 kid/密钥集新鲜度/签名延迟与错误、管理撤回生效延迟/待同步 node 数/隐藏失败、回应聚合与抑制风暴计数，以及组快照成员数/目标 node 数、派发页滞后与未派发年龄/费用预留及退款、成员退出拒收、逐成员接受/终态/未知聚合一致性、广播发布拒权/限频、ephemeral 丢失/重复/旧版本丢弃，以及本地拒收原因聚合/扫描阈值命中/屏蔽与解除/安全上报积压/审计故障/地址分配高水位异常、系统首联拒绝/待批准年龄/到期扫描滞后/冷却命中/审批提示去重/决定版本冲突/创建未知、地址关系拒绝/local 不复用违规、本地策略变更所致请求/关系状态上报延迟（不上传个人名单或会话树）、attempt_count/隔离截止滞后、admit 冲突/未知响应、终态空洞闭合时间、HEAD 合并/丢弃/route 缓存命中率、connections/认证失败/续期、NATS slow-consumer与pending、app active channels、订阅/冷会话数量、既有 outbox lag（独立依赖指标）、mailbox head 与 confirmed 差距、最老空洞年龄、head 提示/修复周期、FETCH 页大小/超时、pending bytes、delivery retry/quarantine、stale epoch、Host flush latency、unknown outcomes、授权RPC延迟与失败、FETCH 限流/公平份额、稀疏回执占用、KICK 成功/失败/延迟、PG事务/锁/WAL/磁盘指标。分开记录网络收到、Host接受、执行开始、执行终态时间。

总部仅有组织政策、请求决定状态及不透明地址级审计；插件保个人规则版本/操作者/决定范围和本地 session/grant/lineage 关联，不能以总部汇总指标冒细粒度血缘已核。指标标签限service/region/shard/outcome等有界集合；node/session/thread/message ID放受控日志/trace，不做Prometheus高基数label。不记录payload、token、完整票据或敏感路径。管理员只能按权限查询自己tenant的诊断；系统总部不天然能看所有人历史。

runbook需能回答：哪个故障域阻断、哪些message已提交、哪些只是transport收到、哪个receipt不确定、是否仍能安全重投、谁能解除quarantine、剩余磁盘与积压预计耗时。恢复先修权威存储和身份，再让节点按 FETCH 窗口限速排空；不能通过删队列/重置cursor消除告警。
