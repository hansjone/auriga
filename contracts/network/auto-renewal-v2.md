# 消息网络自动续期 v2 冻结契约【待审查】

权威字段在 `auto-renewal-v2.schema.json`，HTTP 在 `../openapi/network-keys-v2.yaml`，权限/错误/告警在 `../catalog/msgnet-v2.json`。本线提供契约，不提供生产验签器。旧 v1 原样保留用于迁移核验；切换屏障提交后不得常态双签。

## 编码和签名

所有对象严格拒绝重复键、未知字段、非规范 base64url、非整秒 UTC、非规范十进制序号。序号范围 1..18446744073709551615，比较整数值，禁止浮点和字典序比较。每次增加检查溢出，不能绕回。签名为 Ed25519，输入是 UTF-8(域) + 0x00 + RFC8785 JCS(payload)。封装的签名字段不参加自己的签名。摘要为 `sha256:` + SHA256(JCS(完整被引用对象)) 的小写十六进制，不是只摘要 payload。对象引用中的 authority_digest 是完整 SignedRuntimeAuthority 摘要，keyset_digest 是完整 SignedKeysetV2 摘要。数组顺序属于签名字节，不得验签前排序。runtime_auth.kid 必须等于授权的 runtime_kid，root_auth.kid 必须是独立安装根。

固定域清单：

- SignedRuntimeAuthority：`Auriga.NetworkRuntimeAuthority.v2`
- SignedRootControl：`Auriga.NetworkRootControl.v2`
- SignedKeysetV2：`Auriga.NetworkKeyset.v2`
- SignedKeysetAnnouncementV2：`Auriga.NetworkKeysetAnnouncement.v2`
- SignedTransportTrustBundle：`Auriga.NetworkTransportTrustBundle.v2`
- SignedMigrationPermit：`Auriga.NetworkMigrationPermit.v2`
- InstallationReceipt：`Auriga.NetworkControlReceipt.v2`
- ProjectionRequest：`Auriga.NetworkProjectionRequest.v2`
- ProjectionReceipt：`Auriga.NetworkProjectionReceipt.v2`

后三种对移除 identity_auth 后的完整对象签名。运行钥只允许前三种运行对象（清单、公告、transport bundle）；不得签业务封装、根控制、授权、X.509、IAM、发行、资源或再委托。业务三用途钥与 CA 钥保持独立。授权没有 not_after，也不允许增加硬截止、通配能力或再委托字段。365天复核不改变授权有效性。

## 控制、代际和原子持久化

先按安装根验证根控制和授权，再验证授权 ID/完整摘要仍在本地最新控制允许列表、未撤销、scope 与 not_before 合法，最后验运行签名和用途。新的撤销集合必须包含旧集合；同 revision 不同字节拒绝，低 revision 不得覆盖本地高 revision。新控制先原子持久化；失败不得放行业务。RootControl.control_revision 与 keyset_version 是独立序列，不能比较。

清单绑定 revision > 本地最高控制时先同步控制，不能验业务；绑定 revision <= 本地最高控制合法，前提是授权仍在最新允许列表且满足最低 generation/version。撤销优先于任意高版本、bundle 及缓存。generation 只能由根控制抬高/授权重设版本空间；同 generation 内 version 单调，相同 version 必须相同完整字节。更高 generation 必须先安装允许此代际的根控制；运行钥不得自行增加代际重置版本。issued_at 不单调，校正时钟后更高版本可以更早签发；消费者按自己的当前时钟验 `issued_at <= now + allowed_skew`、`now < valid_until` 和钥期限。valid_until 必须严格晚于 issued_at 且不超过24小时。

公告精确绑定授权、scope、控制绑定号、generation/version、完整签名清单摘要、valid_until；更高公告立即持久 fence，失败写盘不放行，不接受消息自带刷新 URL。控制轮换先发布旧新均允许、确认后切新签发，最后撤旧；不抬最低业务版本误伤合法旧快照。

## 消费者转换（Java/Go/Node 同规则）

每用途恰有一把当前有效 active。全局 kid 与 SPKI 唯一；跨版 kid/材料/用途/原期限不可改，retired/revoked 不可复活。每钥期限正且最长8天，并受授权 limits 收紧。普通刷新不能新增 active。所有非 bootstrap/migration 清单都必须携带准确前驱签名快照、授权、根控制和摘要；前驱过期仅允许用于转换证明，不能安装作当前清单。前驱按其当时绑定的根控制允许列表和授权验证；已被最新控制撤销的前驱只可经下文根锚定转换作为历史证明，绝不能作实时授权。对照本地最高版本、材料及墓碑，冲突拒绝，首次/长离线端也验证同一前驱证明。

- bootstrap：只允许无历史的安装，无前驱，三用途全新 active；已有本地账本拒绝。
- refresh：保留 active，可加 next，首发只有真实 PG 发布事务提交且时钟检查通过才记录 committed_at/version；重试下载不得刷新。clock_error 期间禁止首发入账；校正后的首次正常发布重新锚定，不把污染的未来首发当成熟证明。
- activate：前驱 next 仍有效，首发距 issued_at >=86400秒，该用途旧 active 已过期或至少还剩360秒；原 next 身份/期限不变，原 active→retired。
- restart_recovery：仅对没有有效 active 且没有成熟有效 next 的用途新增全新 active；not_before=issued_at，0<寿命<=604800秒，未来生效/过期/不成熟 next→retired 或 revoked。其余仍有效用途 active 必须保持。此模式按用途分别判定：active 仍有效则保留；active 不可用且存在成熟有效 next 则将该 next 激活并将旧 active 退役；active 不可用且无成熟有效 next 才生成受限全新 active。允许同一 restart_recovery 快照同时保留、激活和全新恢复不同用途，成熟有效 next 不得被全新钥替代。全部三用途均无有效 active 且无成熟有效 next 时才新增三把全新 active。拒绝缺用途、缺前驱、伪首发、无必要恢复、7天超限和历史冲突。
- migration：仅一次根签许可及下述屏障允许；保持原有效 active、kid、期限和真实首发。
- authority_transition：正常换授权、旧新均在当前控制允许列表时，使用根签新授权对应的运行钥签第一份新清单；前驱使用前驱自己的运行公钥和其当时的根签控制验链，同代际版本仍须递增。若旧授权已撤销或需要重设版本空间，最新根控制必须带 GenerationTransition，精确绑定 predecessor_digest、from_generation、to_generation、新 authority_id/完整摘要和 first_version；to_generation严格大于from_generation，并等于本控制minimum_generation，first_version等于minimum_keyset_version。转换第一份清单必须逐字段匹配该锚点，可重置版本为first_version；只比较同代际的版本，不用旧代际版本阻止根授权的重新起步。后续版本按通常单调规则。根签锚点只许可历史证明，不恢复旧授权的签发权；用被撤销旧授权签任何新快照都先被拒。事故恢复必须生成新的业务钥并将旧钥撤销，原材料与首发留审计；仍受用途、寿命、墓碑与撤销不复活约束。

首发证明是持久账本的一部分：新kid的committed_version必须等于它在已提交清单中首次出现的keyset_version，committed_at等于该发布事务经校验的issued_at。首次写入后，后续清单/前驱中的记录不可更改；消费端须逐kid与本地first_publication_ledger核对，不以新收到的前驱自报取代已有记录。新/长期离线端对未曾见过的kid验证前驱签名记录后建立账本，仍保留对已见kid的核对。成熟期按该不可变记录计算。clock_error时既不签清单也不落首发记录，稳定后首次正常提交才锚定。

正常同代际换授权的authority_transition必须保留原active，不能借换授权绕过业务钥的next成熟与重叠窗口。只有根控制精确锚定的新代际事故恢复才允许在该起点替换新的active；旧材料必须保持原身份/期限并进入不可逆终态。

前驱授权已被最新根控制撤销时，转换清单必须保留前驱全部active和next的原身份与期限，并将它们全部置为revoked。新清单的每把active必须使用前驱从未出现的全新kid。遗漏旧钥、仅置retired、原样保留旧active或继续使用旧next均拒绝。该检查在根锚点验证成功后执行，先于一般业务钥转换检查。

过期至少360秒且不用于实时验签的钥才允许移出传输。archive.before 不得超过 issued_at-360秒；本地每把移出钥须满足 not_after<=archive.before。完整材料、终态、首发与唯一索引留追加审计历史；消费者留持久 Tombstone，不受128条传输上限约束。跨年离线可直接验证当前集，不能清墓碑或要求逐版补齐。历史审计只读，不重新执行业务。

## 时钟和配置

签发前 PG/app 偏差绝对值<=60秒，同次启动对照单调基准，前跳>300秒或回拨>60秒拒签、clock_error。每30秒复测，连续两次采样墙钟与单调增量误差<=5秒且PG/app一致后重建基准、自动签更高版本。禁止用跨重启的最大 issued_at 锁死恢复。错误期间不写首次发布时间。不得调整宿主时钟进行测试。

RenewalConfig 前缀 `auriga.collab.network.auto-renew.`；默认24h清单/12h刷新/每分钟检查、8天业务钥/5天轮换/48h预告/24h成熟/360s重叠、60s偏差、365天复核及30/7/1天提醒。schema 限界之外还须验证组合：刷新<清单寿命，轮换+预告<=业务寿命，预告>=成熟，激活时旧钥剩余>=重叠；叶子提前续期<寿命、CA提前换代<寿命。提醒允许提前，不能晚于30/7/1阈值；不能配置硬到期。手动 paused 持久，普通 start 不解除。

## 确认、隔离和材料

撤销开始冻结目标集合。ControlPackage 随机 challenge 至少32字节，每目标/控制摘要唯一，设备持久化成功后签 InstallationReceipt，C 原子核挑战、身份、scope、revision/digest，重复只允许精确幂等，不同摘要或nonce拒绝。未确认/离线/新增目标默认 quarantined。I 拒发消息令牌、撤销旧授权、NodeControl 关闭消息通路、network断开及broker驱逐已连会话。只有新链READY且身份未泄露才可 ready；泄露身份先重装/换身份，旧身份回执不得解隔离。公有控制同步不依赖消息连接；没有全网固定撤销传播时限。

ProjectionRequest 绑定独立安装身份、tenant/realm/component/gateway、随机nonce、期望代际/版本；mTLS端口只输出其授权材料。ProtocolMaterial 只供 network 投影身份取 protocol_response active/next，绝不输出运行、CA、KEK或其他业务用途私钥；没有 next 时 next_kid/next_pkcs8 同时缺省，不得伪造私钥。ProjectionMaterial.protocol 只允许 network，leaves 按组件及安装绑定 SAN/EKU 裁剪。nonce一次性，响应必须精确匹配期望值，旧期望拒绝而非返回旧材料。

投影器10001只写绑定组件卷，目录0700/文件0600，应用只读；collab10002独占 KEK 0400，edge65532及投影器不可读。写新代目录→fsync→核公私匹配→原子selector，5秒发现；Unix socket 只接受已核 generation/version/digest 的固定重载/确认，无路径/exec参数。旧PG版本fence立即拒签。失败1秒指数退避至60秒持续重试，60秒告警退流量，成功自动回流。broker监督器仅能重载或重启自己的子进程。

CA10年/提前2年，叶子90天/提前30天续签，NotBefore允许经校验当前UTC回溯300秒，NotAfter<=CA截止。TransportTrustBundle 顺序为 dual_trust→收齐在线reader持久回执（失联先隔离）→new_leaves→真实握手回执→retire_old。bundle_version独立单调，RootControl撤销优先。scope严格限内部 submit/edge/broker/broker-client/edge-presence，不覆盖公网、NodeControl、发行。过期引导固定端点只给公有控制/bundle及密封材料，禁止skip-verify或HTTP明文秘密。

HPKE RFC9180 base mode，KEM 0x0020、KDF 0x0001、AEAD 0x0002。info=UTF-8(`Auriga.NetworkSealedMaterial.v2`)；AAD=JCS(SealedMaterial移除enc/ciphertext)，明文=JCS(ProjectionMaterial)，安装时登记X25519公钥，响应nonce绑定签名请求。解密前先验证独立根链、最新控制、bundle摘要，解密后再核所有scope/版本/身份。安装身份无年度截止，可独立根撤销；不能新增身份或解除隔离。

PG 密文使用 EncryptedPrivateKey：随机96位nonce不可复用，AES-256-GCM，ciphertext为密文后拼16字节认证tag的无填充base64url，AAD为UTF-8(JCS(PrivateKeyAad))，绑定tenant/realm/kid/算法/用途/组件。KEK ID只选择本域受控密钥环，未知ID或任意AAD串换拒绝；读旧新、写新，全部重加密耐久完成后才能撤旧。该模型用于collab私有持久层，公有状态/控制包/NodeControl不得输出密文包或任何私钥。

## 迁移和耐久消息

MigrationPermit 根签绑定不可变祖先归档摘要、全部已签队列成员/材料承诺摘要、最高承诺v1上限和v2最低版本/代际；不绑定动态PG当前摘要。承诺计算为 SHA256(JCS(按version数值升序的数组，每项含version、完整pair摘要、材料摘要))。根签期间旧队列继续推进；超出承诺的新批次需重签。先核剩余>=21600秒，再锁内冻结，单调计时最长600秒；未提交超时自动恢复旧队列。v2首版/最低代际/永久禁旧/receipt同事务构成屏障，屏障前不发v2公告。历史基线51/队列52–65对应首v2>=66；实际以最高承诺值为准。屏障前可回旧，屏障后仅向前补装，保留PG/客户端账本和业务记录。

Redelivery 保留 message_id/delivery_id/sequence/body_digest/original_expires_at，当前授权仍合法且TTL内未确认者使用当前会话重新封装；旧签名仍过期拒绝。按耐久ID去重，已确认不重执行；TTL过期或撤销记expired/rejected，禁止延长TTL。消息正文及原回执留存可查。

## 消费与生成

C采用全部根链/转换/首发/配置、归档及材料端口；N采用Publication、ProtocolMaterial、TransportTrustBundle及投影收据；P采用相同链、前驱/墓碑、控制包/回执、Redelivery；I采用安装身份scope及IsolationRecord、NodeControl公有JSON消息；O采用RenewalStatus及catalog告警；D采用MigrationPermit/Receipt、RenewalConfig及密封引导。

Java：`mvnw -B -pl services/contracts -am verify -T1` 生成 `services/contracts/target/generated-sources/openapi/msgnet`；Go：加 `-Pgo-openapi-models generate-sources` 生成 `infra/gen/msgnetapi`；TS：`pnpm gen:api` 生成 `web/shared/src/generated/network-keys-v2.ts`，插件执行 `pnpm gen:plugin-network` 从同schema生成类型和运行时产物。共享向量在 `fixtures/auto-renewal-v2-vectors.json`，签名输入以canonical字段供跨语言核对，私钥仅在生成进程内存在。

## 第2轮补充：能力、运输与向量

capabilities允许非空子集；每个对象/转换按其实际能力校验，配置不能增加根未授权能力。无restart_recovery即拒受限恢复，无network_transport即拒内部信任包；清单/公告/新业务钥/归档分别要求keyset/announcement/business_rotation/history_archive。limits全部由根签且只能收紧：清单寿命、业务钥寿命、恢复钥寿命取授权上限，成熟/重叠/归档宽限取授权下限，不能只查固定默认常数。

OpenAPI生成接口/模型只表达数据和权限，不承载mTLS握手实现。生成器在既有其他API的mutualTLS类型上有不支持提示；禁止将生成存根当作已实现mTLS。N/I负责在固定安装网关/监听器强制验证证书、安装身份与组件scope，C再次核材料权限；公有bootstrap运输只提供根链及应用层密封对象，不因存根缺少安全定义而输出明文秘密。

共享向量输入、错误优先级、三语言adapter边界与可复现命令见[向量规范](msgnet-v2-vectors.md)。Java/Go的契约测试会传入同一JSON并逐例比较参考adapter结果，另独立验签与验X.509链；这不替代C/N/P各自生产验证器的同数据测试。上线门禁必须由实现线接生产adapter重跑全部三组，不得把Node参考判定器引入生产。

## 本地投影协议（K2 增补）

N 的 projector、network reader、broker 同 UID 监督器和 I 的 edge reader 共用 LocalProjectionManifest / LocalProjectionSelector / LocalProjectionRequest / LocalProjectionResponse 生成模型。D 为每个安装身份与组件配置独立卷根和 Unix stream socket，材料卷内固定 `selector.json`、`generations/`，该组件独立私有控制卷内固定 `reload.sock`。这些位置来自受控装配，消息字段只携带标识符。监听方固定为该组件的 reader（network/edge）或 broker 监督器，连接方固定为 projector。network/broker 监听 UID 10001，edge 监听 UID 65532，服务端核 SO_PEERCRED 的连接 UID 必须为 10001，projector 核服务端 UID 与目标组件一致。reload.sock 仅放在该组件私有控制卷中，卷可写挂载给该 reader/监督器和专属 projector。控制卷目录 owner 为监听 UID、group 为10001、模式0710，socket owner 为监听 UID、group 为10001、模式0660，使 edge UID65532 的监听器与 projector UID10001 可连接。材料卷仍按只读 reader 挂载规则装配。只有该组件 reader/监督器与其专属 projector 挂载这对卷，其他组件与 projector 不挂这些卷。D 为每组件配置独立 projector 实例和材料/控制卷，不能将全部组件卷挂给一个共享实例。相同 UID 无法区分组件进程，隔离依靠卷挂载命名空间和固定私有 socket，peer UID 检查与此共同生效。卷权限沿上文规定。

selector.json 为无 BOM、无换行的 UTF-8 JCS(LocalProjectionSelector)，最大 16384 字节。manifest.json 为 UTF-8 JCS(LocalProjectionManifest)，最大 65536 字节。代目录固定为 `generations/<manifest_digest去掉sha256:前缀>/`。读取时固定根目录、拒绝符号链接与非普通文件，每个文件限制 1048576 字节，整代至多 128 个数据文件。目录内仅有 manifest.json 和 manifest.files 列出的文件。files 按 name 的 ASCII 顺序排列且名称唯一，长度和 sha256 分别核原始文件字节数和 SHA256 原始字节。manifest_digest 是 SHA256(JCS(完整 manifest))。selector 除 manifest_digest 外逐字段等于 manifest 的绑定字段。

`publication.json` 是完整 Publication 的 JCS，`publication_digest` 保持现有完整 SignedKeysetV2 摘要语义。`bundle.json` 是完整 SignedTransportTrustBundle 的 JCS，bundle_digest 摘要完整受签 bundle。两文件必须存在，分别核根链、安装 scope 和本地最新控制。network 有 protocol 时额外包含 `protocol.json`（ProtocolMaterial 的 JCS）。叶子按 ProjectionMaterial.leaves 原数组顺序编码为 leaf-000.cert.pem / leaf-000.key.pem 至 leaf-125，逐项核证书、对应私钥公钥匹配、SAN/EKU/组件/安装授权和 bundle CA。PEM 为规范 LF、64 列 base64、尾换行，无附加块。不得出现未授权用途材料。manifest 的文件摘要覆盖完整实际材料，签名清单摘要、bundle 摘要各保留原语义。

本地 material_revision 为每安装身份/组件独立持久单调 uint64，业务清单和 bundle 未变而叶子替换也必须增加。任一材料字节、绑定字段变化都创建新代。发布前持久保存 revision、ProjectionMaterial 绑定的 nonce、完整 manifest 和 selector，随后 fsync 文件、代目录、原子替换 selector 并 fsync 父目录。重启从已持久事务恢复，同代精确重试复用 nonce/字节；新代使用新材料请求的随机 nonce，不能以旧 nonce 关联新叶子。失败请求的重试保持相同代，不以新 revision 掩盖失败。

Unix stream 每条消息为 4 字节无符号大端长度加 UTF-8 JCS JSON，长度 1..16384，长度只计 JSON。每连接一请求一响应，完整读写支持分片，拒绝截断、超长、重复键、未知字段、非规范编码与尾随字节，5 秒读写截止后关闭。请求 operation 固定 reload 或 confirm。reload 要求 selector 精确匹配磁盘已验证发布值，先校验完整文件集再原子切换活动句柄。confirm 查询该精确代已持久加载/握手状态，不能触发加载其他代。响应逐字段回显 selector，phase 为 loaded / handshake_verified / failed，成功 failure_code=none，失败必须为枚举中的非 none。真实握手必须实际使用这代叶子及对端验证链才能确认 handshake_verified。

每个 reader 持久保存最高 generation/keyset_version、bundle_version、material_revision 与该 revision 的 selector。低代际、同代际低 keyset_version、低 bundle_version 或低 material_revision 拒绝 version_rollback。同 revision 不同 selector 拒绝 same_version_conflict，陌生 manifest 摘要/字节拒绝 invalid_chain，尚未落盘/加载/握手返回 projection_pending。同代精确重试返回持久状态，失败不覆盖最近成功状态，按 1..60 秒退避持续重试，5 秒发现和60秒退流量沿现有规则。回执只确认精确 selector，不能用更早一次的成功解锁新代。

N 从匹配的本地响应生成发 C 的签名 ProjectionReceipt，附 local_selector，nonce、generation、keyset_version、publication_digest、bundle_version、bundle_digest 与其逐字段一致。C 将 nonce 对应的已授权 ProjectionMaterial、完整叶子集与 local_selector 绑定持久化，同一 nonce 的其他 selector 拒绝。仅在同代 loaded 后接受 handshake_verified，同字节回执可幂等重试。I 采用相同生成模型、peer/文件校验与状态机消费 edge 卷。D 按固定布局配置 UID/卷/socket，并保留事务文件和 reader 账本跨重启。上述局部模型供本地协议消费，公有 bootstrap 仍遵循原密封材料流程。


## 第2轮审查补充

本地 selector/manifest 的 nonce 固定为32个随机字节的规范无填充 base64url，共43字符，最后字符必须符合尾位约束。LocalProjectionResponse 的成功 phase 只配 none，failed 只配非 none，schema 同时约束二者。

C/I/D 共用 InstallationPins，schema_version 固定2，tenant_id/realm 绑定安装域，pins 为 InstallationPin 数组。每条固定 target_id、identity_id、signing_spki、component、gateway_id、sealing_spki、subject 七字段。signing_spki 是 Ed25519 SPKI DER 的规范 base64url，sealing_spki 是 X25519 SPKI DER 的规范 base64url，装载时解析算法及精确DER并拒绝非规范编码。subject 为 TLS 客户端证书 subject 的 RFC2253 精确文本，内部 mTLS 路径额外核证书 subject 与 pin 一致，密封引导路径按请求体安装签名和 pin 鉴权。ProjectionRequest.installation_id 和 identity_auth.kid 必须等于 pin.identity_id。identity_id 与 target_id 各自在该安装域内唯一，component/gateway_id 决定唯一允许投影域，不能由请求覆盖。D 写受保护配置，C/I 只读装载，文件/目录权限0600/0700，修改须由已有独立安装管理通路完成。根控制撤销/隔离优先于 pin，pin 不授予解除隔离权限。原 identities-file 无版本数组由 D 在升级时转换成此对象。

C 关于其他交接项的处理如下。初次启动没有成功清单时使用新增 UnpublishedRenewalStatus，通过 RenewalStatusOutput 表达，禁止伪造版本或成功时间。迁移成员摘要的完整输入按新增 MigrationCommitmentMember 及 MigrationMaterialCommitment 冻结，pair_digest 摘要完整 v1 SignedKeyset 对象，material_digest 摘要 JCS(MigrationMaterialCommitment)。keys 按 kid 的 ASCII 排序且唯一，只记录 kid/purpose/公钥SPKI/原期限，私钥以对应公钥关联，不参加摘要。成员按 version 的 uint64 数值排序且唯一，根许可的 queue_commitment_digest 摘要该完整成员数组。C 必须逐成员核真实受签 pair 和真实材料对应公钥，不能只接受摘要自报。

公有 /bootstrap/materials 明确 security=[]，通过 x-auriga-installation-auth 声明以请求体 ProjectionRequest 的独立安装签名认证。x-permission 由该 pin 的投影权限执行，不要求已过期的服务 JWT 或内部 mTLS。仍须固定 HTTPS 端点、独立根控制/安装身份撤销检查、签名 nonce 防重放和 HPKE 密封输出，响应不得明文泄露材料。内部明文材料端口仍按既有 mTLS/JWT 限制。

TransportTrustBundle 在一个周期内仍按 dual_trust→new_leaves→retire_old 推进。下一周期允许 retire_old→dual_trust，bundle_version 继续单调递增且不得重置。消费者从持久前驱或完整受签 previous_transport 验证该起点，前驱必须为已见最高 bundle_version 的 retire_old，当前 previous_bundle_digest 必须精确摘要它。前驱由当前仍允许的运行授权验签，tenant/realm/authority/代际与当前包一致，前驱全部 CA 必须逐字保留，当前包至少增加一个全新 ca_id。旧链保留到新一轮持久加载/真实握手回执齐全后才退休。前驱缺失、摘要/版本/阶段/scope 不一致、丢旧CA或未加新CA均拒绝。运行授权换代先在旧授权尚允许时完成该周期，随后按已有授权转换规则切换。共同向量包含第二周期及逐项负例。


## 消费方须知（第3轮接口对齐）

C 线须将 NetworkRenewal.status()、服务适配器和 /keys/status 的 HTTP 响应统一到 RenewalStatusOutput。已发布状态使用 RenewalStatus，首次成功前使用 UnpublishedRenewalStatus，按 oneOf 分支输出实际状态。

C 线须按 /bootstrap/materials 的 security=[] 与 x-auriga-installation-auth 接线，请求体 ProjectionRequest.identity_auth 和 InstallationPins 是此端点的独立认证依据。该端点使用固定 HTTPS、安装签名、nonce 防重放、撤销/隔离检查及 HPKE 密封响应。C/I 的 HTTP 安全配置须让此端点进入该安装认证流程，投影权限由 pin 的 scope 决定。内部明文材料端口继续使用既有 mTLS/JWT 认证。

contracts.yml 的契约测试与源 schema 做语义深比较，运行时产物的逐字节新鲜度由 web.yml 依次执行 pnpm gen:api、pnpm gen:plugin-network 后的 generated 目录 diff/status 门禁验证。gen:plugin-network 调用插件 --v2 生成器，输出 auto-renewal-v2-schema.js 与 auto-renewal-v2.d.ts。web 镜像的 gen:api 只写 web 目录。
