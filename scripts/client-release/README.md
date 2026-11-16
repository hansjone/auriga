# 现场 Windows 客户端发行

与 `.github/workflows/installer.yml` 共用 broker、插件、安装器构建及 Ed25519 发行协议。现场无需访问 GitHub、Forgejo 或 npm。输出仍是 `AurigaDSH-Setup-<版本>.exe`、诊断 zip、`desktop-build.json`、`desktop-release.unsigned.json`、`desktop.sha256`；签名后生成 `desktop-release.json`。

## 维护者准备 Windows 开发套件

在联网 **Windows x64** 维护机、批准源码 checkout 内执行（串行，不与平台冷建并行）：

```powershell
New-Item -ItemType Directory D:\exports\client-kits
node scripts/client-release/prepare.mjs D:\exports\client-kits
```

准备工具使用 Windows curl（HTTPS、拒绝跳转）获取官方 Node 归档和摘要，用套件 Node/npm 安装锁定依赖。准备步骤允许联网，不是离线成功证据。输出 `auriga-client-windows-<锁SHA256>/windows-client.json`，终端列出清单 SHA-256、逐文件总数与体积；失败保留部分目录，新建父目录后重试，绝不把不完整目录发给现场。

Windows 部分以目录归档并入 OFFLINE-ONECLICK 的 devkit：建议放 `cache/windows/auriga-client-windows-<锁SHA256>/`，让原 devkit 总摘要及签名覆盖整个目录，包括 `windows-client.json`。发行说明另列该清单的独立批准 SHA-256；现场 `--devkit-sha256` 使用批准值，不能从未核验介质临时计算后当批准。锁相同可以复用原目录和归档字节。Windows 部分不包含现场密钥，不复用 Linux npm/可选二进制缓存。

输入覆盖：

- 构建 Node **22.19.0**（CI 同版，自带 npm **10.9.3**）、官方 LICENSE；运行 Node 版本及 zip SHA-256来自 `clients/installer/upstream.json`。
- 构建 pnpm 来自根 `packageManager`，发行内置 pnpm 来自 `upstream.json`，两者分别保存包与 npm lock。pnpm store 包含完整 `web/pnpm-lock.yaml` 工作区输入及 Windows esbuild/rollup 等可选二进制。
- 上游 `@deepseek-ai/dsh`、Windows runtime（包括 LibreOffice）及生产依赖来自已提交 installer npm lock，校验 DSH 根 integrity；不运行其安装脚本。插件生产依赖来自 `clients/dsh-plugin/package-lock.json`，不包含可选宿主 peer 的独立副本。现场插件代码和 UI 重新打包；broker/helper 重新构建，不拿旧成品替代源码改动。
- 仓库的源码、契约、图标、许可证和 Windows 包装脚本从同一离线 checkout 提供；预装 Git 用于记录精确提交及工作区摘要。
- Windows 自带 PowerShell **5.1**、System32 `tar.exe`（ZIP）、.NET Framework **4.x** 的 `Framework64/v4.0.30319/csc.exe` 及引用程序集用于认证 helper、托盘和自解压向导；无需额外 .NET SDK、NuGet、MSBuild、7-Zip、WiX 或付费打包工具。清单记录系统工具/程序集摘要，现场必须匹配；Windows 补丁导致差异时，应在相同受信 Windows 基线重新准备套件，不能略过工具校验。OS/CLR 由受信 Windows 安装介质提供，不把系统目录复制到套件冒充可移植 .NET。

锁键包括根/所有一级 workspace manifests、pnpm workspace/config/lock、插件 npm lock、DSH npm lock 和 `upstream.json`，另含构建 Node 版本及目标平台。改业务代码可复用；改锁或依赖声明必须重出套件。逐文件清单另覆盖所有缓存/闭包/工具原字节；缺失、额外文件、junction、摘要不符均拒绝。

## 离线构建与网络边界

Windows PowerShell 5.1 使用仓库根 `auriga.cmd`。设置 `AURIGA_CLIENT_DEVKIT` 后可使用套件 Node，现场无需另装 Node 或 pnpm。初次运行该 Node 前必须先按总套件签名核验介质，避免执行未经批准的工具。

`build --offline` 先校验完整输入，随后用清洁代理环境启动套件 Node；从 devkit 复制 pnpm store 至独立输出目录，用 `--offline --frozen-lockfile --ignore-scripts` 恢复独立工作区依赖。broker、UI、插件打包继承同一网络拒绝 preload；它拦截 Node TCP、TLS、HTTP、fetch、DNS、UDP，包括子 Node 进程。原生子进程为本地 csc、tar、esbuild，不含下载器或 restore。DSH/插件生产闭包及内置 pnpm 直接复制已核验输入，不做 `npm view` 或解析网络注册表。

成功输出 `offline-evidence.json` 和空的 `network-denials.log`；任何被拦截请求令本次构建失败。此守卫用于可信源码的依赖闭包验证，**不是对恶意源码、原生工具或同用户篡改的 OS 沙箱**。严格隔离验收仍建议在断网 Windows VM/网卡断开的专用构建机执行，不能把代理设置或守卫当系统防火墙。CLI 不修改本机防火墙、系统代理或用户已有服务。输出目录必须全新，失败保留诊断；同一源码目录不并发构建。

源码记录含实际 Git HEAD、已跟踪/未忽略文件内容摘要、删除和 dirty 标记。现场记录 `event=site_offline`，不会伪造 main/tag/工作流运行号。`client sign` 显式接受现场记录；旧 CI 签名入口未带 `--site-offline` 仍拒绝现场记录。来源记录帮助审阅追溯，不代替对源码和输入的独立批准。

Windows 构建输入、公钥根、输出父目录及离线签名目录须位于本机固定磁盘。UNC、设备路径和映射网络盘在文件读取前拒绝；路径从卷根逐级检查 junction，避免先经过间接父目录再检查。请先将离线介质中的输入复制到本地盘。

## 一键安装接口

先在可信 Windows 机生成桌面发行密钥；仅把 `desktop-public.pem` 送往平台，私钥留在离线签名机。Linux 一键安装的最小新增参数：

```sh
sudo ./auriga install --package /media/auriga-offline.tar --address 192.0.2.10 \
  --verify-key /secure/platform-release-public.pem \
  --desktop-public-key /media/desktop-public.pem \
  --desktop-publisher-tenant <实际发行租户UUID>
```

首次未启用桌面分发时，两项可以都省略；平台首管创建后从正常管理接口确认实际租户 UUID，再用同样 install 参数启用（已有安装继续启动）。不是平台离线包签名公钥，也不是资源 release/revocation/downgrade 根。`install.go` 在现有安装锁内、启动/预检之前调用 `provisionDesktop`：校验独立 Ed25519 SPKI PEM，写 `<data>/config/desktop/release-public.pem`，更新 `<data>/stack.env` 的 `AURIGA_DESKTOP_PUBLISHER_TENANT_ID`。已有不同公钥或租户拒绝覆盖。现有 NAS renderer 自动配置：

```properties
AURIGA_RESOURCE_DESKTOP_DIRECTORY=/var/lib/auriga/desktop
AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE=/etc/auriga/desktop/release-public.pem
AURIGA_RESOURCE_DESKTOP_PUBLISHER_TENANT_ID=<实际发行租户UUID>
```

现有 Compose 把 `<data>/desktop` 挂到桌面发行目录，把 `<data>/config/desktop` 只读挂到公钥目录；renderer 设置发行卷 UID/GID **10001:10001**、0700。备份发行卷与 resource 数据库及公钥。平台不持有桌面私钥。OFFLINE-ONECLICK 先合并；本任务只在其 `install.go` 增参数和两处调用，`auriga` 增 client 分派，`MAINTAIN.md` 追加客户端节。

## 发布接口与自检

`client publish` 使用正常 edge 管理入口 `POST /api/resource/v1/admin/desktop/releases`，exe 为 `application/octet-stream`，`X-Desktop-Manifest` 为已签清单 JSON 的 base64url；使用正常管理员 session cookie、Origin 和 `X-Auriga-CSRF`。需发行租户中的 `resource.versions.publish` 权限和近期 step-up（先在管理台完成身份验证）。会话只从 `AURIGA_CLIENT_ADMIN_AUTH` 环境变量读 `{ "cookie": "…", "csrf": "…" }`，不接受命令行凭据，不记入产物/日志；使用后清除环境变量。没有有效近期会话就走管理台手动路径，不生成或伪造令牌。

发布前用独立现场公钥验签及校验 exe 摘要、渠道/最低版本/序号策略，读取管理账本，序号必须超过全平台历史（包括其他渠道和下架版本）。发布后从**门户 origin**读取同渠道 `GET /api/resource/v1/desktop/latest?channel=stable|beta`，重验签并比对完整清单，再流式下载 `/api/resource/v1/desktop/packages/<sequence>` 核长度/SHA-256。任何重定向均拒绝，不跨源发送凭据；HTTPS 是默认，HTTP 需分别显式确认受支持的私有/回环 IP origin。

提交成功后自检失败不能视为未发布，也不会自动撤回或重试写入；先在正常管理台核账本，处理根因。序号、渠道、灰度比例、最低版本均在离线清单内签名，发布时不能临时改参数。手动上传见根 `MAINTAIN.md`。
