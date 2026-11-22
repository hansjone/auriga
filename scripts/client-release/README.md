# 现场桌面客户端发行

本工具独立于 Linux 服务端 ./auriga。Windows PowerShell5.1 使用本目录 auriga-client.cmd，Node 入口为 cli.mjs。当前配方仅 win-x64；平台标识、引导程序布局、安装器命名及本地路径规则集中在 platform.mjs，后续增加 Linux 客户端在此登记并添加构建配方，来源和签名协议沿用。

## 与 #373 发行附件的关系

Linux 累积 devkit 保持原 devkit-catalog.json、blobs 和增量流程。Windows 选简单的独立附件 auriga-client-windows-<锁SHA256>.tar，与同版源码归档分发；依赖未变可复用原附件。client-inputs-release.json 绑定目标、锁摘要、windows-client.json 摘要及引导 node.exe 摘要；windows-client.json 列出全部输入长度和 SHA256。SHA256SUMS 绑定两份元数据，沿用 #373 同一发行私钥对原字节 Ed25519 签名，.sig 为标准 base64。release-public.pem 只是公开副本，现场须 --verify-key 或独立批准的 --trust-fingerprint 核身份，不能自带钥自行证明身份。

从独立批准的发行说明取得源码归档、Windows 套件归档和 node.exe 的 SHA256。先用系统 Get-FileHash 核验两份归档，解包后再核验引导 Node。薄壳使用 AURIGA_CLIENT_DEVKIT 与独立批准的 AURIGA_CLIENT_NODE_SHA256，执行前也检查该 Node 的摘要。构建 Node 的版本由目标源码的 `scripts/client-release/build-node.json` 固定。

联网 Windows x64 维护机、批准源码目录中串行准备：

```powershell
New-Item -ItemType Directory D:\exports\client-kits
node scripts/client-release/prepare.mjs D:\exports\client-kits
node scripts/client-release/sign-inputs.mjs --devkit D:\exports\client-kits\auriga-client-windows-<锁SHA256> --key-file C:\secure\issuer\release-private.pem --version 0.2.0
```

用 Windows 系统 tar 保留套件顶层目录，输出 `auriga-client-windows-<锁SHA256>.tar`。通过批准的 Forgejo 转运将 tar 和独立核对的整包 SHA256 交给 Linux 维护者。`prepare-release.sh --windows-client ... --windows-client-sha256 ...` 使用同一发行钥核内部签名、目标锁和归档内容，再自动并入发行清单、下载说明和总签名。完整命令见根 [MAINTAIN.md](../../docs/release/maintainer.zh.md)。签名前复核全部输入、私钥权限及版本，已签目录须保留原签名。发行私钥始终由维护者保管。准备过程通过 HTTPS 读取固定锁的依赖，离线构建证据由现场构建另行生成。

输入包括构建 Node22.19.0/npm10.9.3、根 packageManager 固定 pnpm、完整 Windows pnpm store（含 esbuild/rollup 可选二进制）、upstream.json 固定运行 Node/zip摘要、内置 pnpm/integrity、已提交 DSH npm lock 对应完整 Windows 生产闭包（含 DSH runtime/LibreOffice）及插件生产依赖。broker/helper、UI、插件由现场当前源码重建，不使用旧产物替代，不改上游 DSH；可选宿主 peer 不另装副本。

2026-11-17起，Windows附件还包含 `python/cpython-3.12.14+20260901-x86_64-pc-windows-msvc-install_only.tar.gz`、`python/wheels/*.whl` 与个人环境引导输入 `python/pip/pip-26.3-py3-none-any.whl`。完整URL、长度、SHA256及许可证固定在 `clients/installer/locks/python-win-x64.json`；联网prepare按锁获取并校验全部15个输入，windows-client.json逐文件登记。该锁显式参与锁键，Python pin或wheel锁变化必需重出附件，旧附件拒绝复用；不得现场改版本或用sdist替代wheel。

离线构建先验附件，再校验Python/wheel锁摘要；使用内置解释器的 `-I -S` 和stdlib解包wheel到 `payload/runtime/python/Lib/site-packages`，不调用pip、安装脚本、构建后端或联网解析。移除pip、Lib/ensurepip及内嵌wheel、开发调试文件，省略wheel的可选CLI脚本，保留许可证原文，生成 `runtime/python/runtime.json`（含全部wheel摘要、包版本和许可证路径）。固定解释器再以 `-I -S -B -m compileall --invalidation-mode unchecked-hash` 预编译Lib/site-packages，pyc随包清单校验。运行时随完整安装包更新及回滚；现有自动更新不采用二进制差分，即使Python pin没变，每次更新仍携带完整运行时。

隔离构建后可离屏验证，禁止指向真实安装目录：

```powershell
node clients/installer/scripts/python-smoke.mjs D:\exports\desktop-102\auriga-desktop-<版本>-win-x64\payload --dsh
$env:AURIGA_PYTHON_INPUTS = 'D:\client-kit'
node --test clients/installer/test/python-runtime.test.mjs
```

冒烟检查13包精确版本、许可证及PATH上的 `python/python3` 导入office库。实际运行时位于 `versions/<id>/runtime/python`，仅影响DSH及子进程环境，不修改系统Python。系统py继续按原PATH解析。安装时创建当前用户SID显式保护DACL，复制校验后拒绝普通文件访问的内容写入和新建目录项，保留删除/WriteAttributes供Windows清理使用。运行时无pip/ensurepip，不支持向受管环境装依赖。发现清单外文件提示 `desktop.python_runtime_modified`，退出后重新运行同版本安装包修复。 管理员启用SeRestorePrivilege并使用备份语义访问时，可覆盖DACL拒写。Node/libuv文件打开使用FILE_FLAG_BACKUP_SEMANTICS，因此此类令牌下的Node写入也属于该边界。启动前字节校验仍拒绝已修改运行时，修复路径相同。[Windows备份语义](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/ns-fileapi-createfile2_extended_parameters)Java和其他Python版本的审核签名按需渠道仍属P3。

Windows 自带 PowerShell5.1、System32 tar、.NET Framework4.x Framework64/v4.0.30319/csc.exe 与引用程序集用于认证 helper、托盘和自解压向导。无需额外 .NET SDK/NuGet/MSBuild/7-Zip/WiX 或付费打包工具。系统工具摘要固定在输入清单，补丁/工具变化需在匹配受信 Windows 基线重出套件，不能复制系统目录冒充便携 SDK。锁键包括根/一级 workspace manifests、workspace/config/pnpm lock、插件 npm lock、DSH npm lock、upstream pin、构建 Node 和目标；业务源码可复用，依赖/锁改变明确拒绝。

## 无 Git 来源及离线构建

保留 #373 源码包的 auriga-source.json、SHA256SUMS、SHA256SUMS.sig 和 release-public.pem。清单优先，即使现场后建 .git 也不替换基线。验发行身份、元数据摘要、文件表与基线快照，再按 Go 同格式 mode/sha256/path 换行计算当前源码摘要。已知文本 CRLF、shell BOM、执行模式沿 #373 规范；支持普通源码新增/修改/删除。固定上游 commit，记录当前/基线摘要、发行者指纹和 dirty/siteChanges。缺失、篡改或没有独立信任拒绝；无清单时仍支持原 Git checkout，两者都无则拒绝。

Go→Node 互操作在 deploy/offline/desktop_source_test.go 检查。凭据、数据、bin、node_modules、target/dist、元数据等按 #373 规则排除；客户端生成的 clients/dsh-plugin/ui 在签名基线未含它时也排除，避免构建输出变成源码改动。为防止未记录内容参与构建，构建前拒绝源码树中的 node_modules、dist、.pnpm-store，以及基线未含的 clients/dsh-plugin/ui。构建本身不再向源码树写入：先把源码复制到 `<out>\build-src`（文件集合与源码身份相同，按 sourcePathAllowed 过滤：没有 node_modules、.git、`.env*`、`*.pem`、`*.key`、`*.exe`、顶层归档等；签名源码包另带四个签名元数据文件），worker 在装依赖前先要求副本摘要等于已校验的源码身份（sourceTreeSha256），不等即拒绝构建；随后在副本里恢复依赖、打包 UI 和插件，结束后无论成败都删除副本（Node `fs.rmSync`，超长路径可用）。“构建期间未变化”仍按用户给的源码目录复核，因此同一份源码可连续构建。旧版本残留可用 `auriga-client clean-source <源码目录> [--include-ui]` 一键清理：遍历规则与上述构建前门禁完全一致（不进入 .git、bin、data、target、deploy/compose/var、.dev 等），仅删任意层级的 node_modules、dist、.pnpm-store（`--include-ui` 另含 clients/dsh-plugin/ui，但该目录属于签名基线或已被 Git 跟踪时拒绝），链接只删链接不跟随，目录无 scripts/client-release/build-node.json 时拒绝。--online 与 --offline 同此行为。依赖变更仍由 Windows 锁门禁拒绝。来源记录用于审阅追溯，不代替独立批准；其他额外产物放源码之外。

```powershell
.\scripts\client-release\auriga-client.cmd build --offline --devkit $env:AURIGA_CLIENT_DEVKIT --verify-key D:\approved\release-public.pem --version 1.0.2-site.1 --sequence 102 --release-public-key C:\secure\site-desktop\desktop-public.pem --resource-trust-json D:\site\resource-roots.json --out D:\exports\desktop-102
```

--devkit-sha256 仅保留给有独立批准摘要的旧 Git 输入；正式无 Git 路径用同一发行钥核签名源码与 Windows 附件，不能用此参数跳过源码验签。本机固定磁盘输入/公钥根/签名目录/输出父目录要求，拒绝 UNC、设备路径、映射网络盘和 junction。离线介质先复制到本地，输出目录须全新，单一源码目录不并发构建。

构建先核全部输入，把 store 复制到新输出目录，以 --offline --frozen-lockfile --ignore-scripts 恢复独立工作区依赖；清空代理，Node 子进程继承拒绝 TCP/TLS/HTTP/fetch/DNS（含 Resolver）/UDP 的 preload。原生子程序为本地csc、tar、esbuild及固定Python（以 `-I -S -B` 运行stdlib wheel解包器和compileall）；生产闭包只复制本地输入，不用npm view/联网解析。成功保留offline-evidence.json和空network-denials.log，出现拒绝请求则失败。它不是恶意源码/原生程序的OS沙箱，严格断网仍建议专用Windows VM；不改系统网络/防火墙、不操作用户桌面、不启动常驻项目服务。

## 现场 deps（`--client`）与文件源 / 系统工具基线

现场由 Go `auriga deps … --client` 调用本目录 `site-deps.mjs`（也可直接用 Node）。可写缓存布局与服务端一致：`inputs/client/<锁摘要>/`（锁摘要即 `inputs.mjs` 的 `lockIdentity().lockSha256`）；系统工具基线写在缓存根 `client-tools-baseline.json`。

- **files 源**（`deps.sources.files`）：锁定 URL 映射为 `<files>/<URL 路径>`，没有再用 `<files>/<文件名>`；路径逐段百分号解码（`%2B` → `+`），解码后为空、`.`、`..` 或含 `/ \ :`、控制字符、Windows 保留字符的一律拒绝。支持 `https://`（不得内嵌凭据；curl 只允许 https 与 https 重定向；`deps.caFile` 作 `--cacert`）与 `file://`（离线介质）。配置了 files 时不回落公网；未配置且 `allowPublic=false` 则失败并说明缺什么。摘要由调用方按锁核对。实现见 `files-source.mjs`，`prepare.mjs` 经 `createDownloader` 接入。已知限制：构建 Node（`build-node.json` 只钉版本）的 zip 摘要来自同一来源的 `SHASUMS256.txt`，没有仓库内固定摘要。
- **npm**：`deps.sources.npm` 经 `prepareInputs(..., { npmRegistry })` 写入 `npm_config_registry`（pnpm / 插件 npm 同源），`deps.caFile` 写入 `npm_config_cafile`；`allowPublic=false` 且未配置 npm 源时 `fetch` 失败。
- **基线**：`deps baseline --client` 登记本机 csc/tar/PowerShell 等摘要；`verifyInputs(..., { toolsBaseline })` 允许当前工具与发行清单或现场基线之一相符，不能用基线绕过非工具文件摘要；都不符时报「Windows 更新导致系统工具变化，请重新 deps baseline --client」。构建入口与 worker 都从 `AURIGA_CLIENT_TOOLS_BASELINE`（`auriga.exe` 设为 `deps.cache`，未登记时只按清单）读取；所用依据写入 `offline-evidence.json` 的 `systemTools`，`sign --review-only` 输出 `systemToolsBaseline` 提示。
- **缓存条目信任**：`fetch`/`import` 把 `site-dependencies.json`（来源、锁摘要、`manifestSha256`）与 `auriga-origin.json` 写进暂存目录后整体改名落位；之后命中缓存时当前 `windows-client.json` 必须等于记录的 `manifestSha256`，缺记录或不符即拒绝。缓存目录本身的管理员 ACL 与并发锁由 Go 侧校验。
- **套件独立信任**：`deps.kits` 套件必须带 `trustFingerprint`，用 `trust.mjs` 验发行签名并核对 `client-inputs-release.json` 绑定的清单摘要与锁摘要；不接受未签名套件或套件自带公钥自证。
- **import**：先复制到 `<cache>/_client-import-staging` 再校验副本（逐文件、锁摘要一致；给了 `--trust-fingerprint` 时另验签），失败不留半成品。
- **build --offline --site-kit**：现场缓存套件无发行签名，按 `--devkit-sha256`（缓存记录的清单摘要）校验套件，`--verify-key`/`--trust-fingerprint` 只核签名源码；worker 同样以该摘要复核。
- **acceptNewDeps**：缓存与套件都未命中时，`fetch` 在 `acceptNewDeps=false` 下拒绝。
- 解析顺序：可写缓存 → `deps.kits` → 缺失（`resolve-devkit`）。单测：`node --test scripts/client-release/site-deps.test.mjs`。

## 联网补依赖（build --online）

现场只改了前端 / workspace 的 pnpm 依赖，或 dsh-plugin 的 npm 依赖，而发行套件里没有这些新包时，`--offline` 会因“依赖锁不匹配”拒绝。联网构建机可以改用：

```powershell
.\scripts\client-release\auriga-client.cmd build --online --devkit $env:AURIGA_CLIENT_DEVKIT --verify-key D:\approved\release-public.pem --site-devkit D:\exports\site-kit-102 --version 1.0.2-site.1 --sequence 102 --release-public-key C:\secure\site-desktop\desktop-public.pem --resource-trust-json D:\site\resource-roots.json --out D:\exports\desktop-102
```

- 与 `--offline` 互斥；必须有 `--verify-key` 或 `--trust-fingerprint`（基础套件必须是发行签名的）和 `--site-devkit`（全新目录，本机固定磁盘，不能在基础套件或输出目录内，也不能包含它们）。参数错误在任何联网、拷贝前报出。
- 先按发行公钥完整校验基础套件（签名、元数据、摘要、逐文件、系统工具），只跳过“锁必须与当前源码一致”；源码身份校验不变。
- 范围：允许变化的只有 `web/pnpm-lock.yaml`、`pnpm-workspace.yaml`、`web/*/package.json`、`clients/*/package.json`、根 `package.json`（`packageManager` 不能变）、`clients/dsh-plugin/package-lock.json`。构建 Node、`.npmrc`、`clients/installer/upstream.json`、Python 锁、DSH 锁、`packageManager` 变化一律报错并列出文件：Java/Python/DSH/Node 版本变化仍需维护方出新套件。锁与套件完全一致时同样报错，提示直接用 `--offline`。
- 派生：把基础套件（只读）复制到 `--site-devkit`，不复制 `SHA256SUMS`、`SHA256SUMS.sig`、`client-inputs-release.json`、`release-public.pem`、`windows-client.json`；再只重做变化的部分。pnpm 相关锁变了：用套件自带 build-node/build-pnpm 在临时 workspace 里 `pnpm fetch --frozen-lockfile --ignore-scripts`，旧 store 作起点只下新增；插件锁变了：删 `plugin-deps` 旧依赖后用套件自带 npm `npm ci --omit=dev --omit=peer`。完整性由 pnpm-lock / package-lock 的 integrity 保证。透传已设置的 `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` 与 `npm_config_registry`，其余 `npm_config_*`/`pnpm_config_*` 去掉；不设置就不传。失败时清理半成品派生目录，基础套件从不被修改。
- 派生套件按与 `sealInputs` 相同的格式写新的 `windows-client.json`，并写 `site-dependencies.json`（基础/新的清单与锁摘要、变化的锁文件、新增包的名称/版本/integrity、“不再需要”的数量；取不到旧 `web/pnpm-lock.yaml` 时数量为 null 并带警告）。旧锁来源：源码基线的 Git 提交（`git show`），或 `--base-pnpm-lock <原始 web/pnpm-lock.yaml>`，摘要必须与基础套件记录一致，路径读不到或摘要不符直接报错（不静默降级），该参数只能与 `--online` 同用。
- 构建：派生套件以刚算出的清单摘要为期望摘要，走与 `--offline` 完全相同的断网构建（网络守卫、环境过滤一致）。`site-dependencies.json` 同时复制到输出目录并在构建结束时打印；不向 `desktop-build.json` / 发布清单加字段。复制完成后、联网前会把派生目录与已校验的基础套件清单逐项复核（路径、大小、SHA-256）。只改了 `web/*/package.json` 或 `clients/*/package.json` 的 scripts、version 等不影响依赖的字段时，锁文件本来就不变：照常派生套件并重封清单，但不联网下载，打印“依赖锁未变，仅 package.json 变化，未联网下载”。若改的是依赖声明却没更新锁，离线构建里的 `pnpm install --frozen-lockfile` 会报错，请先在联网机运行 `pnpm install --lockfile-only` 更新锁再重试。dsh-plugin 的 `package.json` 在插件锁未变时，只允许 scripts、version 等非依赖字段变化；`dependencies`、`optionalDependencies`、`peerDependencies`、`bundleDependencies`/`bundledDependencies` 与基础套件不同会在复制和联网前报错，请先在 `clients/dsh-plugin` 运行 `npm install --package-lock-only` 更新插件锁。`sign --review-only` 在输出目录有 `site-dependencies.json` 时会提示“含现场联网依赖：N 项”：这类输出的输入摘要来自现场派生套件，不在维护方的发行签名之内，签名者须人工审阅该清单。

## 套件清单格式兼容（requires）

规则与服务端 `deploy/offline/devkit_compat.go` 一致，实现见 `compat.mjs`。`client-inputs-release.json` 与 `windows-client.json` 的顶层字段在 `FIELD_REGISTRY` 登记为基线、说明性或能力名；写出时只有出现非基线字段才带 `requires`，现在写出的内容与此前逐字节相同。读到带 `requires` 的清单：有当前脚本不认识的能力、`requires` 类型错误或大小写变体键就拒绝，提示“套件需要更新的客户端发行脚本，请使用该套件发行页对应版本的源码”；说明性字段忽略；没有 `requires` 的旧清单照常。以后加能力字段要同时改 `compat.mjs` 的登记表（测试会检查漏登记）和 Go 的 `deploy/offline/release_client_kit.go`（它对 `client-inputs-release.json` 严格解码；`windows-client.json` 虽为非严格解码，会默默忽略 `requires`，同样要同步）。

## Linux 公钥配置及发布

Linux install 加 --desktop-public-key <独立Ed25519 SPKI PEM>、--desktop-publisher-tenant <实际发行租户UUID> 和 --desktop-confirm-fingerprint <独立核对的64位小写SPKI-SHA256>，三者一并提供。指纹与 Windows keygen 输出 publicKeyFingerprint 独立核对，公钥不符拒绝登记，成功打印已登记指纹。现有安装锁内写原 state 的 desktopPublicKeyFingerprint/desktopPublisherTenant，保留 #373 TrustedKeys、AllowUnsigned、资源初始化及冷备份/回滚。重复安装省略三项时复核 state 中的固定指纹；显式提供公钥仍须确认指纹，拒绝自动换钥或租户；升级保存 operator settings 和 state。

平台仅接收公钥，写 <data>/config/desktop/release-public.pem；stack.env 设置 AURIGA_DESKTOP_PUBLISHER_TENANT_ID。原 renderer/Compose 自动配置 AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE=/etc/auriga/desktop/release-public.pem、AURIGA_RESOURCE_DESKTOP_DIRECTORY=/var/lib/auriga/desktop 和发布租户，桌面卷使用 UID/GID10001:10001、0700。备份发行卷、resource数据库、公钥及安装state。

sign --review-only 展示来源/现场改动、包摘要、公钥/资源根、版本/序号/最低版本/渠道等，Git 来源（无签名源码清单）醒目显示 sourceWarning，须独立核对源码提交及工作区。人工确认 reviewSha256 后才用现场桌面私钥签名。该钥与平台包/Windows附件发行钥分开。publish 要求同目录 desktop-build.json，先核 --public-key 的 SPKI 指纹等于构建记录 publicKeyFingerprint，再通过正常 edge 管理 POST /api/resource/v1/admin/desktop/releases 上传 exe，X-Desktop-Manifest 为已签 JSON base64url；使用近期管理员会话 Cookie、Origin、X-Auriga-CSRF，要求 resource.versions.publish。会话 JSON 仅从 AURIGA_CLIENT_ADMIN_AUTH 环境读，不进入 argv/日志。

发布前核签名、摘要和全平台账本序号；发布后从门户同渠道 latest 验完整清单，再完整流式下载 /api/resource/v1/desktop/packages/<sequence> 核长度/SHA256。拒绝跳转，HTTP 需分别显式确认支持的私有/回环 IP origin。自检失败可能已发布，先看账本，不自动重发或撤回。管理台手动入口 /ops/desktop，完整操作及密钥轮换见根 [MAINTAIN.md](../../MAINTAIN.md#客户端发布windows-开发机)。当前更换桌面发行根需成员受信重新安装；门户/旧版托盘自动更新仍由主控隔离演练验收。
