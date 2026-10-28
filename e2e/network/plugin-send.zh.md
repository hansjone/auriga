# SEND-3 默认插件最小收发

本线沿默认 device-connection 装配生产发送工具、会话接收控制、耐久发件箱与收件箱。
仅普通在线 session 的 text 单播；用户在原生 Auriga 设置页逐会话开启“接收消息”，
默认 receive_none。没有首联、好友关系、human、组播或模型自动回复。

插件在 HTTPS 前 fsync/rename 原 SubmitRequest、producer epoch/key、来源引用与本机
绑定/typed-local 快照；未知结果保留原键，同一会话重复同体工具调用先对账原操作。
跨重连/重启恢复同一请求，已知结果保留完整原字节。没有自动清理，未知项有128条上限。
producer_epoch 只接受签名 READY，首次随账本保存，后续不允许更换。

接收通过鉴权本机控制→receive_set 六字段登记见证→签名结果→账本开关。
发送模型不能控制接收开关。关闭的未确认请求立即阻止本机新输入，设置页可原操作重试。
FETCH 验签页头、完整 manifest 和每个条目，按不透明 continuation 翻页；不把 scan_after
当确认游标。只处理本轮普通单播；其他条目保留并报告不支持，不伪造成功或跳过屏障。
attempt/admit、宿主 invocation 与接受证据均先写账本；真实 HostPort 经公开
sessionController/SessionPersistence 接收、flush 和回读后才提交 host_accepted。
RECEIPT.expected_state_version 来自服务端 recipient.state_version；本地 revision 不参与 CAS。
already_admitted 不给予第二次宿主调用许可；未知调用只查真实请求/持久屏障。

经验签的 request-scope overloaded/retryable=true 在生产 mailbox 交换中至多5次原字节发送，
100/200/400/800ms 退避并遵守 retry_after、原5秒期限、取消和连接代际。
坏签名、异受众、确定拒绝不重试；持续过载保留明确 reason/attempts/retry hints。
HTTP 提交失败保留原请求，交由同体工具重试或新连接对账，不自动新建 key。

回执确认丢失后保留原 operation/proof。若新 READY 因服务端游标已推进而给出签名
recovery_conflict，只在本机每个连续序号都保有完整接受/回执或已验签终态证据时保存该已确认前缀，
关闭冲突 C、另票另 C 严格 READY；之后原 proof 重提，得到真实 RECEIPT_COMMITTED
才标宿主接受已提交。没有本机证据时失败关闭，不盲抄服务端计数。

FETCH 的 terminal 条目按契约表示服务端已终结的邮件，不含正文。插件完整验签页头、
manifest 和原终态，核目标 realm/node/incarnation、正序号/版本、唯一 recipient 及终态枚举，
保留原签名条目且禁止改写；只在连续前缀均有本机接受或终态证据后保存服务端 committed_cursor。
分页期间仍使用原 resume_cursor，不把 scan_after/snapshot_head 当确认，也不跨越 gap/held/paused。
每页完整验签并保存证据、校验continuation后，立即持久推进服务端确认范围内的连续前缀；
仍保留每轮128页上限。到达上限时已完成的前缀不丢失，下一轮从新resume_cursor、
空continuation开始；本轮后续分页仍使用本轮原resume_cursor，不把新游标混入旧续页token。
缺口后的证据可保留，但不能提前确认；最终页仍要求与服务端确认游标之间没有证据缺口。
终态不会进入 HostPort、不会产生本机接受证明或 RECEIPT；receive_none 不阻止保存无正文的服务端终态。
该路径修复 SEND-2 短 TTL 邮件被 SEND-3 定期 FETCH 过期后，插件丢弃终态导致重连游标冲突的回归。

文件账本在Windows上仅对原子rename的瞬时EPERM/EACCES/EBUSY重试，最多5次，
退避10/20/40/80ms；保留同一已fsync的临时快照，不删除目标、不复制覆盖，不放宽其他存储失败。
持续错误仍失败关闭。插件组合用例共用真实文件账本，验证正常持久收件、终态保留/严格重连、
重连后再次正常收件同时成立。真实链失败另存`*-runtime.json`的状态与有界文件错误码/修订号，
以区分协议拒绝、宿主失败和文件替换失败；不降低原断言或将未知写入当作成功。

同页收件按recipient隔离会话异常：关闭接收或provider失败不阻止健康会话及后续分页，
失败邮件保留原身份，游标仍不能越过尚无接受/终态证据的邮件。已验证连续前缀按128条分段检查，
超过128条不会被永久拒绝；遇缺口仅保存缺口之前的完整前缀，READY仍须全部证据齐备才能换C恢复。
pending接收设置与已接受回执也逐项恢复；原会话卸载时保留原request_id/registration/版本，
收发工具和健康会话继续可用。本地messaging快照及运行时诊断的failures最多保留128项
scope/id/固定原因码，不记录远端错误原文；会话恢复后可经原设置入口按原请求对账并清除诊断。
连接退役或账本写入结果未知仍整体失败关闭，不作为可隔离的单会话异常。

SEND-2传输探针与SEND-3自动收件复用同一连接，探针可能在生产FETCH在途时收到
合法的request-scope overloaded。探针独立使用固定设备配置的根、正式公告/公钥集和私有文件缓存，
通过生产mailbox client完整验签并执行原期限内、最多5次的同字节重试；坏签名、异受众、
取消或持续过载仍失败，不把错误当作FETCH成功，也不改原受理/正文/版本/跨身份断言。
插件层另验证HEAD/定时/接收开关同时触发时合并轮询、同连接pull及续页串行，
不是通过暂停自动接收或增大服务端并发来消除驱动竞争。

## 验证入口

```powershell
node --test --test-concurrency=1 clients/dsh-plugin/test/network/durable-*.test.js
node --test e2e/network/plugin-send.test.mjs
# 独立锁定真实 DSH rc.3；远端为明确的契约夹具，无 PG/真实 HTTPS 受理声明。
node --input-type=module -e "import { installNativeRuntime } from './e2e/network/support/native-install.mjs'; await installNativeRuntime();"
node --test e2e/network/plugin-native-check.mjs
# 主控在精确干净 HEAD 跑真实 Java/IAM/collab/Org/PG/Broker/WSS/HTTPS：
.\e2e\network\verify-plugin-send.ps1 -ExpectedHead <40位HEAD> -Run
```

verify 从同一 git archive 解包 Control、SkipSubmissionCommit、LocalReceiptVersion，分别
安装锁定的独立依赖、typecheck、编译 Java 桥和 Go 网关。三个场景使用完全相同测试源码；
两个坏实现只各改一个生产文件并须编译成功。归档哈希、测试/源码哈希、命令/退出码和
完整 manifest 留在 e2e/network/target/verify-plugin-send-*。
-CompileOnly 不运行 PG，不声明业务反证通过。

Control 保留 T3b/T4 与 SEND-2 的原断言，增加真实 ToolRuntime→生产工具→设备 HTTPS→PG，
另一真实 Context 的原生会话通过生产 FETCH/attempt/admit→HostPort→持久 provider→回执提交，
核正文、同键原结果字节和服务端版本。当前桥 IAM 与网关是两个真实 HTTPS origin，
测试各用同一生产 device-provider 和同一真实设备凭据承接两端口；不注入全业务 provider。
每个仅承担传输的provider配置platformOrigin/issuer为其自身固定origin，不沿用另一个
端口的配置。该辅助配置不执行登录、发现公钥或验证/改写JWT；getDevice仍交付原IAM令牌，
gateway仍按原IAM authority验签鉴权。产品对issuer的scheme/host/port严格同源校验保留。
隔离驱动在 SEND-3 子进程中为精确 submit origin/path 安装专用 mTLS fetch 传输适配，
复用 source-assembly 生成的客户端证书、独立服务端 CA 和固定服务端名。生产
device-provider 的 getDevice、身份一致性检查、Bearer、15 秒期限、取消、64 KiB 响应限制、
严格解析和 HTTP 拒绝分类照常执行；响应通过真实 HTTPS stream 交回生产读取逻辑。
其它 fetch（包括 IAM 请求）继续使用原传输；结束时按所有权恢复原 fetch。
两端仍走同一生产 ToolRuntime、发件箱/收件箱及同一套 Control/反证，证书不会替代设备身份。

精确 HEAD archive 的宿主安装沿 main 的持久公共 npm 缓存机制：可用绝对路径
`AURIGA_DSH_HOST_NPM_CACHE` 指定共享缓存；独立 profile、凭据、npmrc 和工作目录保持隔离。
`host-install-cache.test.mjs` 验证缓存复用和冷缓存超时诊断；真正宿主安装及 PG 全链仍须重跑
下述 `-Run`，不能用缓存单测或 `-CompileOnly` 代替。
recipient快照只新增host_request_id投影：按tenant/recipient/delivery及system或session接受阶段，
读取同一只读/RLS快照内network_acceptance_proof.proof_json的已持久字段。生产回执事务
原本已保存该证明，recipient表自身没有此列；缺证明返回null，错误ID保留原值，
不从recipient_id推算、不改变原断言、表清单或其他表输出，也不修改产品落库与契约。
本机设置准入用仅本进程持有的控制能力，HTTP 原生认证/同源/CSRF 另有插件测试，
不把驱动当 GUI 点击或完整登录验收。

SkipSubmissionCommit 必须命中 `SEND-3 original submission must be durable before HTTPS`；
LocalReceiptVersion 必须命中 `SEND-3 receipt must use server recipient.state_version`。
初始化、SQL、超时、其他断言或清理失败都不算指定业务反证。
两个独立 DSH 进程/profile 的往返对话由 SEND-4 验收。
