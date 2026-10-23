# Docker 正式发行材料签发与严格导入

`infra/agent/cmd/runtime-release` 为发布者签发 Docker-only 的正式发行材料，并为装配者严格导入公开材料。
它直接调用生产 `releasetrust.Merge`、`releaseverify.Verify` 和 `VerifyOCI`，签名正文仅调用生产
`CanonicalReleaseBody`。本工具没有另一套签名格式、JCS 或 OCI 解析器，不生成任何身份、根或部署配置。
规范见 [RT0](../../contracts/runtime/README.md)，生产启用流程见 [agent](agent.md)。

本批只交付声明、公开根及单平台原始 manifest/config，**不是完整可部署发行包**。
NAS 通过[公开安装配方](nas.md#消费独立审核的正式运行时)复用严格导入，装配受 receipt 保护的正式模板、公开根和原始 proof。
registry 下载与认证、镜像层离线载体、build 入口参数化均不在此工具内；签名通过也不代表镜像层已经到位。
不支持 native、归档混合声明、背书链管理或根自动轮换。

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
