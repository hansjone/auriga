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

- **内存**：每物理连接空闲128KiB（TLS/库/缓冲/goroutine等合计占位），每活跃logical channel 512B、每应用subscription 256B。大档连接约625MiB，channel约48.8MiB、subscription约48.8MiB。若每DSH平均在途buffer64KiB，再加312.5MiB，合计约1.01GiB；尚不含进程基线、GC余量、NATS路由/JS缓存、PostgreSQL和宿主LLM。按每连接硬窗口1MiB全部占满则另需4.88GiB，不能按典型值配置硬上限或一次性预分配。多进程总量与单副本都要测。
- **单接入Broker规划起点**：2k连接时空闲预算约250MiB、平均buffer125MiB；先配置4vCPU/2GiB测试机、FD上限至少16k，测50%与75%负载点。此为测试起点，不是额定规格。大档建议至少3接入副本、任一故障后余下节点仍≤已测安全上限70%；是否能共用内部JS节点由隔离压测决定。
- **FD**：Broker约`C_local + cluster routes + internal clients + stores/logs + margin`；edge反代通常每数据连接占客户端/上游两端FD，约`2C_local`。5k连接edge是约10k基础FD，另算HTTPS池与滚更双连接。不能只调Broker ulimit。
- **CPU**：若待测纯adapter处理每delivery为0.2ms，大档3000/s仅此项约0.6核；TLS/JSON/校验/GC、IAM/org与DB另算，不能据此宣称1核够。握手成本、payload16KiB上限和大fanout分别测，不用平均2KiB掩盖极限。
- **身份依赖**：最低按源发送2次权威调用、每delivery领取及ACK各2次估算，`2λ+4λF`，大档约16k次/s；若执行准入另2次，再加6k/s。当前多层重复验证可能更高。必须压测IAM/org，优先新增受控批量核验接口且逐项拒绝，不用全tenant缓存绕过撤销。网络Broker吞吐充足并不意味着授权服务充足。
- **PG事务**：批量领取/ACK各16条，理想近似`λ + 2λF/16`，大档约2375事务/s，不含grant/路线/配额与outbox。线程/根树热点锁可能比总TPS先限速。接口授权与外部发布不能长占业务事务。
- **心跳**：NATS keepalive目标空闲30s一次、两次未应答判异常，约`C/30` PING/s；大档约167/s，加PONG约334条/s。由官方客户端/Broker统一配置，不另给每会话心跳。另有node级应用健康/窗口更新可合并进有业务的状态消息，最多同30s级预算；PG route续期每批256，避免每node一事务。
- **刷新重连**：按每4分钟一次`C/240`，大档约20.8握手/s，须计票据/IAM/TLS。全站重启重连full jitter 0.5s指数至60s；限入100次/s为示例，大档至少50s才能全部重建，目标120s内99%恢复需留故障与授权余量。429/Retry-After控制节奏，不能同时回零等待。
- **积压**：离线1小时、全部接收端离线时`Q=λF×3600`；大档10.8M deliveries，单正文即约22.1GB（十进制），加行/索引/去重/事务WAL更多。默认node队列10k条或64MiB取先到，tenant100k条或1GiB取先到，只是保守初始配额；大档持续离线会明确拒收，不能同时声称无限留7天。
- **磁盘**：按每源消息正文2KiB +消息/dedup/outbox元数据约1.5KiB + F×1KiB delivery，合计约5KiB；索引/膨胀初估×2为10KiB/源消息。持续全天小/中/大档分别约17.7GB/177GB/1.77TB每日逻辑占用；7天约124GB/1.24TB/12.4TB。再加PG副本、WAL/PITR、备份，不把JS仅存通知估成零成本。高吞吐不等于低存储成本，留存/正文外置和配额是部署选择。

扩容触发看最先到达的资源：adapter/授权CPU持续>60%、P99越标、线程锁/IO延迟、pending年龄、存储>70%、连接断开率、单租户公平份额被侵占。扩Broker不能修PG写瓶颈；加CPU不能修无界重试。

## 4. 可靠性与性能目标分别验收

**正确性门禁（无可容忍计数）**：在声明故障模型内，已platform_stored且未过期/显式终态的消息丢失数=0；跨tenant/无权binding交付=0；旧epoch成功新操作=0；重复宿主request准入=0（限定受支持adapter与持久日志可核场景）；未知外部副作用被自动重做数=0。任一失败不以P99或“99.9%成功”掩盖。

**首发性能目标**：在同区域RTT≤50ms、payload≤2KiB、fanout≤2、资源低于70%、身份与存储正常且未积压时，提交响应P95≤250ms/P99≤750ms；commit→在线Node收到P95≤500ms/P99≤2s；收到→host持久接受P95≤1s/P99≤5s，不含LLM排队/运行。应按多个tenant同时测，不只平均。

**恢复目标**：单连接网络恢复后，在无大积压条件下P99≤10s恢复READY/业务游标；丢通知repair默认30s周期、P99≤60s发现并重新派发；网关/Broker滚更或全站重启后，目标120s内99%节点恢复，剩余有明确错误。积压清空时间下界`Q/(可用消费率-新到达率)`，分母≤0时永远清不完，必须限入/扩容，不能给固定恢复承诺。

**HA目标**：生产HA档月度平台消息准入可用性99.9%，分别计自身故障、IAM/org、PG、Broker依赖；用户终端长期离线另计，不能通过全排除依赖使数字变好看。单机档不继承HA可用性承诺。同步PG档已提交数据目标RPO=0（限定一故障域失效且同步副本幸存）；异步备份灾难恢复必须披露可丢窗口。PG failover目标RTO≤60s、整服务≤120s均待演练，未知故障不承诺这些数值。

## 5. 故障注入矩阵

每项用随机fixture、固定种子、故障点钩子与有界deadline；记录接受ID集合、PG状态、Host ledger及实际副作用计数。隔离网络代理注入丢包/延迟，禁止修改用户系统网络/防火墙。

| 故障/操作 | 必须观察的结果与过关条件 |
|---|---|
| send提交前崩溃/回滚 | 不存在platform_stored；同键重试只形成一条消息。 |
| PG提交后、响应丢失 | 同键返回原message；异体同键冲突，message/delivery/dedup数量不增。 |
| commit后afterCommit/NOTIFY丢失 | dirty/outbox仍在，repair≤60s发现；无按连接短poll。 |
| JS PubAck后outbox标记前宕机 | 可重复事件；不重复业务message/Host request。 |
| JS消费ACK后adapter崩溃 | PG dirty/租约仍驱动恢复，不因通知已ACK丢投递。 |
| Core下行故意丢弃/客户端缓冲溢出 | 未Host receipt不清pending；同delivery重新领取，epoch/cursor无空洞。 |
| WSS单向黑洞/半开/代理空闲断连 | native heartbeat/timeout发现；bounded jitter重连，原request身份不变。 |
| 两Broker中接入节点崩溃 | 官方客户端重新接入，重新认证/READY、补投；不粘死原节点。 |
| adapter滚更/dispatcher分片接管 | socket可继续；旧owner generation不能claim，新owner批恢复，无全网广播。 |
| 两连接同node/旧socket迟到 | 新epoch唯一；新正文不发旧down subject，旧ACK拒绝；短期接管风暴进入conflict。 |
| 重用已兑换ticket/伪造up subject/任意reply | Broker或adapter明确拒绝；无法伪造source binding，模型无法获得票据。 |
| 订阅其他tenant、`>`、`$SYS`、`$JS.API` | 全部拒绝，日志无凭据；不能从错误中泄漏他人正文/存在性。 |
| IAM/org故障、到期、退出、设备/成员/父grant吊销 | 新业务失败关闭；旧队列不新启动，子grant不自动恢复；在途边界准确报告。 |
| PG主故障/同步副本不足/读到旧route | 无新成功耐久ACK；单写者failover与epoch正确；不同DB主不能并发接受同node。 |
| JS不可用但Core正常 | PG有界积压，repair降级可运行；超阈值明确拒收。 |
| 整个NATS集群不可用 | 只有PG有界存储，不能声称在线push；恢复后按游标/dirty排队。 |
| 磁盘满/WAL失败/只读文件系统 | 不返回Host/PG成功ACK；保留已确认数据，ACK/回收预留不能被正文吃完。 |
| Host prompt后flush前kill | 恢复查原request；已发生副作用且结果不明进入unknown，不把无ACK当未执行。 |
| Host flush后ledger前kill | 从宿主持久历史/inbox重建同receipt；不能随机换requestId。 |
| Host ledger提交后ACK丢失 | 新租约提交同receipt，不二次prompt；PG终态幂等。 |
| 同session多thread并发/重复消息 | session准入串行；每thread入队顺序正确，其他session不被锁死。 |
| 非默认cwd/冷session恢复 | 同binding恢复，不create冲突、不另造新会话。 |
| recursive subagent/父不在线/归档 | 真实parent-address adapter；不绕普通API拒绝，不默认继承权限。未支持明确host_incompatible。 |
| poison/不支持schema/超大包 | 隔离对应delivery/channel、有错误详情；不无限重投拖全tenant。 |
| 慢DSH占满窗口/一个热root | 内存稳定有界，其他tenant P99不越正常目标2倍；同用户其他DSH仍服务。 |
| JS socket不读/断线持续publish/认证票据过期 | SDK内部、WebSocket bufferedAmount和应用队列都有可证明上界；断线停止入SDK，超限明确失败。同步callback不做异步获票、不throw；不能用pendingLimit名字冒硬cap。 |
| fanout32与超上限/一目标无权 | 原子上限内全成功或全拒；分批模式逐目标状态，不谎报全体成功。 |
| cancel与start竞争/过期与迟到receipt竞争 | CAS只形成可解释终态；已started不能报“保证未执行”。 |
| 时间偏差±60s/证书和JWT轮换 | 不放宽有效期，凭据轮换后恢复，安全错误可诊断。 |
| cursor过大/过旧/乱序/丢ACK | 不按max seen推进；有权对账，过旧明确gap，不静默跳过。 |
| 删除过期去重后重放老producer_epoch | 拒绝新写，不重新执行；元数据清理验证最大重试窗口。 |
| 100%连接同时重启/身份服务限流 | full jitter/入场预算生效，恢复达目标或清楚说明资源不足，无无限CPU/内存增长。 |

## 6. 可落地的自动化与真实验收顺序

1. **选型spike**：NATS官方Go/JS客户端、真实Broker、隔离IAM/org fixture；负向subject矩阵与原生expiry；另用Tinode+PG做相同身份模型对照，记录新增代码/依赖与不能薄适配之处。不是只跑ping。
2. **持久协议套件**：真实PostgreSQL/NATS、随机端口、两个fixture Host adapter，有状态机随机操作模型；枚举提交/ACK/kill点，以消息ID集合和副作用计数作oracle。不用mock数据库/租约事务。
3. **宿主兼容套件**：固定发行DSH build，两个隔离profile；普通现存、冷恢复、首次建会话、同DSH多session、recursive子agent、同delivery重复、缺provider、磁盘错误分别测试。fake模型只用于可复现故障；不得将其冒成真实模型闭环。
4. **负载/chaos**：逐档跑30分钟预热+2小时稳态；大档至少一次24小时浸泡。50%流量一个热点tenant、20%慢节点、1%重复/ACK丢失、5倍突发60秒；再做单节点滚更、Broker/PG故障和全连接重启。通过后不无理由重跑全部，只对相关变更重验。
5. **真实两DSH与真实模型闭环**：另行使用已授权预算，不在设计任务计费。A与B真实独立登录、不同工作目录；A原生工具开线程→B宿主持久接受并真实工具reply→A原绑定会话收到→权威PG与两边Host证据相符。额外消息如模型重发必须如实计数。再验证断网/ACK丢失/进程恢复不重复已接受request。必须分别记录platform_stored、Host receipt和实际工具回复，不能只看模型自然语言。
6. **发布门禁**：schema生成/兼容、单元/真实依赖集成、安全负向、HA档演练、容量证据、安装与回滚文档齐备；CI后fresh非作者独审，保护路径标【待审查】，本工人不提交/推送/合并。

后续脚本宜分`broker-admission`、`mailbox-model`、`host-compat`、`load-chaos`四类，各自输出版本/hash、随机种子、设置、原始首失、指标与exit code。避免测试专用开关放进生产安全路径；故障点用依赖/网络代理/进程边界注入。模拟driver不调LLM，真实模型只在最后小样本验收。

## 7. 观测和运维闭环

必须有connections/认证失败/续期、NATS slow-consumer与pending、app active channels、订阅/冷会话数量、outbox lag、dirty age、pending bytes、delivery retry/quarantine、stale epoch、Host flush latency、unknown outcomes、授权RPC延迟与失败、dispatcher公平份额、PG事务/锁/WAL/磁盘指标。分开记录网络收到、Host接受、执行开始、执行终态时间。

指标标签限service/region/shard/outcome等有界集合；node/session/thread/message ID放受控日志/trace，不做Prometheus高基数label。不记录payload、token、完整票据或敏感路径。管理员只能按权限查询自己tenant的诊断；系统总部不天然能看所有人历史。

runbook需能回答：哪个故障域阻断、哪些message已提交、哪些只是transport收到、哪个receipt不确定、是否仍能安全重投、谁能解除quarantine、剩余磁盘与积压预计耗时。恢复先修权威存储和身份，再让dispatcher限速排空；不能通过删队列/重置cursor消除告警。
