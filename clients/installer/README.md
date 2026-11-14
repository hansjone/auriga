# Auriga Desktop installer (Windows test build)

设计见 `docs/design/dsh-installer.zh.md`。包一层不 fork，上游 `@deepseek-ai/dsh`、自带 Node 与 pnpm 是可替换组件，我们的插件、首启配置与安装核心单独存放。

```powershell
cd clients/installer
node scripts/build.mjs --exe                 # 默认上游版本见 upstream.json，同时出单文件安装器
node scripts/build.mjs --upstream 0.2.1-rc.4 # 换上游版本重新出开发包
node scripts/build.mjs --platform-origin <https地址> --without-libreoffice
pnpm test                                    # INSTALLER_STAGE=<包目录> 可额外校验成品
node scripts/e2e.mjs --old <包目录或setup.exe> --sandbox <目录>
```

本包是 pnpm 工作区成员 `@auriga/installer`，脚本只用 Node 内置模块。输出默认在已忽略的 `target/`。

## 发布新版本

### 版本与发行钥

Desktop 使用自己的 semver，保存在 `clients/installer/desktop.json`。上游 DSH、Node、插件版本分别记入发行清单 `components` 和构建记录。上游或插件更新也递增 Desktop 版本，首版通道为 `stable`。`--desktop-version` 可覆盖开发构建版本。

发行清单使用独立 Ed25519 离线钥。运营者在本机私有目录生成，限制 ACL 为本人，并加密离线备份。以下命令将私钥直接写入文件。

```powershell
openssl genpkey -algorithm ED25519 -out <私有目录>/desktop-private.pem
openssl pkey -in <私有目录>/desktop-private.pem -pubout -out <公钥目录>/desktop-public.pem
```

在 GitHub 仓库变量 `DESKTOP_RELEASE_PUBLIC_KEY` 配置公钥 PEM，`DESKTOP_PLATFORM_ORIGIN` 可配置首启平台 HTTPS origin。私钥始终留在本机私有目录。消息网络、DSH runtime 和插件市场使用各自的钥。

构建时公钥固定到包内 `auriga/runtime/release-public-key.txt`，参与逐文件摘要和包 ID。缺少公钥的开发包会拒绝更新。签名脚本要求包内公钥与签名私钥匹配，当前发行流程更换发行钥须人工重新安装并同步平台信任配置。

### CI 构建

正式包由 `.github/workflows/installer.yml` 在 `windows-latest` 构建。递增 Desktop 版本并合入 main 后，在对应提交打 `desktop-v<版本>` tag，或手动运行 Installer 工作流并选择这个 tag，填写发行序号、最低可升级版本。工作流检查源码提交属于 main。手动选择分支只能用于验证，其制品不能签名发布。PR 构建不嵌生产公钥，也不上传发行制品。

发行序号为严格递增正整数，默认采用工作流 run number。重跑或下架后发行仍须选择比已发布最高序号更大的值。tag 的版本后缀作为构建版本。

CI 制品包含完整 ZIP、EXE、`desktop.sha256`、`desktop-release.unsigned.json` 和 `desktop-build.json`。构建记录包含源码 commit、Desktop 和各组件版本、ZIP SHA-256、EXE SHA-256、包 ID、workflow 路径、run、event、ref、main 祖先检查结果，以及包内公钥 SPKI DER 的 SHA-256 指纹。工作流执行安装器单测、包校验和隔离安装/启动/卸载验证后上传制品。

### 审阅与离线签名

主控下载 CI 制品，先核对工作流来源、源码 commit、版本和 CI 记录，并用 `Get-FileHash -Algorithm SHA256` 核对 ZIP 和 EXE 的摘要。将 ZIP、未签名清单及构建记录放到签名机。

从 `clients/installer` 运行审阅命令。

```powershell
node scripts/sign-release.mjs --review-only manifest=<制品目录>/desktop-release.unsigned.json buildRecord=<制品目录>/desktop-build.json archive=<完整ZIP> output=<输出目录>/desktop-release.json
```

脚本核对 ZIP 与 CI 清单/记录，实际解包并验证包内文件和公钥指纹，输出版本、包摘要、上游组件、序号、有效期、workflow、ref、commit、公钥指纹和 `reviewSha256`，并保存 `<output>.review.json` 本地审阅检查点。审阅阶段无需私钥。来源必须是通过 main 祖先检查的 `desktop-v<版本>` tag 构建，否则拒签。构建记录仍须与 GitHub 对应运行人工核对，记录本身不是 CI 来源的加密证明。

核对输出与 CI 记录一致后，使用刚审阅的 `reviewSha256` 确认。

```powershell
node scripts/sign-release.mjs manifest=<制品目录>/desktop-release.unsigned.json buildRecord=<制品目录>/desktop-build.json archive=<完整ZIP> output=<输出目录>/desktop-release.json keyFile=<私有目录>/desktop-private.pem --confirm-sha256=<reviewSha256>
```

确认摘要绑定完整未签名清单与构建记录。审阅后改变版本、摘要、说明、有效期等参数会被拒绝，须重新审阅。签名私钥推导出的公钥必须与 ZIP 内固定公钥指纹相同。输出文件须尚不存在。签名成功后清理检查点。私钥及其路径不会出现在脚本输出里。

### 同包续期

自动更新清单默认有效 30 天。到期前可从原 tag 构建的已核对制品复制未签名清单，仅将 `sequence` 改成高于平台已见最高序号的新整数，将 `notAfter` 延长。保留原 ZIP、构建记录、Desktop 版本、包 ID、SHA、大小和组件版本。重新执行上述 `--review-only` 和 `--confirm-sha256` 两步，再在管理台上传新签名清单和原 ZIP。平台要求同版本的包身份相同且有效期严格延长，拒绝同版本替包。

续期会成为新的上架记录并写发布审计。已安装该版本的客户端只记住新的序号，不重复安装。即使错过续期，登录页与匿名包下载仍提供最新上架的安装包供新用户下载；自动更新客户端继续严格拒绝过期清单，运营者须完成续期后才恢复自动更新。

### 平台接线和发布

resource 服务需要以下配置。

- `AURIGA_RESOURCE_DESKTOP_DIRECTORY=/var/lib/auriga/desktop`，独立持久卷，由服务账号独占写入。
- `AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE=/run/auriga/desktop-public.pem`，与 CI 构建相同的发行公钥，只读挂载。
- `AURIGA_RESOURCE_DESKTOP_PUBLISHER_TENANT_ID=<发行管理员租户UUID>`，固定公开发行来源。仅该租户内具有发布权限的管理会话可以管理，匿名请求无需选择租户。

卷须支持 OS 文件锁和原子 rename。插件市场对象存储上限为 64 MiB，Desktop 使用独立卷存放大包。发行卷与 resource 数据库须纳入备份。反向代理允许上传实际包大小，产品上限 4 GiB，并设置足够超时。签名清单放在 base64url `X-Desktop-Manifest` 头中，代理和服务端头上限至少 44 KiB。

管理台进入“运维 → 客户端版本” `/ops/desktop`，完成近期身份验证，选择签名清单和 ZIP 发布。管理操作要求有效 ADMIN 会话、`resource.versions.publish` 和 step-up。平台验证签名、有效期、SHA、大小、版本和序号后发布。

发布和下架先原子持久化发行状态及待投递审计，再经 resource outbox 向统一审计链路投递。数据库收据防止重试产生重复事件，审计暂不可用时每 30 秒补送。统一审计页可按 `resource.desktop.publish` 或 `resource.desktop.withdraw` 查询。

### 下载、更新和撤回

云端登录首页显示版本、大小、发布时间、SHA-256 和 Windows 下载按钮。以下两个 GET 接口允许匿名访问，返回公开签名清单和 ZIP，复用 edge 的按客户端 IP 限速。

- `/api/resource/v1/desktop/latest`
- `/api/resource/v1/desktop/packages/{sequence}`

edge 精确放行这两种路径的 GET。客户端也使用相同匿名接口，首启检查一次，此后每 6 小时检查，可在 Auriga 设置中关闭或手动检查。下载仅使用首启绑定的 HTTPS origin。

用户点击更新后下载并校验暂存，须退出整个 Desktop/DSH 进程，启动器随后运行原有安装事务，下次启动进入新版。仅关闭浏览器页不足以退出 DSH。失败由安装事务还原旧版和 profile，用户数据保留。下载采用 60 秒无数据的空闲超时，持续接收数据不受总时长限制。一次更新最多自动尝试三次，中断后通过单个 `Range: bytes=<已下载长度>-` 续传，服务端返回 200 时安全重下，拼接后重新验证完整 SHA。重试耗尽或进程退出后，下次用户操作重新下载。进程被杀时残留锁记录 PID 和创建时间，下次启动在 DSH 启动前清理锁及暂存目录；卸载也清理残留。

设置页的更新操作等待后台完成下载、校验和排队，不套用普通状态读取的 15 秒超时；下载的空闲超时和校验规则仍由后台执行。默认冒烟失败时，启动器仅输出固定原因码 `desktop.smoke_failed`，不输出上游配置或错误详情。

下架当前记录后，登录首页和更新检查返回上一个仍上架的记录。续期会创建新记录，若要撤回某个坏包，应将该包的全部续期记录一并下架。已安装更高版本的客户端会拒绝降级，可执行 `rollback.cmd`，或等待更高版本的修复包。下架保留最高序号，修复包须新版本、新序号，同包续期须新序号和延长有效期。第一代安装器需先人工安装一次带更新能力的包。

隔离验证运行 `node --test test/update-e2e.test.mjs`，使用随机端口发布替身、真实 ZIP、临时测试钥和安装事务，验证坏包拒绝、退出安装和数据保留。真实 DSH 宿主另按 `scripts/e2e.mjs` 验证。

# P1-E 集成状态

正常入口仍为 `bin/auriga-desktop.cmd`。启动器独立核验共享 Skill 的冻结 ledger、事务、回执、原始清单、归档及实际落盘字节，再持锁复核 revision 和字节后持久化高水位与激活许可。启动器单实例监督 DSH 子进程，记录 PID/创建时间和后代；撤销、最早租约截止、身份改变或回拨触发 `taskkill /T /F`，确认退出后隔离共享根并写停用回执。插件关闭或卸载不影响该监督。

C 正常持锁或在两次加锁间提交新 revision 时，E 有界退避重试，不把竞争当成信任失败。尚未激活且 current 为空时，缺公钥、陈旧信封或不认识的 ledger 只禁止共享，不阻止私有 Desktop 启动。K2 的 desiredState 存在时优先于 enabled。selectionRevision/generation 前进需要新的 prepared 证明和完整验签，不视为身份切换；回退拒绝。从 private_only 恢复共享先停机，在同一启动器 session 里自动重新拉起 DSH，并提示建议新建会话。

构建时以 `--resource-trust-json <可信公钥文件>` 固定独立的 release/revocation/downgrade Ed25519 公钥；文件按 `{release:{kid,publicKeySpkiBase64},revocation:{...},downgrade:{...}}` 提供，禁止测试钥用于部署。未固定资源公钥的包只能使用私有技能。构建同时带入冻结 JSON Schema，使用锁定上游 Ajv 完整校验。运行时不从插件、环境变量或制品读取公钥。

构建器会拒绝 unified 向量的公开测试公钥，即使改换 kid 也不能作为部署根。启动时校验包内 runtime、公钥和配置的 manifest 摘要。相同 OS 用户仍可一并改写程序、manifest 或 guardian 数据；本实现没有服务账户或操作系统隔离，包内校验不能防御这种同用户主动篡改。Windows LastBootUpTime 也可能随系统校时变化而触发保守重验，不应当作不会变化的硬件开机编号。

每秒检查租约和轻量文件属性清单；文件、身份或代次变化时立即完整复核，稳定时最多每 30 秒完整复核一次，临近租约截止也复核。进程树每 15 秒枚举及停机前即时枚举；高水位只在变化时写入，时钟在完整核验时持久化。C 的已验证旧视图允许在新 prepared 集合等待切换期间保留，E 自有 authorized-ledger.json 只保存冻结格式的已授权账本，不能代替新集合的验签。

仅支持本地固定 NTFS 卷，拒绝网络盘、映射盘、UNC 与重解析路径。持久化以同目录临时文件、fsync、rename 为前提。已知陈旧锁必须在停止全部受管写者、复核 lockId 后回收；未知锁保留并失败封闭。进程崩溃后的下次启动先根据可信账本停止旧树并恢复隔离；高水位与停用证据放在独立 DSH_HOME，不随应用回滚删除。存在未隔离共享状态时禁止退回不带 guardian 的旧版本。

第 2 轮决定允许隔离后以 `private_only` 正常重启。启动器显示“共享技能已停用，建议新建会话”，原历史保留，也允许恢复旧会话；已载入旧上下文的影响是已知残余风险。guardian 崩溃至下次启动期间，DSH 可能继续运行。正常配置和预置技能均保留；本提交未经 NAS 部署，Linux 争锁、C 的真实安装联调及完整页面验收由 F 线执行。

本机 Windows 实测：先按 installer 锁在 `target/dsh` 安装隔离的 DSH，再在仓库根执行 `node clients/installer/scripts/guardian-real-dsh.mjs`；它只使用临时 DSH_HOME、随机端口，并在结束时停止自己的进程。缓存摘要目录内应有唯一匹配摘要的原字节文件；签名撤销表也以内容摘要缓存。prepared 内容位于 `staging/<transactionId>/<manifest.name>/`，applied 回执按 K 路径布局。C 只能在 E 许可的身份、代次和完整集合完全一致时切换 current，停用回执须先对账后才能重新申请共享。

## IP 模式 HTTP 更新的信任边界

域名模式仍只允许 HTTPS。HTTP 仅支持明确确认的内网或回环字面 IP：IPv4 为 RFC1918 私有网段和 127.0.0.0/8，IPv6 为 ULA（fc00::/7）和 ::1。公网 IP、域名、localhost、IPv4 映射 IPv6 和十进制等 IP 别名均拒绝，避免安装后无法登录。示例中的私有 IP 仅用于说明：

```powershell
node payload/auriga/runtime/cli.mjs install --payload payload --root <独立安装目录> --data-home <独立数据目录> --platform-origin http://192.168.2.10:8080 --platform-access-mode ip --confirm-platform-origin http://192.168.2.10:8080 # gitleaks:allow
```

构建器 build.mjs 也接受这三个参数作为安装配置。HTTPS 地址会规范化主机名、默认端口、末尾斜杠和路径，只保存 origin；拒绝带用户名或口令的地址。HTTP 地址和确认值先分别规范化后比较，允许末尾斜杠和默认端口，但拒绝路径、查询和片段。主机必须是上述私有或回环字面 IP。

首次绑定写入安装目录 desktop-origin.json。带明确确认参数重新安装时，安装器按本次确认结果重写该文件，不保留旧格式或多余字段。已有 HTTP 绑定缺少 ip 模式或确认标记时，必须使用上面的三个参数重新安装；不自动兼容或猜测同意，没有参数的重装会明确报错，客户端设置也会显示需要重新确认的提示。正常升级未带确认参数时保留已验证的绑定。Windows install.ps1 对应参数为 -PlatformOrigin、-PlatformAccessMode 和 -ConfirmPlatformOrigin。

更新的信任根始终是安装包内固定的发行 Ed25519 公钥，绝不从平台下载公钥。签名覆盖版本、序号、签发时间和包 SHA-256；客户端持久化已见最高版本、序号和签发时间，并拒绝版本回退、旧清单和篡改。下载完成先核对签名清单的包 SHA-256，再解包验证文件摘要和包 ID，最后执行安装事务及冒烟回滚。HTTP 仍会泄露下载元数据，也可被阻断；签名不能保证可用性或保密性。

首次下载的安装程序及公钥需要通过受信的独立渠道核验 SHA-256 或人工预置，不能把 HTTP 首次下载本身当作信任来源。门户 HTTP 下载入口可用不代表首次引导已经具备安全认证。
