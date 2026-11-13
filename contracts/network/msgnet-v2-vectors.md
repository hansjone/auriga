# v2 共享向量输入和判定协议

`msgnet-v2-vectors.schema.json`定义完整文件结构，受签对象引用生产schema。config输入提供字段完整的待安装JSON候选，允许小数及待拒绝的策略值，由参考判定器检查整数性、固定复核周期、生产范围和组合约束。每例包含唯一id、input、expected，expected为accept或catalog错误的`collab.msgnet_`后缀。文件分三组全部执行。

## 消费端 cases

- root_spki：独立安装的Ed25519根SPKI，无填充base64url。tenant_id/realm：安装绑定scope，不能从收到的清单选择。
- now：消费者自己的当前UTC整秒；不是服务器自报时钟。
- latest_control：本次已验根签并准备持久安装的最高控制快照；publication.control是清单绑定的控制，两者不要求同revision。
- seen_control_revision：已持久最高控制号；seen_generation/seen_version：已持久最高清单序列二元组，只在相同代际比较version。
- seen_digest：该已持久完整SignedKeysetV2的摘要，用于同序列不同字节拒绝。
- previous_digest：本地账本认可的准确前驱完整签名快照摘要。与传输predecessor.keyset_digest是两个独立证据；不能从刚收到的前驱回填该字段。长期离线端无该记录时以空字符串表示，仍须对照已有钥/首发账本。
- first_publication_ledger：消费者在本例之前已持久的逐kid首发记录；空数组表示首次接触。记录不可被前驱中自报的值覆盖。新kid在当前清单第一次出现时必须有committed_version=当前version和committed_at=当前issued_at；前驱中的既有记录必须与账本一致。
- publication：待接受的完整链和前驱证明；所有摘要均覆盖完整受签封装。
- transport/seen_bundle_version：可选内部信任包及其已持久最高bundle版本。
- previous_transport_phase/readers_persisted/handshakes_verified：可选上轮轮换阶段、在线reader已持久双信任、实际新握手已确认的测试事实。这些由安装回执台账提供，不能取传输声明代替。dual_trust→new_leaves需前者确认，new_leaves→retire_old需握手确认；跳阶段拒绝。

消费者假定输入已按结构schema验证。错误优先级按以下顺序：根链签名invalid_chain；当前授权authority_revoked/authority_unknown；绑定与持久控制control_required/control_rollback；能力/scope/not_before的scope_denied；代际/版本version_rollback、同版摘要same_version_conflict；运行签名invalid_chain；时效invalid_transition；缺前驱predecessor_required；历史链/根锚点/本地前驱摘要；首发账本；钥不可变与用途转换；最后内部transport能力/签名/scope、版本及阶段/确认。前驱错误中先自洽摘要和序列，再历史根链、根锚点，再本地previous_digest。向量中`predecessor-local-ledger-mismatch`保持传输摘要及签名完全正确，专门命中最后一项；`rewrite-first-publication-to-skip-maturity`保持前驱/清单签名与摘要正确，专门与本地首发账本冲突。

## 签发端 issuer_cases

这组不输入publication，不借消费验签代替拒签。clock.pg_skew为PG减应用墙钟偏差（秒），wall_delta为同次启动墙钟采样增量（秒，可负），monotonic_delta为单调时钟增量（秒）。先检查绝对偏差>60、前跳差>300、回拨差<-60，任何一项返回clock_error；此时不得提交清单或首发记录。write_first_publication表示当前计划是否写入新kid首发；clock_error_persisted表示前次处于时钟错误；stable_samples表示已经取得两次间隔30秒、墙钟/单调增量差<=5秒且PG/app一致的采样。错误后仍未稳定不得落首发，稳定后首次正常提交才记录校正后的时间。expected=clock_error表示事务未提交，不能解读为已写入一次错误首发。

## 独立边界 boundary_cases

operation确定input schema和参考判定器：

- signature_domain：signed为测试签名封装，verification_domain及verification_spki为接收端固定选择。正例按真实域签，负例只换真实v1/v2/ServerEnvelope域，或用运行钥替根签RuntimeAuthority/RootControl；不靠kid标签猜测用途。
- announcement：context为完整消费端输入，announcement为运行签公告，persisted为公告fence是否持久成功。先验context及公告签名/能力/scope，再持久门禁，再比较代际版本，最后核同版完整摘要/截止。更高公告或写盘失败为projection_pending，必须阻塞业务，不能忽略公告继续用旧版。
- control_receipt：request为冻结目标的ControlPackage，receipt为设备身份签回执；identity_spki/identity_id来自安装身份，persisted表示真实本地落盘，used_challenge/exact_replay表示台账防重放事实。先验签，再精确对包ID/scope/目标/挑战/控制摘要与revision，再身份、落盘与结果、防重放，最后identity_compromised/new_chain_ready。未READY或泄露身份返回identity_quarantined，不能凭回执解隔离。
- projection：签名request与response为生产模型，identity_spki为独立安装身份。used_nonce来自持久重放台账；exported_purposes列明此次实际导出的用途。先验请求签名，再核scope/nonce、重放、精确期望代际版本，最后最小材料权限；请求字段不能自授其他组件身份。
- hpke：sealed为完整密封元数据及enc/ciphertext；expected_plaintext为解密后的公有合成ProjectionMaterial。参考实现实际执行RFC9180 DHKEM X25519/HKDF-SHA256、base key schedule和AES-256-GCM解密；nonce/身份/版本等AAD改动必须导致认证失败。先认证密文再精确比明文和scope。此夹具没有生产秘密，测试私钥由公开合成标签在内存重建。
- migration：permit为根签许可，root_spki为安装根；members为数值版本升序的承诺成员，每项含version/pair_digest/material_digest；current_version/current_digest为锁内真实PG值。先验许可签名，再核承诺摘要/成员/最高上限，再核remaining_seconds>=21600及frozen_elapsed_seconds<=600，最后首v2版本和barrier_committed/legacy_disabled。超时返回migration_expired表示屏障前释放冻结并恢复旧队列，不是可以继续等根签。根签期间允许推进到已承诺的更高成员。
- archive：issued_at/before/grace_seconds为本次归档边界；removed为移出项，tombstones为持久历史，reappeared为重新出现的项。先核宽限和移出钥原not_after，再对墓碑身份/期限/终态，不能复活。
- config：config为待安装完整RenewalConfig。先查生产schema整数范围/根固定政策，再查各参数只能收紧及组合可行性，最后按倒序提醒阈值>=30/7/1天；拒绝只收紧一个参数却破坏轮换窗口的组合。

## 三语言执行及生产接线

`run-msgnet-v2-cases.mjs --stdin`接受整个文件JSON，从三组input计算结果，输出`[{id,group,result}]`。不读取expected进行判定，不按id返回预置答案。Node单测直接调用同一参考判定器；Java/Go测试各自解析全量输入、通过该契约adapter协议传入，再逐例核结果数量、唯一性和具体错误，同时独立使用各自密码库验共享签名与证书链。Java使用Maven管理的Node，Go测试需要PATH中的Node（CI已有）；这属于契约参考测试，不是三份独立生产实现互操作证明。

**C/N/P必须在各自实现线把adapter换成自己的生产签发/验证入口，原样运行三组全部case，产出同格式结果并核expected。** 生产不能导入这些测试oracle。运行授权撤销、代际及首发账本必须由各自持久层驱动，不能把向量里的输入事实当运行时信任源。

## 可复现生成

从仓库根目录依次执行：

```sh
go run contracts/network/fixtures/generate-msgnet-transport.go
node contracts/network/fixtures/generate-msgnet-v2.mjs
go run contracts/network/fixtures/generate-msgnet-transport.go --check
node contracts/network/fixtures/generate-msgnet-v2.mjs --check
node --test contracts/test/msgnet-v2.test.mjs
```

两套生成器都使用明确标注的公开合成标签派生测试密钥，私钥仅在内存存在，输出仅公钥/证书/签名/合成密封数据。相同源码得到完全相同字节；--check发现漂移退出非0且不覆盖文件。合成种子可被任何人重建，绝不可作为安装/生产材料。npm check-schemas同时运行Node生成器--check；Go证书生成器--check由上述命令及Go线验收执行。

## 逐条件变异自检

`node contracts/network/fixtures/check-msgnet-v2-mutations.mjs`先运行所有正常判定器，确认全部expected一致。随后对两个参考判定器的每个if门禁逐条禁用，并递归拆分每个复合条件的全部AND/OR子条件。边界触发、时钟异常和成熟期表达式也逐项变异。新增if门禁及其子条件自动进入自检。范围校验统一读取生产RenewalConfig schema，逐字段越界向量覆盖每项上界、下界和组合政策。

变异须被至少一个向量发现，存活项须与`fixtures/msgnet-v2-equivalent-mutations.json`逐项精确匹配。该清单逐条说明在合法输入域内的等价理由。未声明的存活项、重复声明、空理由、已经被向量发现的陈旧等价项均导致失败。测试明确断言存活数等于等价清单条数，以及被杀死数加等价数等于总数。当前共255个变异，248个被杀死，7个等价。四个等价项由受签schema固定字段推出，其余三个分别来自历史钥终态与唯一性、前置首发版本上界、墓碑相等比较。

脚本输出逐项witness，标明发现该变异的向量。`contracts/test/msgnet-v2.test.mjs`已接入同一自检，npm check-schemas会自动执行。脚本通过data URL载入内存变异版本，源文件保持原字节。删除成熟期检查的自检使用首发账本完全一致、前驱签名有效的未成熟next向量。撤销授权、同代际保留active、各锚点字段、历史授权和三项能力的负例也分别保持其余约束合法。

## K2 端点与本地协议补充

比较运算符逐个翻转也纳入变异门禁，覆盖两个参考判定器中的 === / !==、< / <=、> / >=，包含回调和 if 以外的表达式。新增端点向量保持现有 cases 原内容，覆盖 24h、360s、now+60、回拨−60、授权起点、归档宽限/截止、业务钥期限与配置组合等号；部分用途恢复同时覆盖保留一用途和保留两用途。当前430项变异，423项被发现，7项与原等价清单匹配，新增运算符变异全部被发现。

N/I/D 另共同读取 `fixtures/msgnet-local-projection-vectors.json`，通过生产本地投影状态/文件完整性 adapter 核 expected。该组独立测试本地协议的摘要、文件完整性、代际与持久状态，合成文件字节只用于这一层。根链、证书与私钥匹配须由原三组及真实材料测试另行核验。参考函数 `msgnet-local-projection-oracle.mjs` 只用于契约测试。覆盖四种回退、同代改字节、未知摘要、损坏/缺失/额外文件、失败持续重试、断电精确重入、confirm 尚未加载、同清单/bundle 的新材料代。

插件运行时产物由 K 使用 `node clients/dsh-plugin/scripts/gen-network-keyset.mjs --v2` 生成。产物导出完整 schema，包含所有 $defs、边界及未知字段限制，契约测试与源定义深比较以检查新鲜度。Java/Go/TS 生成命令沿主契约说明，LocalProjection* 与可选 ProjectionReceipt.local_selector 一起生成；本地投影路径的回执必须携带该绑定。
