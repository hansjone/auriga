# 正式 DSH 发行信任 v1（RT0）

本目录冻结发行签名、bootstrap、背书和防回滚契约。**RT0 只提供契约和跨语言证据，当前生产 agent/workspace 尚未强制消费**：RT1 实现 keyring，RT2 实现发行校验、OCI inspect 和持久 CAS，RT3 才提供正式发行/模板流程。本 PR 不是任何现场可部署证明。公开安装同时面向域名和 IP，不要求公共 DNS，不改变容量默认。

## 签名对象与映射

`release.schema.json` 是严格输入契约。RuntimeReleaseEnvelope JSON 恰有十二字段：
`schemaVersion,purpose,name,artifactId,dshVersion,os,arch,archiveDigest,imageDigest,releaseSequence,issuedAt,signature`。
除去**顶层** `signature` 后，完整正文按 RFC8785 JCS 规范化，以 UTF-8 编码，Ed25519 签名。
`signature` 恰有 `alg="Ed25519",kid,value`；kid 为 `[A-Za-z0-9._-]{1,128}`；value 为原始64字节签名的规范无填充 base64url，解码后重编码必须一致。
未知字段、重复 JSON 键、非法 Unicode、缺失字段、null、非安全整数均拒绝，不能先忽略再验签；扩展须新 schemaVersion。
字符串保留原始码点，不做 Unicode、大小写、SemVer 或 locator 规范化。不复用仅供 plugin 的 ResourceEnvelope。

protobuf 是传输类型而非签名字节来源。必须显式构造以上 JSON：snake_case 对应 lowerCamelCase，uint64 序号映射 **JSON 数字**，所有空字符串仍输出；不能直接签默认 protojson（它可能省略空值、把 uint64 编成字符串）。签名校验前检查 protobuf 未知字段；原 JSON 必须在映射前严格校验，禁止解析丢失未知字段/重复键后再验签。
`schemaVersion=1,purpose="dsh_runtime",name="dsh"`；OS 仅 linux/windows，arch 仅 amd64/arm64，不接受 any。
`artifactId=dsh.artifact_id,dshVersion=dsh.version,archiveDigest=dsh.digest`，逐字符串相等。

issuedAt 来源 Timestamp：先执行 CheckValid 等价校验（seconds -62135596800..253402300799，nanos 0..999999999），
再输出 UTC、四位年份，整秒以 Z 结尾；非零 nanos 补足9位后去尾零，保留最短必要的1..9位小数。
输入 JSON 也必须符合该唯一表示；`.100Z` 不是 `.1Z` 的可接受别名。不得用默认 protojson 的0/3/6/9位规则。

## 归档与实际执行的镜像

- Docker：image 必须为 `<本机策略允许的仓库>@sha256:<64位小写hex>`，摘要后缀与受签 imageDigest **精确相等**。拒绝 tag、OCI index/manifest list；拉取/离线缓存后检查实际单平台 manifest、真实 OS/arch 与受签平台和节点目标相符。locator 仓库不在签名中，仍受本机策略约束。
- Docker-only：archiveDigest、dsh.digest、dsh.url 可同时为空；imageDigest 不可空。若同时提供归档，则 archiveDigest/dsh.digest 必须相等且非空，归档字节仍需核摘要。不得用归档签名证明镜像。
- native（Linux/Windows process）：imageDigest 和 dsh.image 必须为空，archiveDigest/dsh.digest 必须非空且相等，下载或缓存的归档字节必须核摘要。
- 旧 dsh.signed 保留**归档**意义，存在时完整验证签名及 statement 的 purpose/name/version/os/arch/digest/issuedAt 与发行映射逐项一致；其中 version 对应 dshVersion，digest 对应 archiveDigest。Docker-only 没有归档时不可附旧归档声明。旧 signature/signing_kid 字段不成为新发行的信任旁路。

schema 只能表达 JSON 形状与至少一种非空摘要，跨消息映射、驱动模式、本机策略、签名和真实 OCI 检查由消费方执行。RT0 的测试不声称已跑这些生产路径。

## 序号与启用事务（RT2 实现）

releaseSequence 是 1..9007199254740991 的 JSON 整数。持久高水位分区恰为 `(purpose,name,os,arch)`，**不含 artifactId、实例、规格 generation 或 DSH version**。
记录最高序号与完整 JCS 正文的 SHA-256（不含 signature）。低序号拒绝；同序号仅允许相同完整正文摘要，换 key 重签同正文可接受。任一正文变化（包括 artifactId、issuedAt、imageDigest）均冲突。SemVer/时间/resource revision 不能替代序号；首版不支持 runtime 降级，资源 DowngradeAuthorization 不适用。

缓存、启动、重启和恢复均受同一高水位与 key 有效性约束。启用须串行化/CAS 检查分区状态：校验可信签名、准备并核真实内容，再原子提交序号/正文摘要，最后创建或启动实例；提交前若有其他启用推进状态，重新检查，旧候选不得启动。失败不得降低已提交高水位。崩溃后同序号同正文可幂等重试；落盘失败不得启动；落盘后启动失败保留高水位。并发提交、崩溃恢复、生产缓存/重启拒绝测试由 RT2 在实际存储和运行路径完成，不以 RT0 内存模型代替。

## 首次根与 endorsement v1（RT1 实现）

首次成功 Join 仅沿既有 **CA SPKI pin + TLS 验证**的可信 bootstrap 固定初始根，无额外 transcript 签名。入网 token 证明节点资格，不能替代服务端身份认证；bootstrap 服务或 pin 交付被攻破会欺骗新节点。
旧开发选项 AllowInsecure 或仅携带 ca-sha256 的明文 Join 不构成可信发行 bootstrap，不能据此固定发行根；本 PR 不修改旧生产入口或旧开发测试。
必须至少一根；根 endorsement/endorsed_by_kid 均空，公钥原始32字节、not_after 合法且接收时有效。初始附属 key 必须完整锚定到这些根。
后续 Renew、force、丢库、已有身份但缺 keyring 不得重做 TOFU；缺根拒绝正式制品，需受保护的本机重新 bootstrap。ApplySpec、NodeControl、归档不能添根或替换根。

背书输入精确为 `UTF8(child.kid) || child.public_key[32]`，Ed25519 原始64字节签名；无 NUL、域前缀、分隔符、长度、JSON、hex 或 base64。kid 限ASCII的上述正则，边界由末尾32字节确定。改变输入需要新协议版本。
endorsed_by_kid 只选择可信父 key。父 key 必须已锚定且在本次接收时有效；同批中间 key 只有完整验证后才构成锚定链，列表顺序无关。
先完整验候选图再一次事务合并；拒绝未知父、环、重复kid、坏kid/key/signature、缺一半背书、冲突。任何失败不部分提交。
每个 kid 的公钥、收到的原始 not_after（Timestamp seconds/nanos）、来源和背书一经固定不可变；缩短和延长都冲突，历史冲突记录到期后也保留。完全相同的重复列表幂等；列表遗漏不撤销、不抹掉新 key，旧列表不恢复过期 key；空 Renew 列表是无更新，不是撤销。

v1 **没有签 not_after 或 endorsed_by_kid**，不得宣传子 key 时间为发行者签署的有效期。子 key 的本地截止 = min(收到的 not_after,父 key 本地截止)；新父链逐级取最小。首次根期限经可信 bootstrap 固定，后续不可扩大；恶意 workspace 仍可缩短新子 key 首次期限造成拒绝服务。接收或使用时 `now >= 本地截止` 即失效，缓存也不例外。固定根期限内可轮换，跨根期限轮换、独立签署期限与撤销留未来协议；首版根到期须受保护重新 bootstrap。
续证与 key 列表分开验收：合法新节点证书仍保存、保留旧 keyring，明确报告信任更新失败，不能丢弃有效续证导致失联，也不能为续证接纳坏 key。

## 旧 standalone ArtifactStatement

旧独立 SignedArtifact 的 statement 恰为 `purpose,name,version,os,arch,digest,issuedAt` 七字段，全部显式，字符串不规范化，不增加发行序号等字段。issuedAt 使用上述 Timestamp 唯一映射，签名为 protobuf bytes 原始64字节，signing_kid 选择已信任的 key。未知字段拒绝。`standalone.schema.json` 记录该映射。
**已有 resource envelope 的原 JSON 时间精度保持原样签验**，与旧 Timestamp 仅比较瞬间，不追溯套用最短小数规则；resource 契约不变。

## 可复核向量和验证边界

`signature-vectors.json` 由 `RuntimeTrustVectorsTest` 中 Java JCA Ed25519 使用进程内新建密钥生成；只保存公钥、正文、JCS、摘要、签名及公开测试输入，永不落盘私钥。Go/Java/JS 重新构造 JCS 并验证同一份 Java 签名；Go 测试无需环境变量即消费固定向量。Java 测试另外写 `target/java-runtime-signature-vectors.json`，可由 `AURIGA_JAVA_RUNTIME_VECTORS` 交给 Go 复核当次 Java 签名。
常规 Maven 契约测试只读固定向量；仅显式传 `-Dauriga.runtime.generateVectors=true` 才重建共享文件（公钥/签名随进程新密钥改变），生成后再运行 Go 与 JS 校验。`resigned` 保存另一进程内新建 key 对首份完整正文的重签，正文摘要/序号不变。protobuf Go 沿既有 `infra/` 下 `go run generate.go`，Java 沿既有 Maven protobuf 插件生成，不提交 target。
`keyring-scenarios.json` 是 RT1 共用的规范验收案例；JS 参考解释器仅核对向量与契约的可计算规则，不代表持久 keyring、续证或生产事务已经实现。RT1/RT2 必须在真实实现重用这些输入并补齐事务、CAS、崩溃、缓存/重启与 OCI inspect 验收。
