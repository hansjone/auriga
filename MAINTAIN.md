# 现场维护

先从管理台处理成员、邀请、角色、配额、身份源、模型供应商、凭据、入口和运维策略。需要调整 Agent 行为时，先使用资源市场的插件、技能和受控配置。业务规则或界面能力超出这些入口时，再修改源码。

## 准备离线开发机

普通使用者从 **Forgejo Release** 下载源码标签和同版本 `auriga-devkit-<版本>.tar`，不需要自己准备依赖或打包；GitHub 不放这些发行附件。开发机预装 Docker Engine、Compose 2.29+、Git；不要求本地 Go、Maven、Node，工具链均在 devkit 镜像中。

联网电脑按 `DEPLOY.md` 下载发行附件，核验引导程序的独立批准 SHA-256、公钥及签名清单。下载标签源码时保留 `.git`，例如：

```sh
git clone --branch 0.2.0 https://forgejo.example.com/owner/auriga.git auriga-source
git -C auriga-source rev-parse HEAD 'refs/tags/0.2.0^{commit}'
```

将源码目录（含 `.git`）和附件送进内网。Forgejo 网页自动生成的源码 zip/tar 没有 Git 历史，不能直接用作可追溯构建目录；也可使用套件内的 `source/`，但复用套件时它属于清单记载的原始版本，须先导入目标标签历史。

用已核验的发行引导程序解包。若有分卷，把输入改为 `.tar.part001` 或 `.tar.parts.json`，同目录保存全部分卷和签名边车，无需手工拼接：

```sh
cd /media/auriga-release
./auriga unpack auriga-devkit-0.2.0.tar --out /opt/auriga-devkit-0.2.0 --verify-key /secure/release-public.pem
cd /opt/auriga-source
mkdir -p bin
cp /media/auriga-release/auriga bin/auriga-cli
chmod 0755 bin/auriga-cli
./auriga devkit verify /opt/auriga-devkit-0.2.0 --verify-key /secure/release-public.pem
git switch -c site/local-fix 0.2.0
./auriga bundle --build --offline
```

这里 `/opt/auriga-source` 是拷入的标签 checkout，操作时需对相应目录有权限。`devkit verify` 校验缓存及当前源码依赖，在源码根写被 Git 忽略的 `.auriga-devkit.json`；后续 `bundle --build --offline` 自动定位套件。也可用 `--source /opt/auriga-source` 从其他目录校验。以后仅检查已绑定套件可直接运行 `./auriga devkit verify --verify-key /secure/release-public.pem`。

版本之间依赖未变时，发行流程复用 `auriga-devkit-<锁SHA256>.tar` 的相同字节，并以每版友好附件名供下载。`devkit.json` 与发行清单均记载锁摘要；发行清单还记录当前源码与套件原始提交。总键包括 Git 可见的 Go mod/sum/work、Maven pom/wrapper 配置、Node manifests/锁、Python 依赖、Dockerfile，以及批准 inputs/recipes 摘要与工具/基础/服务镜像 digest。业务源码改动不影响依赖键。新 checkout 绑定使用自身上游标签作构建基线，避免复用套件时误报旧上游。

锁不匹配时会列出新增、缺少或不同的路径，并提示下载当前上游标签的 `auriga-devkit-<版本>.tar`。现场自行改依赖须维护者重新准备批准输入；绝不静默联网。保留 `cache/`、`devkit.json` 和根摘要清单原字节，构建逐次校验。首次解包用 `unpack --verify-key` 验完整原包；旧 `devkit --verify <目录>` 仅用于尚未修改源码的完整套件。

仅维护者重出套件时，发行索引 `build` 登记依赖分支提供的 recipes、inputs（Go proxy、Maven/npm 缓存、pnpm store、Corepack、system 包）及固定 digest 镜像，按 `DEPLOY.md` 的 `prepare-release.sh` 在维护机/NAS 执行并由主控发布，不在 CI 完整冷建。

## 修改、测试与构建

在开发机源码目录修改，按对应模块开发规范运行针对性测试，建立普通提交便于审查和回退。构建也支持尚未提交的修改及未被 Git 忽略的新文件，删除会进入源码快照。被 Git 忽略的凭据、构建缓存、部署数据不进入快照。准备归档前核对 `git status`，防止把不应公开的未忽略文件当作源码。

```sh
git status --short
./auriga bundle --build --offline --version 0.2.0-site.3 --out /exports/auriga-offline-0.2.0-site.3.tar
```

省略 `--version` 时，包含现场提交或未提交修改的源码会得到基于源码摘要的现场后缀。省略 `--out` 时输出 `auriga-offline-<版本>.tar`。每次包记录上游版本、实际提交、分支、工作区状态、是否含现场改动、源码摘要和 UTC 构建时间。doctor 及管理台侧栏“关于”可查看这些信息。

`--offline` 只从 devkit 的本地缓存读取依赖和镜像，构建容器使用 `--network none`、`--pull never`，装配复用原离线配方。缺少文件时输出对应路径，Go/Maven/pnpm 缺依赖时保留原工具的具体报错。修改依赖锁或上游 Dockerfile 后，需要回联网维护机重新准备匹配的依赖输入和 devkit。

将产物与其独立批准摘要送入服务器，在安装目录执行升级，再从正常页面实测。

```sh
sudo ./auriga upgrade /media/auriga-offline-0.2.0-site.3.tar
sudo ./auriga doctor
```

升级会停核心栈和确认归属的实例，先备份数据目录、密钥、安装状态及 Compose 当前声明且已存在的 local named volumes。卷必须没有运行消费者。恢复会核对卷名称、驱动、选项、标签和归档摘要，失败时保留恢复记录供重试。旧版本备份可能缺少卷快照，恢复时会明确提示。

## 跟进上游

在联网维护机取得新版本 devkit 和批准的标签。将其拷入内网开发机，先核验新套件，然后在旧现场分支的源码目录导入新历史。

```sh
git fetch /media/new-devkit/cache/source-history.bundle 'refs/tags/*:refs/tags/*'
git switch site/local-fix
git merge <批准的新上游标签>
```

先核验新版本同版本 devkit 和引导程序；依赖相同时可复用旧套件。合并完成后更新 CLI 并重新绑定；不要依赖复用套件内旧源码的版本标签。

```sh
cp /media/new-release/auriga bin/auriga-cli
chmod 0755 bin/auriga-cli
./auriga devkit verify /opt/auriga-devkit-0.3.0 --verify-key /secure/release-public.pem
./auriga bundle --build --offline --version 0.3.0-site.1 --out /exports/auriga-offline-0.3.0-site.1.tar
```

复用套件的 `cache/source-history.bundle` 可能只含原始版本历史。目标标签不在里面时，从联网电脑目标源码 checkout 用 `git bundle create /exports/upstream.bundle --all` 导出并拷入，内网先执行 `git fetch /media/upstream.bundle 'refs/tags/*:refs/tags/*'` 再合并；勿强制覆盖已有标签。

解决冲突、测试、审查后赋予新的现场版本并走 `upgrade`。保留原备份、源码提交和上一版本包，核对管理台“关于”与 doctor 是否显示目标版本。

禁止直接在运行容器或部署服务器上修改程序文件。服务器仅负责加载批准包和保存数据，源码改动在开发机完成并形成可追溯的安装包。

## 客户端发布

客户端在 Windows x64 开发机离线构建。随平台 devkit 一起取得已签名核验的 Windows 部分 `auriga-client-windows-<锁SHA256>`、同版源码（含 Git 历史）及独立批准的 `windows-client.json` SHA-256；不要用 Linux 工具缓存代替。具体输入清单、准备工具、网络边界及一键安装公钥接口见 [客户端发行说明](scripts/client-release/README.md)。

首次在可信离线签名机生成本现场自己的 Ed25519 桌面发行密钥。输出父目录必须已存在，输出目录必须全新。Windows 在写入私钥前保护空目录及文件，DACL 仅当前用户完全控制，签名前再次核验。

```powershell
$env:AURIGA_CLIENT_DEVKIT = 'D:\devkit\cache\windows\auriga-client-windows-<锁SHA256>'
.\auriga.cmd client keygen --out C:\secure\site-desktop-keys
```

私钥 `desktop-private.pem` 留在签名机，备份到受控离线介质；仅将 `desktop-public.pem` 交给平台安装器。用 `./auriga install --desktop-public-key <公钥> --desktop-publisher-tenant <发行租户UUID>` 与原安装参数配置发行公钥和桌面卷。取得一键安装输出的 `resource-keys/resource-roots.json` 送入 Windows 构建机。资源三用途根与桌面发行根相互独立，禁止把资源私钥放进 devkit 或客户端。

修改客户端/插件/认证 broker/界面源码，完成针对性验证后执行。例中版本、序号、公钥根和入口均替换为本现场批准值，`--out` 必须新目录：

```powershell
$env:AURIGA_CLIENT_DEVKIT = 'D:\devkit\cache\windows\auriga-client-windows-<锁SHA256>'
.\auriga.cmd client build --offline --devkit $env:AURIGA_CLIENT_DEVKIT --devkit-sha256 <独立批准清单SHA256> --version 1.0.1-site.1 --sequence 101 --minimum-version 1.0.0 --channel stable --release-public-key C:\secure\site-desktop-keys\desktop-public.pem --resource-trust-json D:\site\resource-roots.json --out D:\exports\desktop-101
```

也可添加 `--platform-origin https://portal.example.com` 预置成员入口。IP/HTTP 必须同时提供 `--platform-access-mode ip --confirm-platform-origin <完全相同origin>`；支持的字面 IP 范围沿 installer 既有规则。构建不修改上游 DSH；依赖锁改变会明确拒绝旧套件，需维护者重出 Windows 输入。

构建生成 exe、诊断 zip、未签清单、`desktop-build.json`、`desktop.sha256` 和离线网络证据。`desktop-build.json` 明确标为现场构建，记录 Git 提交、源码摘要/dirty、devkit/依赖锁摘要、公钥指纹、资源根摘要。先核对源码和套件，再在离线签名机审阅完整输出，不要直接用脚本自动提取摘要后立即确认：

```powershell
.\auriga.cmd client sign --dir D:\exports\desktop-101 --review-only
.\auriga.cmd client sign --dir D:\exports\desktop-101 --key-file C:\secure\site-desktop-keys\desktop-private.pem --confirm-sha256 <人工审阅后的reviewSha256>
```

审阅包含源码身份、公钥指纹、资源根摘要、包 SHA-256、版本、序号、渠道、最低版本、有效期、灰度及回滚策略。任何制品或记录变化必须重新审阅；错误钥、宽松私钥权限和重复覆盖均拒绝。签名后的 `desktop-release.json` 与 exe 组成发行对，zip 不用于上传。默认有效期为构建后30天。

管理员打开正常管理台 `/ops/desktop`（“桌面客户端”），先完成近期身份验证，选择 `desktop-release.json` 和对应 `AurigaDSH-Setup-*.exe`，确认策略后发布。需实际发行租户及 `resource.versions.publish` 权限。发布下一个序号前先看账本；序号必须高于全平台历史，不能因下架而重用，stable 与 beta 共用序号高水位。

已持有正常近期管理员会话时，也可将 session cookie 和 CSRF 以 `{cookie,csrf}` JSON 注入当前进程环境变量 `AURIGA_CLIENT_ADMIN_AUTH`（不要写命令历史、工单或仓库），通过同一管理接口上传并自动回读核摘要：

```powershell
.\auriga.cmd client publish --dir D:\exports\desktop-101 --public-key C:\secure\site-desktop-keys\desktop-public.pem --admin-origin https://admin.example.com --portal-origin https://portal.example.com
Remove-Item Env:AURIGA_CLIENT_ADMIN_AUTH
```

渠道、最低版本、`--rollout-percentage 0..100`、`--rollback-from <精确来源版本>` 在构建时设定并由签名覆盖，发布后不能改签名字段。最低版本高于已装版本会要求更新；日常发布不应随意抬高。beta 只面向对应渠道，低于当前发行版本的回退需显式签名来源，仍受服务端策略校验。

发布后从门户“下载桌面客户端”下载并核 `desktop.sha256`，首次安装件及公钥摘要通过独立可信渠道确认。让装有旧版本且固定**同一现场发行公钥**的测试成员，在托盘“检查更新→立即更新→重启以完成更新”走完整流程，验证目标版本、配置/会话保留和资源市场验签。自动检查/下载沿成员设置，安装仍按已有确认策略，不强制无提示安装。`client publish` 的回读自检不代替真实安装/自动更新验收。

平台不保管桌面私钥。丢失或泄露时停止使用该钥并制定现场切换方案；当前客户端固定单一发行根，**更换发行钥需要成员通过独立可信渠道重新安装**，没有自动获取新公钥或双根轮换。不要直接换平台公钥后向旧客户端发新版，旧客户端会拒绝签名。资源根轮换也要统筹平台与新客户端固定根；保留客户端更新高水位和现有数据，不能清状态绕过防回退。
