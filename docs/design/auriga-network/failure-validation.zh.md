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

## 2. 容量模型：先数对对象

用变量区分用户U、物理DSH节点D、历史会话H、活跃授权逻辑通道A、应用订阅S、当前实际运行的agent R、消息发布率λ、平均fanout F、正文B、未确认积压Q。物理网络连接C默认=D，和H/A/R不成一一对应。用户可有多个DSH；冷会话只在PG保记录，不保连接/心跳/执行worker。

以下是可调的三档**示例估算**：每用户2个DSH、每DSH20个活跃逻辑通道、H=10A、S=2A，活跃通道平均每50秒发1条（λ=0.02A），F=1.5，B=2KiB。并非承诺每个agent都能同时跑LLM；R和模型容量独立。

| 参数 | 小档 | 中档 | 大档 |
|---|---:|---:|---:|
| 活跃逻辑通道/agent量级A | 1,000 | 10,000 | 100,000 |
| 用户U / DSH节点D | 25 / 50 | 250 / 500 | 2,500 / 5,000 |
| 历史会话H | 10,000 | 100,000 | 1,000,000 |
| 应用订阅S | 2,000 | 20,000 | 200,000 |
| 数据网络物理连接C | 50 | 500 | 5,000 |
| 平均发布λ（消息/秒） | 20 | 200 | 2,000 |
| 下行delivery率λF | 30 | 300 | 3,000 |
| 下行纯正文带宽λFB | 60KiB/s | 600KiB/s | 5.86MiB/s |

NATS Broker实际每DSH仅少量上下行subject订阅，不是表中每个应用订阅一个consumer。MCP/HTTPS有界池另计，h2目标1条、h1最多2条/DSH；因此不能把数据连接C写成全系统所有TCP连接数。TLS/协议/ACK和重投带宽初估再加50%，突发另测5倍，不将压缩收益写进预算。

**敏感性**：100k逻辑会话若集中在500个DSH而非5,000，物理连接少10倍但每DSH公平性、宿主磁盘和事件循环压力增10倍；若每DSH只有1个活跃会话，则100k逻辑会话意味着100k连接，必须另测该物理连接档。统一用户身份不能把这几种形态混报为“支持10万agent”。

## 3. 资源预算与公式

这些单位成本是选型假设，后续用heap/pprof、RSS、GC、FD、WAL与延迟替换，不是引用上游营销吞吐。

- **内存**：每物理连接空闲128KiB（TLS/库/缓冲/goroutine等合计占位），每活跃logical channel 512B、每应用subscription 256B。大档连接约625MiB，channel约48.8MiB、subscription约48.8MiB。若每DSH平均在途buffer64KiB，再加312.5MiB，合计约1.01GiB；尚不含进程基线、GC余量、NATS路由/JS缓存、PostgreSQL和宿主LLM。按每连接硬窗口1MiB全部占满则另需4.88GiB，不能按典型值配置硬上限或一次性预分配。多进程总量与单副本都要测。Broker max_pending 显式建议 2MiB，大档 5,000 连接全部触顶仅这一项约 9.77GiB；adapter 2MiB/node 又是约 9.77GiB，不包含在前述典型 buffer 估算中。SDK、TLS、内核 socket 还有独立开销，不能以单连接 cap 当整机上限。
- **单接入Broker规划起点**：2k连接时空闲预算约250MiB、平均buffer125MiB；先配置4vCPU/2GiB测试机、FD上限至少16k，测50%与75%负载点。此为测试起点，不是额定规格；2k 连接的 Broker pending 若都达到 2MiB 已约 3.91GiB，不能在这台 2GiB 测试机上承诺该极端档。须按总内存预算降低准入连接数/缓冲上限或增配，并测试慢消费者断连。大档建议至少3接入副本、任一故障后余下节点仍≤已测安全上限70%；是否能共用内部JS节点由隔离压测决定。
- **FD**：Broker约`C_local + cluster routes + internal clients + stores/logs + margin`；edge反代通常每数据连接占客户端/上游两端FD，约`2C_local`。5k连接edge是约10k基础FD，另算HTTPS池与滚更双连接。不能只调Broker ulimit。
- **CPU**：若待测纯adapter处理每delivery为0.2ms，大档3000/s仅此项约0.6核；TLS/JSON/校验/GC、IAM/org与DB另算，不能据此宣称1核够。握手成本、正文 16KiB 上限和大fanout分别测，不用平均2KiB掩盖极限。
- **身份依赖**：源发送按 2 次权威调用，每 delivery 取页核权与回执各 2 次，基础为 `2λ+4λF`，大档约 16k 次/秒。处理尝试和执行准入是新增必经检查；若每项都触发宿主执行且两步各需 2 次 IAM/org 调用，另加 `4λF=12k`，合计约 28k 次/秒。重试可进一步放大，最多 5 次是每项累计上限，不是可无限维持的稳态吞吐。批量核验或安全复用校验结果的收益必须实测，不用全租户缓存绕过吊销。
- **PG 操作**：FETCH/RECEIPT 各平均每批 16 条（编码 ≤64KiB），基础 `λ+2λF/16` 为约 2,375 次/秒，其中写 2,187.5、读 187.5。每项第一次处理尝试登记和执行准入各增加一个短写事务；全部 delivery 均执行时再加 6,000，达到约 8,375 次/秒。仍未计提示查 route、配额维护、到期任务与既有 outbox，不能把基础数当完整容量。
- **提示路径查询**：令 H 为各 adapter 合并后的 HEAD 下发总率，R 为 adapter 副本数、D_active 为活跃 node 数、缓存 TTL T=2 秒。稳定路由且无失效时，route 查询率近似受 min(H, R×D_active/T) 限制；接管/失效和冷缓存会升高，保守不计缓存时预算按 λF=3,000 次/秒。100ms 合并只减少同 node 的密集提示，平均低频流量可能几乎无收益；不能假定所有提示被合并。三类操作合计大档保守约 11,375 次/秒（写 8,187.5、读 3,187.5），不含重试。
- **修复与到期查询**：大档全部 5,000 node 在线积压时，256 node/页，一轮至少 ceil(5000/256)=20 个批查询；每 60 秒约 0.33 次/秒，单扫描器才是此数，多副本重复扫描乘实际数量。修复页批量带 route/head，避免另按 node 查 route；每轮仍可能发出 5,000 个 HEAD。到期任务对 Pending/Quarantined 的索引批处理、稀疏区间合并及局部限额更新另测。提示查询无需逐条调用用户 IAM，但只能返回内部受限路由与无正文提示；FETCH/admit 权威校验不缓存。线程/邮箱热点锁可能比总 TPS 更先限速。
- **心跳**：NATS keepalive目标空闲30s一次、两次未应答判异常，约`C/30` PING/s；大档约167/s，加PONG约334条/s。由官方客户端/Broker统一配置，不另给每会话心跳。另有node 级应用健康更新可合并进有业务的状态消息，最多同30s级预算；PG route续期每批256，避免每node一事务。
- **刷新重连**：按连接凭据一小时、平均提前 90 秒续接的 `C/3510`，大档约 1.42 握手/s，须计票据/IAM/TLS。全站重启重连full jitter 0.5s指数至60s；限入100次/s为示例，大档至少50s才能全部重建，目标120s内99%恢复需留故障与授权余量。429/Retry-After控制节奏，不能同时回零等待。访问 token 刷新独立计算，不应触发每四分钟 WSS 重连；若实际 IAM 网络授权更短，必须按真实剩余期限重算。
- **积压**：离线1小时、全部接收端离线时`Q=λF×3600`；大档10.8M deliveries，单正文即约22.1GB（十进制），加行/索引/去重/事务WAL更多。默认node队列10k条或64MiB取先到，tenant100k条或1GiB取先到，只是保守初始配额；大档持续离线会明确拒收，不能同时声称无限留7天。
- **磁盘**：按每源消息正文2KiB +消息/dedup/既有事件元数据约1.5KiB + F×1KiB delivery，合计约5KiB；索引/膨胀初估×2为10KiB/源消息。持续全天小/中/大档分别约17.7GB/177GB/1.77TB每日逻辑占用；7天约124GB/1.24TB/12.4TB。再加PG副本、WAL/PITR、备份，既有 JS 事件另计，不把它当本方案正文备份。该 7 天情景不是运营默认值。
- **中档留存与缓解**：首选缩短已接受正文留存至 24 小时；有长期历史需求则将正文外置到授权对象存储，PG 保留摘要和引用。上述中档 1.24TB/7 天中，正文按 ×2 分摊约 495GB，其余约 743GB；正文改 1 天、其余暂按 7 天，仅该口径约 814GB，说明外置正文也不能解决全部成本。真实默认去重/终态元数据至少 30 天，须按实际行宽另算，不能把 814GB 当磁盘采购总量。大档正文分摊约 4.95TB/7 天；缩短或外置优先于无界扩大 PG。外置要先完成对象耐久写，再事务提交引用；校验 digest、读取重授权、孤儿回收和备份一致性，不能让 PG 确认指向未保存对象。待投未接受正文仍保留至截止或明确终态，不能随 24 小时策略提前删。

扩容触发看最先到达的资源：adapter/授权CPU持续>60%、P99越标、线程锁/IO延迟、pending年龄、存储>70%、连接断开率、单租户公平份额被侵占。扩Broker不能修PG写瓶颈；加CPU不能修无界重试。

## 4. 可靠性与性能目标分别验收

**正确性门禁（无可容忍计数）**：在声明故障模型内，已platform_stored且未过期/显式终态的消息丢失数=0；跨tenant/无权binding交付=0；旧 epoch 成功的新 FETCH/发件/执行准入=0（受限回执对账不是新操作）；重复宿主request准入=0（限定受支持adapter与持久日志可核场景）；未知外部副作用被自动重做数=0。任一失败不以P99或“99.9%成功”掩盖。

**首发性能目标**：在同区域RTT≤50ms、payload≤2KiB、fanout≤2、资源低于70%、身份与存储正常且未积压时，提交响应P95≤250ms/P99≤750ms；commit→在线 Node 收到 FETCH_RESULT 正文 P95≤500ms/P99≤2s（不能只量 HEAD 提示）；收到→host持久接受P95≤1s/P99≤5s，不含LLM排队/运行。应按多个tenant同时测，不只平均。

**恢复目标**：单连接网络恢复后，在无大积压条件下P99≤10s恢复READY/业务游标；丢通知修复默认 60 秒周期、一轮扫描 ≤30 秒、取页恢复预算 10 秒；仅在下一轮修复 HEAD 未再次丢失、依赖健康且无大积压时，恢复预算 100 秒、用例 deadline 120 秒。若前 k 轮修复提示继续丢失，第 k+1 轮成功，则预算 100+60k 秒；再丢一轮预算 160 秒、deadline 180 秒。持续丢失无有限上限；网关/Broker滚更或全站重启后，目标120s内99%节点恢复，剩余有明确错误。积压清空时间下界`Q/(可用消费率-新到达率)`，分母≤0时永远清不完，必须限入/扩容，不能给固定恢复承诺。

**HA目标**：生产HA档月度平台消息准入可用性99.9%，分别计自身故障、IAM/org、PG、Broker依赖；用户终端长期离线另计，不能通过全排除依赖使数字变好看。单机档不继承HA可用性承诺。同步PG档已提交数据目标RPO=0（限定一故障域失效且同步副本幸存）；异步备份灾难恢复必须披露可丢窗口。PG failover目标RTO≤60s、整服务≤120s均待演练，未知故障不承诺这些数值。

## 5. 故障注入矩阵

每项用随机fixture、固定种子、故障点钩子与有界deadline；记录接受ID集合、PG状态、Host ledger及实际副作用计数。隔离网络代理注入丢包/延迟，禁止修改用户系统网络/防火墙。

| 故障/操作 | 必须观察的结果与过关条件 |
|---|---|
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
| 克隆 A(epoch 5) 已 RECEIVED，B(epoch 6) 接管先于 A admit | A 的在线 admit 返回 stale_epoch，不调用 prompt；B 取得唯一准入。A 离线/仅持本地 ledger 同样不得执行，副作用计数最多一次。 |
| A admit 先提交，B 随后接管；A 响应丢失或 prompt 前崩溃 | B 不获第二许可；A 重启查询不再次发起 prompt。无法确认原执行则持久 Unresolved；允许零次执行，不以“至少执行一次”掩盖重复风险。 |
| 每小时续接 / 旧 epoch 页面或 RECEIVED 项 | 无准入项用新连接重新在线 admit；已有 admission 只对账，不因换 epoch 清除。旧 ingress 回执仅受限补录，conflict 检测不是唯一防重手段。 |
| admit 与 cancel/expiry 并发、IAM/连接不可用 | 同一执行记录版本 CAS 决定胜者；未获准只保留 RECEIVED。取消先提交则不执行；admit 已提交的在途动作不能宣称被网络撤回。 |
| 101 毒消息、102…N 属其他正常线程 | 101 隔离期间其他项继续接受，连续后缀合并精确区间，不能覆盖 101；隔离到期/撤销/有权 skip 提交后前缀跨过。其他线程不因 A 的局部限额停止。 |
| 隔离项无人处理、node 离线、原期限早于隔离期限 | 平台按 min(expires_at, quarantine_at+24h) 禁止新准入，60 秒目标批处理持久终态；原 TTL 不延长，不依赖客户端回连。 |
| deliver 连续使插件崩溃 / ATTEMPT_BEGIN 响应丢失 | 平台次数在进入 deliver 前提交；本地 entered 持久化后才调用，重复响应不二次进入。重启不退款；无执行准入的崩溃最多自动 5 次，下一请求隔离，不靠 FETCH 次数或本地重置绕过。 |
| 本线程跨度 1,024 / 稀疏区间 256 与 node 总区间 4,096 | 局部阈值只拒该 thread/binding 新消息；node 全局未满时其他线程仍服务。区间精确合并，保留 256 个在途回执增量预算，任何阈值不得拒绝必要终态回收或伪造已接受。 |
| 本地 outcome_unknown、平台 Unresolved 提交前后 | 本地报告不推进游标；平台原子写执行未知、停止再准入与传输 tombstone 后才跨过空洞。查询仍显示未知，迟到结果可补录，不触发自动重做。 |
| held_ids / 暂停标记重复、越权、过大或重启丢失 | 有权有界集合只抑制本次重复正文，不清 pending；越权/超限拒绝。遗失排除集可重发，已持久项去重，其他线程可继续扫描。 |
| 撤销后 KICK 成功/失败、CID 复用、旧 KICK 延迟 | 后续准入立即按权威校验拒绝；系统面可用时断连目标 ≤5 秒。错误/旧 server 运行世代映射不得踢错连接；失败有界重试、到期兜底，不恢复授权。 |
| 私有 inboxPrefix 的 request()/mux/noMux/任意 reply | 仅当前 ingress 私有响应订阅可用；跨 ingress、全局 inbox、系统 subject 均拒绝，超时/重连释放订阅，身份不能由 reply 推断。 |
| 网络 JWT 与 HTTP 访问 token 分别到期 | 访问 token 正常刷新不切断健康 WSS；网络 JWT 到期前随机续接且原生过期关闭；IAM 会话撤销即拒新操作，不以一小时剩余期放行。 |
| 重用已兑换ticket/伪造up subject/任意reply | Broker或adapter明确拒绝；无法伪造source binding，模型无法获得票据。 |
| 订阅其他tenant、`>`、`$SYS`、`$JS.API` | 全部拒绝，日志无凭据；不能从错误中泄漏他人正文/存在性。 |
| IAM/org故障、到期、退出、设备/成员/父grant吊销 | 新业务失败关闭；旧队列不新启动，子grant不自动恢复；在途边界准确报告。 |
| PG主故障/同步副本不足/读到旧route | 无新成功耐久ACK；单写者failover与epoch正确；不同DB主不能并发接受同node。 |
| 既有 JetStream 不可用但 Core 正常 | HEAD/FETCH/RECEIPT 正常；新投递不依赖 JS。既有事件有界积压，若共享存储阈值触发则明确限入，不能掩盖资源耦合。 |
| 整个 NATS 集群不可用 | 只有 PG 有界存储，不能声称在线 push；恢复后 READY/游标取页，按窗口恢复。 |
| 磁盘满/WAL失败/只读文件系统 | 不返回Host/PG成功ACK；保留已确认数据，ACK/回收预留不能被正文吃完。 |
| Host prompt后flush前kill | 恢复查原request；已发生副作用且结果不明进入unknown，不把无ACK当未执行。 |
| Host flush后ledger前kill | 从宿主持久历史/inbox重建同receipt；不能随机换requestId。 |
| Host ledger 提交后回执或提交响应丢失 | 当前有效连接重送同 receipt，不需新 token、不二次 prompt；PG 幂等回原状态。 |
| 同session多thread并发/重复消息 | session准入串行；每thread入队顺序正确，其他session不被锁死。 |
| 非默认 cwd/冷 session/开线程回包前首条消息 | 同 binding 恢复，不 create 冲突、不另造会话；预留映射与响应原子合并，同一逻辑请求只有一个宿主 session。 |
| recursive subagent/父不在线/归档 | 真实parent-address adapter；不绕普通API拒绝，不默认继承权限。未支持明确host_incompatible。 |
| poison/不支持schema/超大包 | 隔离对应delivery/channel、有错误详情；不无限重投拖全tenant。 |
| 慢DSH占满窗口/一个热root | 内存稳定有界，其他tenant P99不越正常目标2倍；同用户其他DSH仍服务。 |
| JS socket不读/断线持续publish/认证票据过期 | SDK内部、WebSocket bufferedAmount和应用队列都有可证明上界；断线停止入SDK，超限明确失败。同步callback不做异步获票、不throw；不能用pendingLimit名字冒硬cap。 |
| fanout32与超上限/一目标无权 | 原子上限内全成功或全拒；分批模式逐目标状态，不谎报全体成功。 |
| cancel与start竞争/过期与迟到receipt竞争 | CAS只形成可解释终态；已started不能报“保证未执行”。 |
| 时间偏差±60s/证书和JWT轮换 | 不放宽有效期，凭据轮换后恢复，安全错误可诊断。 |
| 客户端 cursor 回退、超前、超保留窗口或伪造 continuation | 回退对账 PG 已确认前缀，不重执行；超前不删 pending；伪造分页位置拒绝；过期明确 gap/resume_unavailable，不能按 max seen 确认。 |
| 删除过期去重后重放老producer_epoch | 拒绝新写，不重新执行；元数据清理验证最大重试窗口。 |
| 100%连接同时重启/身份服务限流 | full jitter/入场预算生效，恢复达目标或清楚说明资源不足，无无限CPU/内存增长。 |

## 6. 可落地的自动化与真实验收顺序

以下仅为用户后续授权后的验收顺序，当前不运行任何 spike、服务或模型。宿主接缝与网络接入分开推进，没有前者必须等待后者的依赖。

1. **宿主 adapter 独立门禁 + 接入 spike**：先用 fixture 传输调用 `deliver(page) → receipts`，核映射竞争、冷恢复与持久证据；独立接入侧核 NATS官方Go/JS客户端、真实Broker、隔离IAM/org fixture；负向 subject 矩阵、私有 inboxPrefix、server_id/CID/KICK/LDM 与原生 expiry；另用Tinode+PG做相同身份模型对照，记录新增代码/依赖与不能薄适配之处。不是只跑ping。
2. **持久协议套件**：真实PostgreSQL/NATS、随机端口、两个fixture Host adapter，有状态机随机操作模型；枚举提交/ACK/kill点，以消息ID集合和副作用计数作oracle。不用 mock 数据库/邮箱和回执事务；不用 dispatcher 租约作为 oracle。
3. **宿主兼容套件（N1 普通会话，N2 子 agent）**：固定发行DSH build，两个隔离profile；普通现存、冷恢复、首次建会话、同DSH多session、recursive子agent、同delivery重复、缺provider、磁盘错误分别测试。fake模型只用于可复现故障；不得将其冒成真实模型闭环。
4. **负载/chaos**：逐档跑30分钟预热+2小时稳态；大档至少一次24小时浸泡。50%流量一个热点tenant、20%慢节点、1%重复/ACK丢失、5倍突发60秒；再做单节点滚更、Broker/PG故障和全连接重启。通过后不无理由重跑全部，只对相关变更重验。
5. **真实两DSH与真实模型闭环**：另行使用已授权预算，不在设计任务计费。A与B真实独立登录、不同工作目录；A原生工具开线程→B宿主持久接受并真实工具reply→A原绑定会话收到→权威PG与两边Host证据相符。额外消息如模型重发必须如实计数。再验证断网/ACK丢失/进程恢复不重复已接受request。必须分别记录platform_stored、Host receipt和实际工具回复，不能只看模型自然语言。
6. **发布门禁**：schema生成/兼容、单元/真实依赖集成、安全负向、HA档演练、容量证据、安装与回滚文档齐备；CI后fresh非作者独审，保护路径标【待审查】，本工人不提交/推送/合并。

后续脚本宜分`broker-admission`、`mailbox-model`、`host-compat`、`load-chaos`四类，各自输出版本/hash、随机种子、设置、原始首失、指标与exit code。避免测试专用开关放进生产安全路径；故障点用依赖/网络代理/进程边界注入。模拟driver不调LLM，真实模型只在最后小样本验收。

## 7. 观测和运维闭环

必须有 attempt_count/隔离截止滞后、admit 冲突/未知响应、终态空洞闭合时间、HEAD 合并/丢弃/route 缓存命中率、connections/认证失败/续期、NATS slow-consumer与pending、app active channels、订阅/冷会话数量、既有 outbox lag（独立依赖指标）、mailbox head 与 confirmed 差距、最老空洞年龄、head 提示/修复周期、FETCH 页大小/超时、pending bytes、delivery retry/quarantine、stale epoch、Host flush latency、unknown outcomes、授权RPC延迟与失败、FETCH 限流/公平份额、稀疏回执占用、KICK 成功/失败/延迟、PG事务/锁/WAL/磁盘指标。分开记录网络收到、Host接受、执行开始、执行终态时间。

指标标签限service/region/shard/outcome等有界集合；node/session/thread/message ID放受控日志/trace，不做Prometheus高基数label。不记录payload、token、完整票据或敏感路径。管理员只能按权限查询自己tenant的诊断；系统总部不天然能看所有人历史。

runbook需能回答：哪个故障域阻断、哪些message已提交、哪些只是transport收到、哪个receipt不确定、是否仍能安全重投、谁能解除quarantine、剩余磁盘与积压预计耗时。恢复先修权威存储和身份，再让节点按 FETCH 窗口限速排空；不能通过删队列/重置cursor消除告警。
