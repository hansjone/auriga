# 正式发行与 OCI 原始证据验证

本包实现 `contracts/runtime/README.md` 的发行声明和离线 OCI 证据校验，
只使用标准库及既有 protobuf / `releasetrust`，不接启动驱动、Store、网络或归档解压。
验签通过不代表生产启动已被保护，也不代表已提交防回滚高水位。

## API 与接线

```go
target := releaseverify.Target{
    OS: localOS, Arch: localArch, Driver: selectedDriver,
    AllowImage: localPolicy.AllowImage,
}
verified, err := releaseverify.Verify(dsh, currentRing, target)
// 检查 err；Docker 的 currentRing 来自本次 Store.GetReleaseTrust。
image, err := releaseverify.VerifyOCI(verified, currentRing, manifestBytes, configBytes)
// 检查 err；原始字节不能由解析后重新序列化的 JSON 替代。

// 后续 RT2 在连续启用互斥区内重新读取可信 ring 并执行：
err = verified.Recheck(latestRing)
// 检查 err，并执行持久高水位/真实驱动的完整启用事务。
// Docker 还应核真实本地 Engine 的内容寻址身份 == image.ConfigID()。
// 有归档时独立核实际归档字节的 sha256 == verified.ArchiveDigest()。
```

`Verify(*nodev1.DshRuntime, releasetrust.Ring, Target) (VerifiedRelease, error)`
校验本机实际 OS/arch（仅 linux/windows、amd64/arm64）、选定驱动以及全部跨消息映射。
驱动接受 `docker`、`linux-process`、`windows-process`；native 驱动与 OS 必须对应。
Docker 必填 `AllowImage func(string) error`，可直接复用 `policy.Policy.AllowImage`，
由调用方决定前缀/仓库策略；本包独立要求小写完整 registry/path、规范可选端口、
无 scheme/tag/别名的 `repository@sha256:<64位小写hex>`，摘要与受签值相等。
显式端口的单标签本地 registry（如 `registry:5000/auriga/dsh`）合法，仍须通过同一策略回调；
无端口、无点且不是 localhost 的首段只会形成隐式仓库名，拒绝接受。
v1 locator 不支持 IPv6 registry、非规范端口或隐式 Docker Hub 仓库缩写。

`VerifyOCI(VerifiedRelease, releasetrust.Ring, []byte, []byte) (VerifiedImage, error)`
输入原始单平台 manifest M 和 config C；必须先检查 `err`。
`VerifiedRelease` / `VerifiedImage` 只有私有字段，零值 `Valid()==false`，
`Recheck` 拒绝零值；无导出可写结构、反序列化入口或可篡改签名状态。
`CanonicalBody` 返回深拷贝，其余访问方法只返回值/不可变字符串。

发行结果提供 `Partition()`（purpose/name/os/arch）、`Sequence()`、`BodyDigest()`、
`CanonicalBody()`、`ArchiveDigest()`、`ImageDigest()`、`Image()`、`ArtifactID()`、`DSHVersion()`。
摘要统一是 `sha256:<小写hex>`；正文摘要只覆盖完整 JCS 正文，不含 signature。
镜像结果提供 `ConfigID()`、`ManifestDigest()`、`BodyDigest()`、`Partition()`、`LayerCount()`。
configID 永远来自 `sha256(C)`，不是发行签署的 manifest 摘要。

两种结果的 `Recheck(ring) error` 接收本次最新可信 ring，逐次用 `time.Now()`
查询新/旧签名所用 kid 并重新验签；`VerifyOCI` 也先重新检查。
结果不捕获信任环，也不持有可永久复用的启用资格。
RT2 仍须把最后一次 Recheck、持久序号提交和真实创建/启动动作纳入同一启用保护，
缓存、重启、恢复入口不得省略。仓库策略变化也须由调用方重新应用。

## 签名与传输边界

递归拒绝 DshRuntime、envelope、Signature、Timestamp、旧 SignedArtifact/statement
的 protobuf 未知字段，校验 Timestamp.CheckValid；显式构造完整十一字段正文
（含所有空串），对应十二字段 envelope 去掉顶层 signature。
序号为 1..9007199254740991，Timestamp 为 UTC 四位年份与最短必要纳秒小数。
JCS 使用 UTF-16 键排序、精确 JSON 转义，拒绝非法 UTF-8，不做 Unicode/大小写等规范化，
不使用 protojson 或 HTML escaping。签名要求 Ed25519、规范 raw base64url、64 原始字节。
旧 signed 存在时以同一可信 ring 验七字段正文及其完整映射（含秒/纳秒瞬间）。
废弃 signature/signing_kid 不提供授权；缺新 envelope 一律拒绝。

该 API 接受 protobuf；protobuf 不能恢复原始 JSON 的重复键、缺失字段或 null。
任何未来 JSON 入口必须在映射前按 runtime 契约严格检查原始 JSON；不能先丢弃字段再调用本包。

## OCI v1 支持范围

- manifest 为 schemaVersion **整数 2**，显式 mediaType 仅 OCI image manifest v1 或
  Docker distribution schema2；config descriptor 必须分别为 OCI image config v1 / Docker container image v1。
  拒绝 index/list、artifactType、subject、manifest 集合、空 config、未知 mediaType。
- descriptor 仅支持 mediaType/digest/正整数 size/字符串 annotations；digest 为 sha256，
  size <= 最大安全整数；拒绝 urls/data/外部或内嵌载荷等未支持扩展。
- OCI layers 支持 tar、tar+gzip、tar+zstd；Docker schema2 支持 rootfs.diff.tar.gzip。
  不支持 foreign/nondistributable layer、其他压缩或可执行格式。
- C 必须有匹配 os/architecture、对象 config、`rootfs.type=layers` 及 diff_ids 数组。
  diff_ids 必须是 sha256，数量与 layers 一致；未压缩 tar 的 layer digest 必须等于 diff_id。
  history 如提供，必须有合法条目，其非 empty_layer 数量须匹配 layers。
  annotations、history、普通顶层元数据扩展均允许；它们仍受原始摘要和严格 JSON 检查保护。
  config 的执行字段只接受已支持的 OCI/Docker 字段及对应形状，未知执行字段拒绝。
  v1 不协商 variant 或额外 os.features，非空时拒绝；os.version 只检查字符串形状，
  目标 Windows 内核兼容性仍由可信驱动负责。
- M、C 各最多 4 MiB；深度最多 32 层容器（含顶层对象）。全部 JSON 层级拒绝重复键、
  trailing JSON、非法 UTF-8 与未配对 UTF-16 surrogate escape；不对 JSON 做摘要复刻。

本包不下载 layer，也不证明压缩载荷解压后的 diff_ids；只核对原始 M/C 及所需结构。
真实 layer 摘要/解压内容与本地 configID 的对应关系必须由可信 Engine 内容寻址保证。
本包不声称抵抗宿主 root、受损 daemon、签名发行者本身、或拥有本机策略配置权的攻击者。

格式依据：[OCI manifest v1.1.1](https://github.com/opencontainers/image-spec/blob/v1.1.1/manifest.md)、
[OCI image config v1.1.1](https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md)、
[Docker schema2](https://github.com/distribution/distribution/blob/v2.8.3/docs/spec/manifest-v2-2.md)。
本包是上述格式的明确受限消费实现，不是通用 registry/镜像解析器。

## 验证

`go test ./internal/releaseverify` 默认消费只读的共享 Java 14 个签名向量及 resigned 输入，
运行七种 Timestamp 精度的 runtime/standalone 校验；归档存在的共享 runtime 向量还通过
实际 Verify 路径同时校验配对旧签名。设置 `AURIGA_JAVA_RUNTIME_VECTORS` 可额外核对当次 Java 输出。
测试另用进程内生成密钥覆盖字段篡改、过期/错误 key、映射/驱动策略、未知字段、
整数/时间/Unicode 边界、合法 OCI/schema2、M/C 原字节损坏及大小/深度/重复键等负例。
共享向量只证明本包签名映射一致，不代表生产驱动/Store 已接线。
