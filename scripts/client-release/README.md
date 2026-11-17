# 现场桌面客户端发行

本工具独立于 Linux 服务端 ./auriga。Windows PowerShell5.1 使用本目录 auriga-client.cmd，Node 入口为 cli.mjs。当前配方仅 win-x64；平台标识、引导程序布局、安装器命名及本地路径规则集中在 platform.mjs，后续增加 Linux 客户端在此登记并添加构建配方，来源和签名协议沿用。

## 与 #373 发行附件的关系

Linux 累积 devkit 保持原 devkit-catalog.json、blobs 和增量流程。Windows 选简单的独立附件 auriga-client-windows-<锁SHA256>.tar，与同版源码归档分发；依赖未变可复用原附件。client-inputs-release.json 绑定目标、锁摘要、windows-client.json 摘要及引导 node.exe 摘要；windows-client.json 列出全部输入长度和 SHA256。SHA256SUMS 绑定两份元数据，沿用 #373 同一发行私钥对原字节 Ed25519 签名，.sig 为标准 base64。release-public.pem 只是公开副本，现场须 --verify-key 或独立批准的 --trust-fingerprint 核身份，不能自带钥自行证明身份。

发行说明还须独立列出源码归档和 node.exe SHA256，供 Windows 在执行任何附件程序前用系统 Get-FileHash 引导核验。不要从未核验 JSON 临时推导批准值。薄壳要求 AURIGA_CLIENT_DEVKIT 与独立批准的 AURIGA_CLIENT_NODE_SHA256，先核 Node 再执行；无需安装 Git/Node/pnpm。

联网 Windows x64 维护机、批准源码目录中串行准备：

```powershell
New-Item -ItemType Directory D:\exports\client-kits
node scripts/client-release/prepare.mjs D:\exports\client-kits
node scripts/client-release/sign-inputs.mjs --devkit D:\exports\client-kits\auriga-client-windows-<锁SHA256> --key-file C:\secure\issuer\release-private.pem --version 0.2.0
```

用系统 tar 归档该目录，按 #373 受信发行流程作为独立附件分发；sign-inputs 不上传。准备允许联网，只从 HTTPS curl、固定 npm 锁和完整性字段取得依赖，不运行上游安装脚本，不算离线证据。签名前完整复核输入、私钥权限及版本，拒绝覆盖已有签名材料。发行私钥不进入附件、源码、客户端或日志。

输入包括构建 Node22.19.0/npm10.9.3、根 packageManager 固定 pnpm、完整 Windows pnpm store（含 esbuild/rollup 可选二进制）、upstream.json 固定运行 Node/zip摘要、内置 pnpm/integrity、已提交 DSH npm lock 对应完整 Windows 生产闭包（含 DSH runtime/LibreOffice）及插件生产依赖。broker/helper、UI、插件由现场当前源码重建，不使用旧产物替代，不改上游 DSH；可选宿主 peer 不另装副本。

2026-11-17起，Windows附件还包含 `python/cpython-3.12.14+20260901-x86_64-pc-windows-msvc-install_only.tar.gz` 与 `python/wheels/*.whl`。完整URL、长度、SHA256及许可证固定在 `clients/installer/locks/python-win-x64.json`；联网prepare按锁获取并校验全部14个输入，windows-client.json逐文件登记。该锁显式参与锁键，Python pin或wheel锁变化必需重出附件，旧附件拒绝复用；不得现场改版本或用sdist替代wheel。

离线构建先验附件，再校验Python/wheel锁摘要；使用内置解释器的 `-I -S` 和stdlib解包wheel到 `payload/runtime/python/Lib/site-packages`，不调用pip、安装脚本、构建后端或联网解析。移除pip/开发调试文件，省略wheel的可选CLI脚本，保留许可证原文，生成 `runtime/python/runtime.json`（含全部wheel摘要、包版本和许可证路径）。运行时随完整安装包更新及回滚；现有自动更新不采用二进制差分，即使Python pin没变，每次更新仍携带完整运行时。

隔离构建后可离屏验证，禁止指向真实安装目录：

```powershell
node clients/installer/scripts/python-smoke.mjs D:\exports\desktop-102\auriga-desktop-<版本>-win-x64\payload --dsh
$env:AURIGA_PYTHON_INPUTS = 'D:\client-kit'
node --test clients/installer/test/python-runtime.test.mjs
```

冒烟检查13包精确版本、许可证及PATH上的 `python/python3/py` 导入office库。实际运行时位于 `versions/<id>/runtime/python`，仅影响DSH及子进程环境，不修改系统Python。Java和其他Python版本的审核签名按需渠道仍属P3。

Windows 自带 PowerShell5.1、System32 tar、.NET Framework4.x Framework64/v4.0.30319/csc.exe 与引用程序集用于认证 helper、托盘和自解压向导。无需额外 .NET SDK/NuGet/MSBuild/7-Zip/WiX 或付费打包工具。系统工具摘要固定在输入清单，补丁/工具变化需在匹配受信 Windows 基线重出套件，不能复制系统目录冒充便携 SDK。锁键包括根/一级 workspace manifests、workspace/config/pnpm lock、插件 npm lock、DSH npm lock、upstream pin、构建 Node 和目标；业务源码可复用，依赖/锁改变明确拒绝。

## 无 Git 来源及离线构建

保留 #373 源码包的 auriga-source.json、SHA256SUMS、SHA256SUMS.sig 和 release-public.pem。清单优先，即使现场后建 .git 也不替换基线。验发行身份、元数据摘要、文件表与基线快照，再按 Go 同格式 mode/sha256/path 换行计算当前源码摘要。已知文本 CRLF、shell BOM、执行模式沿 #373 规范；支持普通源码新增/修改/删除。固定上游 commit，记录当前/基线摘要、发行者指纹和 dirty/siteChanges。缺失、篡改或没有独立信任拒绝；无清单时仍支持原 Git checkout，两者都无则拒绝。

Go→Node 互操作在 deploy/offline/desktop_source_test.go 检查。凭据、数据、bin、node_modules、target/dist、元数据等按 #373 规则排除；客户端生成的 clients/dsh-plugin/ui 在签名基线未含它时也排除，避免构建输出变成源码改动。为防止未记录内容参与构建，启动 worker 前和 worker 内恢复依赖前均拒绝源码树中的 node_modules、dist、.pnpm-store，以及基线未含的 clients/dsh-plugin/ui，并给出对应 PowerShell 清理命令。建议每次把源码解压到全新目录；重复构建须先核对并清理上次生成内容。依赖变更仍由 Windows 锁门禁拒绝。来源记录用于审阅追溯，不代替独立批准；其他额外产物放源码之外。

```powershell
.\scripts\client-release\auriga-client.cmd build --offline --devkit $env:AURIGA_CLIENT_DEVKIT --verify-key D:\approved\release-public.pem --version 1.0.2-site.1 --sequence 102 --release-public-key C:\secure\site-desktop\desktop-public.pem --resource-trust-json D:\site\resource-roots.json --out D:\exports\desktop-102
```

--devkit-sha256 仅保留给有独立批准摘要的旧 Git 输入；正式无 Git 路径用同一发行钥核签名源码与 Windows 附件，不能用此参数跳过源码验签。本机固定磁盘输入/公钥根/签名目录/输出父目录要求，拒绝 UNC、设备路径、映射网络盘和 junction。离线介质先复制到本地，输出目录须全新，单一源码目录不并发构建。

构建先核全部输入，把 store 复制到新输出目录，以 --offline --frozen-lockfile --ignore-scripts 恢复独立工作区依赖；清空代理，Node 子进程继承拒绝 TCP/TLS/HTTP/fetch/DNS（含 Resolver）/UDP 的 preload。原生子程序为本地csc、tar、esbuild及固定Python（仅以 `-I -S -B` 运行stdlib wheel解包器）；生产闭包只复制本地输入，不用npm view/联网解析。成功保留offline-evidence.json和空network-denials.log，出现拒绝请求则失败。它不是恶意源码/原生程序的OS沙箱，严格断网仍建议专用Windows VM；不改系统网络/防火墙、不操作用户桌面、不启动常驻项目服务。

## Linux 公钥配置及发布

Linux install 加 --desktop-public-key <独立Ed25519 SPKI PEM>、--desktop-publisher-tenant <实际发行租户UUID> 和 --desktop-confirm-fingerprint <独立核对的64位小写SPKI-SHA256>，三者一并提供。指纹与 Windows keygen 输出 publicKeyFingerprint 独立核对，公钥不符拒绝登记，成功打印已登记指纹。现有安装锁内写原 state 的 desktopPublicKeyFingerprint/desktopPublisherTenant，保留 #373 TrustedKeys、AllowUnsigned、资源初始化及冷备份/回滚。重复安装省略三项时复核 state 中的固定指纹；显式提供公钥仍须确认指纹，拒绝自动换钥或租户；升级保存 operator settings 和 state。

平台仅接收公钥，写 <data>/config/desktop/release-public.pem；stack.env 设置 AURIGA_DESKTOP_PUBLISHER_TENANT_ID。原 renderer/Compose 自动配置 AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE=/etc/auriga/desktop/release-public.pem、AURIGA_RESOURCE_DESKTOP_DIRECTORY=/var/lib/auriga/desktop 和发布租户，桌面卷使用 UID/GID10001:10001、0700。备份发行卷、resource数据库、公钥及安装state。

sign --review-only 展示来源/现场改动、包摘要、公钥/资源根、版本/序号/最低版本/渠道等，Git 来源（无签名源码清单）醒目显示 sourceWarning，须独立核对源码提交及工作区。人工确认 reviewSha256 后才用现场桌面私钥签名。该钥与平台包/Windows附件发行钥分开。publish 要求同目录 desktop-build.json，先核 --public-key 的 SPKI 指纹等于构建记录 publicKeyFingerprint，再通过正常 edge 管理 POST /api/resource/v1/admin/desktop/releases 上传 exe，X-Desktop-Manifest 为已签 JSON base64url；使用近期管理员会话 Cookie、Origin、X-Auriga-CSRF，要求 resource.versions.publish。会话 JSON 仅从 AURIGA_CLIENT_ADMIN_AUTH 环境读，不进入 argv/日志。

发布前核签名、摘要和全平台账本序号；发布后从门户同渠道 latest 验完整清单，再完整流式下载 /api/resource/v1/desktop/packages/<sequence> 核长度/SHA256。拒绝跳转，HTTP 需分别显式确认支持的私有/回环 IP origin。自检失败可能已发布，先看账本，不自动重发或撤回。管理台手动入口 /ops/desktop，完整操作及密钥轮换见根 [MAINTAIN.md](../../MAINTAIN.md#客户端发布windows-开发机)。当前更换桌面发行根需成员受信重新安装；门户/旧版托盘自动更新仍由主控隔离演练验收。
