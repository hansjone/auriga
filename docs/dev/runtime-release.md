# Docker 正式发行材料签发与严格导入

`infra/agent/cmd/runtime-release` 为发布者签发 Docker-only 的正式发行材料，并为装配者严格导入公开材料。
它直接调用生产 `releasetrust.Merge`、`releaseverify.Verify` 和 `VerifyOCI`，签名正文仅调用生产
`CanonicalReleaseBody`。本工具没有另一套签名格式、JCS 或 OCI 解析器，不生成任何身份、根或部署配置。
规范见 [RT0](../../contracts/runtime/README.md)，生产启用流程见 [agent](agent.md)。

`sign` 交付声明、公开根及单平台原始 manifest/config；`stage-layout` 可另行验证并复制完整离线 OCI 载体，**仍不是完整可部署发行包**。Linux 上的 `import-layout` 从同一输入新建严格快照，随后复制到批准的本地 registry 并逐对象读回。
NAS 通过[公开安装配方](nas.md#消费独立审核的正式运行时)复用严格导入，装配受 receipt 保护的正式模板、公开根和原始 proof。
仅签名通过不代表镜像层已经到位。`import-layout` 仅支持本机匿名 HTTP registry，不管理认证或生产启用。
不支持 native、归档混合声明、背书链管理或根自动轮换。

## 本地 registry 构建与原始 proof

在受控本机环境准备已独立审核并固定于 `deploy/compose/real-dsh/versions.json` 的 `NODE_IMAGE`。
从仓库根目录运行下列命令；本地 registry 须先由 operator 准备，命令会真实 build/push，
然后从本次**精确仓库**的唯一合法 RepoDigest 写元数据。无 `--image-tag` 时仍使用
`127.0.0.1:5000/auriga/real-dsh:w3`；`--metadata-out` 可与新选项组合。

```powershell
$env:NODE_IMAGE = '<versions.json 中已审核的完整 nodeImage pin>'
node deploy/compose/scripts/build-real-dsh.mjs `
  --image-tag localhost:50123/team/real-dsh:release_1 `
  --metadata-out ./release-work/real-dsh.json
```

`--image-tag` 只接受规范 ASCII 的 `127.0.0.1:<端口>` 或 `localhost:<端口>`、小写仓库路径和合法 tag。
端口为 1..65535 且无前导零。builder 仍固定 linux/amd64、禁用构建证明索引，并只复制白名单构建输入。
记录元数据中的精确 `image` 值（`repo@sha256:<M>`），再从同一本地仓库取得原始 M/C：

```powershell
node deploy/compose/scripts/collect-runtime-proofs.mjs `
  '<元数据中的完整 image>' ./release-work/runtime-proofs `
  --repository localhost:50123/team/real-dsh
```

`--repository` 是独立批准的完整本地仓库，须与 image 的 repo 逐字相等；网络地址仅从该仓库推导。
无此选项时 collector 仍仅接收原 `127.0.0.1:5000/auriga/{fake,real}-dsh` fixture。
它按 M 下载单平台原始 manifest 和 descriptor 指向的 config，严格校验后保留原字节。
将得到的 `manifest.json`、`config.json` 作为下文 `sign` 的输入，并按已有显式 root、私钥、
序号、时间及目标平台步骤签发；消费方继续将公开 bundle 中的 root 与**外部独立审核的根**核对，
以 `verify --root ... --allow-image <精确 image>` 严格导入。新建的本地 fixture 根不因此成为官方授权根。

此流程本身没有镜像层载体或公网 registry 认证客户端，也不保证跨 registry 重发布后保持同一个 M。
离线层载体可使用下文 `stage-layout`；Linux 本地 registry 的受限运输可使用 `import-layout`。

## 发布者输入

由发布者在受保护的发布环境准备：

- 明确审核固定的 `ArtifactSigningKey` protobuf 文件，包含 kid、Ed25519 原始 32 字节公钥和原始 `not_after`。
  本工具只接受无 endorsement/parent 的单一根；shape 和当前有效期交由生产 Trust 校验。
- 与该公钥匹配的 Ed25519 PKCS8 PEM 私钥文件，恰为一个 `PRIVATE KEY` 对象；拒绝 PEM 前导文字、附尾对象、
  尾随非空白内容、PEM headers、加密/其他算法格式。应由发布环境现有密钥管理流程保护；工具不负责生成、改 ACL 或解密。
- registry 实际返回的单平台原始 M 字节，以及 M 的 config descriptor 指向的原始 C 字节。
  保留返回的完整原字节，禁止 JSON 重序列化、index/manifest list、docker save 替代或镜像内部文件冒充。
- 独立确定的完整 `<repo>@sha256:<M摘要>`、artifact/version、OS/arch、分区发行序号和 `issued-at`。
  序号、时间、根、平台均无自动默认。时间必须是 UTC `Z` 的 RFC3339 唯一表示，非零小数保留最短必要的 1..9 位。

从 `infra/agent` 构建：

```powershell
go build -o runtime-release.exe ./cmd/runtime-release
```

发布者签发示例（将占位摘要替换为原 M 的实际 SHA-256）：

```powershell
./runtime-release.exe sign `
  --root ./publisher/root.pb --private-key ./publisher/publisher.pem `
  --image registry.example.com/auriga/dsh@sha256:<64位小写hex> `
  --artifact dsh-1.2.3-linux-amd64 --version 1.2.3 --os linux --arch amd64 `
  --sequence 7 --issued-at 2026-10-23T01:02:03.123456789Z `
  --manifest ./source/manifest.json --config ./source/config.json --out ./release-7
```

Linux 使用同一组选项和二进制，移除 PowerShell 续行符。IPv4 仓库同样支持，例如
`192.0.2.8:5000/auriga/dsh@sha256:<64位小写hex>`；是否能通过 HTTP 访问 registry 由后续下载流程决定，工具不联网。
私钥只通过文件路径输入，不接受 argv/env/stdin 密钥字节，不打印或复制私钥到输出。
sign 阶段将发布者显式给出的 image 作为本次唯一允许镜像，签后仍实际调用生产 V/OCI 自验。

发布者负责为 `(purpose,name,os,arch)` 分区分配递增安全整数序号，避免同序不同正文。
工具不持久化序号分配记录，也不防止发布环境自身重复签发冲突。

## 最小公开目录

```text
release-7/
  runtime.pb
  runtime-keys/root.pb
  runtime-proofs/<M摘要的64位小写hex>/
    manifest.json
    config.json
```

`runtime.pb` 是生成契约中的 `DshRuntime`，含正式 `release_envelope`；Docker-only 的 url/digest/archiveDigest
和全部旧签名字段均为空。`root.pb` 是传入公开根的原字节。没有自签元数据、安装 identity、owner/profile/resource 或第二套授权。
M/C 按原字节写出。包以确定性 protobuf 编码严格读取，拒绝重复 singular field、非最短 wire 编码及未知 protobuf 字段；
所有嵌套未知字段仍由生产 V/Trust 拒绝。

公开材料可通过不可信介质运输。目录中附带的根仅供核对，不能自行授权这个目录。
operator 必须通过既有可信发布渠道核对根的 kid、公钥、原始期限及发布者身份，审核后单独固定在受保护位置。
不要把本机新建根或目录自带根自动称为“官方可信”；本工具不会自动注册任何根。

## 严格导入

verify 必须独立指定外部固定根、目标平台和允许的**精确完整镜像**，缺一拒绝；不能从包内声明自动生成策略。
外部根必须位于 bundle 目录之外；公开目录里的根必须与其所有 protobuf 字段完全相同（包括原始秒/纳秒期限和背书空值）。
路径长短名或 8.3 别名指向同一个包内 root 文件时，仍会按实际读取句柄的文件身份拒绝，不能冒充独立外部根。

```powershell
./runtime-release.exe verify `
  --dir ./release-7 --root ./operator-approved/root.pb --os linux --arch amd64 `
  --allow-image registry.example.com/auriga/dsh@sha256:<64位小写hex>
```

验证成功只输出正文摘要和 ConfigID；失败退出码非零，不输出输入原文或私钥。再次读入不会重写任何材料。
安装装配代码可导入 `github.com/hansjone/auriga/infra/agent/runtimebundle`，调用 `Sign` / `Verify`，
通过 `Material` 的复制型 accessor 取得完整 protobuf 和原 M/C 字节、正文摘要及 ConfigID。
该结果是本次验证快照，不是持续有效的启用令牌；装配后仍须按生产流程重新校验。

导入严格拒绝缺叶、额外文件/子目录、未知字段、坏签名、根漂移、过期根、目标/策略不符、M/C 单字节变化，
以及生产 V/OCI 所拒绝的索引、平台错配、结构字段大小写别名和重复 JSON 键。
文件必须是非空普通单链接文件，M/C 各不超过 4 MiB，runtime 不超过 64 KiB，root 不超过 4 KiB，私钥不超过 16 KiB。
路径及祖先不得包含链接；禁止 `..` 穿越。输出父目录必须预先存在，输出通过原子 mkdir 创建，任何已有对象（包括空目录）都拒绝。
先读完并验证所有输入后才创建输出，文件使用独占创建，不覆盖已有材料。后续磁盘 I/O 失败可能留下新建的残缺目录，
strict verify 会拒绝它；诊断后使用另一个全新输出路径重试。工具不提供安装替换或掉电事务保证。

## 完整离线 OCI 载体快照

先取得公开 bundle、独立固定的外部根和明确批准的完整 `repo@sha256:<M>`。operator 另行提供含原 M、C 与全部引用层的本地 OCI image-layout 1.0.0 目录，并显式指定唯一 CAS 对象的总字节预算；工具不会推断可用磁盘空间。示例：

```powershell
./runtime-release.exe stage-layout `
  --dir ./release-7 --root ./operator-approved/root.pb `
  --os linux --arch amd64 `
  --allow-image registry.example.com/auriga/dsh@sha256:<原M的64位小写hex> `
  --layout ./incoming-oci --out ./staged-oci --max-bytes 300000000
```

`stage-layout` 先调用与 `verify` 相同的生产 Trust、V、VerifyOCI 路径，再检查 OCI 目录。它只接受**单一已批准 M** 的严格运输 profile：`oci-layout` 声明 1.0.0，`index.json` 为 schemaVersion 2 且恰有一个与生产已验 M 的 digest、size、mediaType 完全一致的 descriptor。正常字符串 `annotations`（包括 ORAS 的 ref-name）可输入，但不能选择另一个 M；索引、artifact、subject、多平台描述符、外部 URL、重复 JSON 键、大小写结构别名和错类型均拒绝。这是严格单镜像运输工具，不能作为通用 OCI layout reader。外层 index 不参与签名，也不代替原 M。

所有被引用 CAS 文件须是无链接的普通单链接文件。工具从已验 digest 推导 `blobs/sha256/` 路径，流式核对实际大小及 SHA-256；M/C 还须逐字节等于公开 bundle 的原始证明。同摘要重复引用仅复制一次，且 size/mediaType 必须相同。预算包括唯一 M、C 和层的实际字节；缺层、坏层、大小不符或超预算均在创建输出前拒绝。输入中未被引用的 blob 不进入结果。

验证全部输入后，工具原子创建全新输出目录，逐个复制并在复制时重新核对字节；最后才写最小 index。输出只含原 M/C/引用层、`oci-layout` 和无标签的单 M 索引。任何已有输出对象或与输入树交叠的输出路径都拒绝。复制阶段失败会保留新建的残缺目录供诊断，不打印成功结果；请选另一全新路径重试。成功行只报告 M、C、唯一 blob 数和总字节数。本次返回值仅是当时的快照事实，不构成持续授权或运行就绪证明。

此命令只做快照，不执行 ORAS、Docker load/push、联网、identity/receipt 安装或运行时启用。目录格式依据 [OCI Image Layout v1.1.1](https://github.com/opencontainers/image-spec/blob/v1.1.1/image-layout.md)。

## Linux 本地 registry 导入与真实读回

发布者须在签发材料之前独立批准并固定最终本地 locator，例如 `127.0.0.1:50123/auriga/dsh@sha256:<原M的64位小写hex>`。源构建机与目标 NAS 均可使用同一数字 loopback authority 和仓库；签发后不得改包内 image 或消费方的精确 `--allow-image` 来迁就另一仓库。operator 独立审核 ORAS 1.3.0 可执行文件的来源和 SHA-256，显式指定绝对路径。工具的 `oras version` 检查仅核兼容性，不证明文件可信。

先由 operator 启动现有**只发布到宿主 127.0.0.1** 的 registry，并确保批准仓库可匿名通过 HTTP 访问。Linux 命令示例：

```sh
./runtime-release import-layout \
  --dir /srv/container/releases/release-7 \
  --root /srv/container/operator-approved/root.pb \
  --os linux --arch amd64 \
  --allow-image '127.0.0.1:50123/auriga/dsh@sha256:<原M的64位小写hex>' \
  --layout /srv/container/incoming-oci \
  --out /srv/container/new-staged-oci \
  --max-bytes 300000000 \
  --oras /srv/container/operator-approved/oras-1.3.0
```

全部 bundle、外部根、平台、精确 image、layout、全新输出目录和预算均必填。工具先完整执行 `stage-layout` 的生产验签、预算与层校验；失败时不会调用 ORAS。ORAS 源固定为新快照的 `path@sha256:<原M>`，辅助 tag 只用于运输，不取自 index 注解。它只把目标仓库从批准 image 派生，拒绝 `localhost`、DNS、远端、URL、userinfo、非规范端口和仓库覆盖。子进程只获得独立空认证文件及受限环境，不读取已有 Docker/registry 登录配置、代理或 HOME；以 [ORAS cp](https://oras.land/docs/commands/oras_cp/) 的 `--from-oci-layout`、`--to-plain-http` 参数复制。

复制期间，ORAS 只接触工具拥有的本机回环运输端口；该端口只把符合批准仓库的请求送往原定数字 loopback registry，拒绝重定向、认证挑战以及跨来源或跨仓库的上传位置。合法同仓库上传位置会改写回本机运输端口；签发的最终 image 与实际 registry 目标不变。复制退出后，工具从**同一个精确数字 loopback HTTP 仓库**按原 M 获取 manifest，并获取原 config 与每个被引用层，流式限制大小并检查 SHA-256；M/C 还逐字节比对原 proof。拒绝压缩响应、缺对象、坏对象或错误摘要。ORAS 版本检查、复制及完整 HTTP 读回共享 5 分钟超时；Linux CLI 收到 Ctrl+C/SIGTERM 时取消并收齐仍在同一进程组的 ORAS 后代，不承诺收容自行脱离进程组的程序。先行 `StageLayout` 由显式字节预算约束，尚不能在文件读取或复制中途按时钟强制中断；其前后均检查取消，取消后不会启动 ORAS。所有对象通过后才输出原 M/C、唯一对象数与字节数。失败返回非零且保留新快照以供诊断；目标 registry 可能已有部分对象，工具不会盲删。这一结果只是当次读回事实，不是持续授权、安装成功或真实 DSH 已启动。此 profile 不支持公网认证、重定向、跨仓库重写或自动下载 ORAS。

## 文件与平台权限边界

Linux 用逐层、相对已固定目录描述符的 `O_NOFOLLOW` 打开，避免跟随祖先或叶链接；`O_NONBLOCK` 避免 FIFO 阻塞。
私钥必须归当前有效 UID 所有，至少 owner 可读，group/other 完全无权限，无 setuid/setgid/sticky 位，且只有一个硬链接。
例如 owner 的 0600/0400 文件可用，0640/0644 会拒绝。测试使用原生 Linux 临时目录，不能把 Windows 文件系统权限当作等价证明。

Windows 支持普通本地盘符路径，拒绝 UNC、设备路径、ADS、symlink/junction 等 reparse point，检查句柄实际路径与链接数。
私钥按**打开句柄的实际安全描述符**检查：owner 必须是当前用户，DACL 非空，仅接受基本 allow ACE，
受权 SID 只能为当前用户、SYSTEM、Administrators；任何其他受权 SID 或不支持的 ACE 类型都拒绝。
不会用 Go 的 0600 标志模拟 Windows ACL，不会自动放宽或修复私钥权限。调用者应预先用受控 ACL 交付私钥。
这些限制保守拒绝复杂但可能有效的 DACL，也不声称防御管理员、同一受信账号或受控发布目录被攻破后的竞争修改。
公开输入的内容真实性最终由原字节摘要与生产签名校验决定。

## 安装信任与启用边界

operator 审核的初始根通过既有 **CA SPKI pin + TLS 校验的首次 Join** 固定到生产身份/keyring，
公开目录的 `root.pb` 不能直接替代该流程。入网 token、明文 Join、ApplySpec、Renew、丢库、`--force` 或此工具均不能添根。
不修改既有 wire 契约，不通过远端请求注册根，不自动轮换。根到期后的受保护重新 bootstrap 仍遵守生产限制。

部署装配须将公开 M/C 按 [agent 文档](agent.md)的目录约定交付，向生产控制流程传递已签 `DshRuntime`；
激活仍经过现有生产 S 的当前 key 检查、持久高水位、防回滚、intent 及真实 Engine 身份/启动闭窗。
本工具的 sign/verify 成功不更新这些状态，也不承诺部署就绪或现场可用。
