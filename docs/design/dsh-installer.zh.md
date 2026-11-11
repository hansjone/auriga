# DSH 可安装包设计（内置 Auriga 插件、统一依赖环境）

状态：设计 + Windows 首版原型（任务 DSH-PKG）。路线图依据：`docs/plan/roadmap.zh.md` M4（D1 Windows 安装包、D2 Linux 安装包、P3 插件连接模式）。

目标：成员装一个包就得到“DSH + Auriga 插件（已启用、可填平台地址登录）+ 它需要的运行环境（Node 等）”；以后这个安装底座同时是工具市场“本地工具”和插件市场的本地落点。**上游 DSH 升级时，我们的包必须能跟着升级**（见第 5 节，用户硬性要求）。

## 1 调研结论（含证据）

调研只读：上游源码 `D:\project\chatgpt\DeepSeekHarness`（与本机 0.2.0-rc.2 同版本）、本机已装桌面版目录（未改动）、npm 注册表（只读查询）。

### 1.1 上游发布形态

| 形态 | 证据 | 要点 |
| --- | --- | --- |
| npm 包 `@deepseek-ai/dsh` | `npm view @deepseek-ai/dsh versions dist-tags license`：许可 MIT；`latest=0.2.2-rc.1`、`alpha=0.2.2-alpha.3`；已有 `0.2.1-rc.3/4/5` | 纯 Node 包，bin 为 `lib/bin.js`（`package.json` 的 `bin.dsh`）；依赖 `@deepseek-ai/dsh-*` 约 40 个同版本精确钉死的子包。需 Node `^22.19.0 \|\| >=24` |
| 桌面版（Electron + electron-builder NSIS） | `%LOCALAPPDATA%\Programs\DeepSeek Harness`：`DeepSeek Harness.exe`、`resources\app.asar`、`resources\runtime`；`apps\desktop\scripts\electron-builder-config.mjs`：`oneClick:false`、`perMachine:false`、`allowToChangeInstallationDirectory:false` | 每用户安装，不需管理员；卸载程序只删应用与 `%APPDATA%` 下数据，**不动 `~/.dsh`**（`apps/desktop/README.md` 卸载一节） |
| 桌面版自带运行时 | `resources\runtime\versions.json`：`node 24.21.0`、`pnpm 11.28.5`；`resources\runtime\primary-runtime`；`runtime\cli\bin\dsh(.cmd)` | 桌面版自带 Node 和 pnpm，插件管理用自带 pnpm，用户机器无需装 Node |
| 桌面版自动更新 | `resources\app-update.yml`：`provider: generic`、`url: https://download.deepseek.com/dsh-desk/feeds/win-x64/`、`channel: nightly`、`publisherName: CN=Hangzhou DeepSeek ...`（验证更新包签名） | 固定 nightly 通道，只升不降；README 另述“强制更新（mandatory update）”策略：平台可要求客户端必须更新，无法由插件/安装器关闭 |

npm 版实测体量：隔离目录 `npm install @deepseek-ai/dsh@0.2.1-rc.3`，`node_modules` 约 **834 MB**、锁文件 687 个包（78 个可选平台包）；其中 `@deepseek-ai/libreoffice-kit-win32-x64` 约 188 MB（办公文档功能）。证据：`agent-cache\dsh-pkg\probe\`（安装 `package-lock.json` 408 KB 已作为 `clients/installer/locks/` 提交）。

### 1.2 许可证与再分发

- `LICENSE`（上游仓库与 npm 元数据）均为 MIT：允许再分发、预置插件，须保留版权与许可文本。我们的包保留 `@deepseek-ai/dsh` 包内的 LICENSE，且不 fork、不改源码。
- `BRAND_GUIDELINES.md`：“DeepSeek Harness”是注册商标，**不要在项目名里直接使用全称**，推荐用缩写 **DSH** 命名；不得暗示官方背书。结论：产品名用 “Auriga DSH”，不用 “DeepSeek Harness”；安装包不带上游品牌素材（图标、桌面版安装器）。**品牌名最终由用户决定**。
- 桌面版安装程序是上游签名二进制，再分发它在 MIT 范围内可行，但我们没有得到它的离线下载渠道（见 2.A），原型不采用。

### 1.3 插件预装机制（不改上游即可首启启用）

- profile 是目录 `$DSH_HOME/profiles/<名>/`：`package.json` 里 `dsh.profile.bundles` 列出启用的包（例：`@deepseek-ai/dsh-base`、`@deepseek-ai/dsh-web-app`、`auriga-dsh`），`node_modules` 放插件，`cordis.patch.yml` 是用户补丁层（顺序：bundle 内置 patch → profile patch → `$DSH_HOME/cordis.patch.yml` → `--patch`）。证据：`apps/cli/README.md`；本机既有隔离 profile `agent-cache\auriga-client-rc3-qa\profiles\auriga-client-qa\{package.json,cordis.patch.yml,node_modules}`；桌面版 profile 同构（`~/.dsh/profiles/desktop`，只读查看）。
- 插件 `auriga-dsh` 自带 `dsh.bundle.patch=cordis.patch.yml`（`insert: id: auriga-dsh`），因此只要它在 profile 的 bundles 与 `node_modules` 里，首启即启用。DSH 只认 profile 依赖闭包，单个目录 symlink 不够（`docs/dev/m2.md` 真实 DSH 一节）；因此原型**复制**插件及其生产依赖进 profile `node_modules`（无需 pnpm、无需联网）。
- 平台地址：插件“connect”模式读 `config.connect.platformOrigin`（`clients/dsh-plugin/src/settings/host.js`），用户也可在 DSH 设置页 Auriga 项里改并保存（保存值优先）。安装器把 `- id: auriga-dsh / disabled:false / config:{connect:{platformOrigin}}` 写进 profile 的 `cordis.patch.yml`（形状同 `docs/dev/network-installation.md`），**只在文件不存在时写**，升级不覆盖用户改动。
- 上游命令：`dsh --profile <名> [--dump-config|--port|--no-open]`；`dsh plugin --profile <名> add <tgz>` 转调 pnpm（插件管理页同样需要 pnpm，见未完成项）。

### 1.4 依赖环境现状

- 运行 DSH 本体只需 Node（`engines`：22.19+/24+）；桌面版用 Electron 内置 Node 24.21 并带 pnpm 11.28.5。
- git/python 不是基础依赖：python 只在 office 技能运行时（桌面版 `primary-runtime` 离线自带）按需使用；git 用于“从 GitHub 装插件”前的检查。基础包不预装 git/python，缺失时相关功能明确失败。
- 本机开发机 Node 22.19.0 在 PATH（可作构建机），但**成员机器不能假设有 Node**，所以包内自带 Node。

### 1.5 上游自动更新与我们插件更新的关系（概述，详见第 5 节）

桌面版自带 nightly 自更新且有强制更新策略；npm 版无自更新（升级即换版本号）。插件声明 `engines.dsh >=0.2.1-rc.3`，且用能力探测失败封闭（`_auriga-common.md` 核心原则），所以“上游新、插件旧”不会拖垮 DSH，只会让 Auriga 功能显示明确诊断。

## 2 方案比较

### 方案 A：包装上游桌面版安装器 + 首启注入插件与配置

做法：安装包先静默运行上游 NSIS 安装器（`/S`，每用户），再往 `~/.dsh/profiles/desktop` 写入 `auriga-dsh` 依赖与 bundles（桌面版需先启动一次并退出才初始化 profile，再用其自带 CLI `dsh plugin --profile desktop add <tgz>`）。

| 项 | 内容 |
| --- | --- |
| 安装/升级/卸载 | 安装：上游安装器 + 我们的注入器两步；升级：上游自更新自己走，我们只在插件版本不满足时重新注入；卸载：上游卸载器（不删 `~/.dsh`，插件留在 profile），我们须另清理 |
| 更新通道 | DSH：上游 nightly feed（我们无法关闭，强制更新策略由上游平台控制）；插件：我们自己的下发。两条通道互不协调，上游强升后可能先于我们适配 |
| 签名 | 上游二进制已由 DeepSeek 代码签名；我们的注入器/外壳仍需自己的签名才不触发 SmartScreen（**需花钱**） |
| 全局环境影响 | 写 `%LOCALAPPDATA%\Programs`、注册表卸载项、共享用户 `~/.dsh`（会话、设置、凭据与用户自己的其他插件同处）；插件注入碰用户 profile，有改坏用户配置风险 |
| 本地工具落点 | 无自有位置，只能借 `~/.dsh` 或自建目录；上游更新/卸载不感知 |
| Linux | 上游桌面版只有 mac/win 产物（`package:desktop:win:x64`、`mac-*`），Linux 无桌面包，方案不可延伸 |
| 可复现/CI | 依赖上游安装器的可得性：本任务未找到公开、带校验的离线下载源（feed 为 generic 更新通道，非文档化的下载接口）；无法在 CI 固定其摘要 |

优点：有桌面窗口、托盘等完整 GUI；自带 Node 24 + pnpm + Office 运行时。缺点：升级主权在上游，版本不可控；无法保证“rc.3 起”；安装与卸载都要碰用户全局 profile。

### 方案 B：自带 Node 运行时 + npm 版 DSH 的独立发行（“包一层不 fork”）

做法：包内含 ① 官方 Node（校验 SHA-256）② 用锁文件精确安装的 `@deepseek-ai/dsh@<版本>`（完整性与注册表一致）③ 我们的 `auriga-dsh` 插件及生产依赖 ④ 我们的安装核心 `core.mjs`（纯 Node，Windows/Linux 共用）。独立 `DSH_HOME`，独立 profile `auriga`，用 `dsh --profile auriga` 启动 Web 版，浏览器访问。

| 项 | 内容 |
| --- | --- |
| 安装/升级/卸载 | 安装：解压包运行 `install.cmd`，复制到 `<根>\versions\<id>\`，种 profile，冒烟校验，写 `current.txt` 切换；升级：同一命令装新包，旧版本保留一份，profile 先备份，冒烟失败自动回滚；`rollback.cmd` 手动回退；卸载：`uninstall.ps1`，默认保留用户数据，`-PurgeData` 才删我们 profile |
| 更新通道 | 上游 DSH 与我们的部分分开存放、分开版本号（`manifest.json` 记录 `upstream.version/integrity`、`plugin.version`、包 id `<插件版本>-dsh<上游版本>`）。统一由我们下发，我们决定何时跟进上游（与“DSH 版本由我们控制”的原则一致） |
| 签名 | 免费可做：SHA-256 清单。**需花钱**：Windows 代码签名证书（OV/EV 或云签名服务，数额与是否可用需用户核实采购）；未签名包会被 SmartScreen 提示，且从网络下载的 zip 带 Mark-of-the-Web。Node 官方二进制自带官方签名，随包保留。不擅自采购 |
| 全局环境影响 | 默认不改 PATH、不写注册表、不碰 `~/.dsh`；数据放 `%LOCALAPPDATA%\AurigaDSH\home`。代价：与用户已有 `~/.dsh`（桌面版/CLI 的历史会话）不共享，需要共享时安装参数 `-DataHome` 指过去（风险自负，见未完成项） |
| 本地工具落点 | `<DataHome 同级>\tools\<工具id>\<版本>\`：独立于 `versions\`（应用升级/回滚不动它）、独立于 DSH profile（插件市场落点仍是 profile `node_modules`）。工具由 manifest + SHA-256 描述，以 DSH 子进程方式运行，只收到显式授予的环境变量，不继承平台令牌；共享 Node 运行时通过只读路径提供。 |
| Linux | 同一 `core.mjs` + `bin/node`（`nodeBin` 已区分平台），换 Node 的 linux tar.xz 与 `install.sh`/tar.gz，路径换 XDG；glibc 版本与 `real-dsh` 镜像的 Linux amd64 约束一致 |
| 可复现/CI | 全部输入可钉死：Node 校验和、npm 锁文件（已提交）、插件 tgz 校验；CI `windows-latest` 能完整重建 |

缺点：没有 Electron 桌面窗口（用浏览器打开本机 DSH Web）；体积大（约 0.8 GB 安装体量，主要在 DSH 依赖，见 1.1）；插件管理页需要 pnpm（首版未随包提供）。

### 方案 C：B 为底座，后续再叠加桌面外壳（可选的第二阶段）

等上游给出稳定、带校验的桌面版离线产物后，把 A 作为 B 之上的可选“桌面外壳”：外壳只负责窗口，DSH 与 profile 仍由 B 的核心管理，并通过 `app-update.yml` 之外的方式与上游强更新对齐。现在不做，因为上游强制更新策略无法关闭，且无可固定的下载源。

### 推荐

**推荐 B**（首版 Windows，随后同核心出 Linux）：

1. 满足“跟随上游升级”：上游是可替换组件，升级只换 `upstream.json` 版本与锁，我们的部分不动；用户端有备份、冒烟、回滚。
2. 可复现、可在 CI 构建与测试，没有对上游安装器可得性的依赖。
3. 对全局环境零侵入，隔离 `DSH_HOME`，卸载干净；符合“不碰用户已装的 DSH”。
4. Linux 延伸几乎免费（核心纯 Node）。
5. 作为工具市场与插件市场的底座：有固定的“版本化应用目录 + 用户数据目录 + 工具目录”三分结构。

代价（需用户知晓）：首版无桌面窗口；包体大；需自管签名与更新分发。

## 3 原型设计（`clients/installer/`）

```
clients/installer/
  upstream.json            上游版本参数：dsh 版本 + 完整性、Node 版本 + SHA-256
  locks/dsh-<版本>.package-lock.json   已提交的上游依赖锁（npm ci 用）
  scripts/build.mjs        构建（参数：--upstream <版本> --plugin-tgz --out --cache --no-zip）
  scripts/lib/             semver-lite、文件清单与密钥扫描
  runtime/core.mjs cli.mjs 安装核心（纯 Node）：install / rollback / uninstall / launch
  windows/                 install.ps1、install.cmd、launcher\（auriga-dsh.cmd、rollback.cmd、uninstall.ps1）
  test/                    node --test
```

包内布局：`<包>\install.cmd`、`payload\{manifest.json, upstream\{node, app}, auriga\{plugin, runtime, launcher}}`。安装后：

```
<根>\versions\<id>\ (upstream\ + auriga\)   不可变，一个版本一个目录，保留“当前 + 上一版”
<根>\current.txt  <根>\install.json         当前/上一版指针
<根>\bin\{auriga-dsh.cmd,rollback.cmd,uninstall.ps1}
<DataHome>\profiles\auriga\{package.json,cordis.patch.yml,node_modules\…}   用户数据区，永不在 <根> 内
<DataHome>\profile-backups\auriga\<上一版id>\   升级前的 profile 备份
```

安全与完整性：构建期校验 Node zip 的 SHA-256（`SHASUMS256.txt` 或 `upstream.json` 钉死值）；上游 `npm ci --ignore-scripts` 以提交的锁安装，并断言锁中 `@deepseek-ai/dsh` 的版本与完整性等于注册表值（非默认版本则记录 `pinnedInRepo:false`）；`engines.dsh` 门槛不满足则构建失败；`manifest.json` 对我们的每个文件给 SHA-256，并扫描 PEM 私钥、`sk-`、JWT、`Bearer`、`AURIGA_*TOKEN=` 与 `.env/*.pem/*.key` 文件名，命中即构建失败。

## 4 安装、升级、卸载流程（原型实现）

- 安装：`install.cmd [-InstallDir] [-DataHome] [-PlatformOrigin]` → 包内 node 运行 `cli.mjs install`：复制 `payload` 到 `versions\<id>`（先 `.partial` 再改名）→ 种 profile（bundles 含 `auriga-dsh`，补丁只在缺失时写）→ 把插件闭包复制进 profile `node_modules` → 冒烟（`dsh --profile auriga --dump-config` 必须成功且含 `auriga-dsh`）→ 写 `current.txt`/`install.json`，更新 `bin\`。
- 升级：对已有安装再运行新包的 `install.cmd`，同一流程；profile 的 `package.json` 与 `node_modules` 先备份；任何一步失败：还原备份、删除新版本目录、指针不变。数据（会话、设置、登录态、`cordis.patch.yml`）不在被覆盖范围内。
- 回滚：`rollback.cmd`：恢复备份的插件文件，切回上一版，再冒烟。
- 卸载：`uninstall.ps1`：从临时拷贝运行 node（避免锁住安装根），删除安装根；默认保留 `<DataHome>`；`-PurgeData` 删除 profile 与备份。

## 5 上游升级跟随（用户硬性要求）

### 5.1 原则：包一层，不 fork

上游 DSH 是**可替换组件**：版本号 + 完整性写在 `upstream.json` 和包内 `manifest.json`，目录 `upstream\` 与我们的 `auriga\` 分开存放。构建脚本把上游版本做成参数：`node scripts/build.mjs --upstream <版本>`，换版本号即重新出包，无需改任何源码（若该版本没有已提交锁，构建会生成锁到 `out\locks\` 供评审后提交）。我们对 DSH 只用公开 API/扩展点（`_auriga-common.md` 核心原则），升级只触及插件适配层。

### 5.2 升级通道（上游出新版 → 用户端升级）

1. 检测：定时任务读 `npm view @deepseek-ai/dsh dist-tags versions`，发现新的 `rc/stable` 版本。（首版为人工/手动触发；自动化放 CI 定时工作流，不自动发布。）
2. 兼容性检查（均可脚本化）：
   - `engines.dsh` 范围：构建时 `satisfiesMin(<新版本>, 插件 engines.dsh)`，不满足则拒绝出包；
   - 插件测试与能力探测冒烟：`clients/dsh-plugin` 单测 + `deploy/compose/real-dsh` 真实 DSH 配方（`docs/dev/m2.md`）换新版本跑一轮；
   - 安装冒烟：新包在隔离目录跑 `install → dsh --dump-config` 与升级回滚测试（本原型的核心测试）。
3. 重新打包：通过则 `build.mjs --upstream <新版本>` 产出新包，包 id 形如 `0.1.15-dsh0.2.2-rc.1`，我们的版本与上游版本各自可见。**发布到成员前由用户/主控评审**（沿用“不自动追最新版，由用户定期评估”）。
4. 用户端升级：运行新包安装器（后续由启动器检查我们的签名清单并下载，非首版）。保留数据：profile、会话、`cordis.patch.yml`、插件设置与登录状态都在 `<DataHome>`，不在版本目录；升级前自动备份 profile；失败自动回滚；成功后 `rollback.cmd` 仍可退回上一版。
5. 回收：仅保留“当前 + 上一版”两个版本目录，避免体积翻倍。

### 5.3 与上游自带自动更新的关系

| 路线 | 做法 | 取舍 |
| --- | --- | --- |
| 关掉上游自更新，由我们统一下发 | 方案 B 天然如此：npm 版 DSH 没有自更新，升级只发生在我们的包里 | 版本由我们控制，兼容性可先验；代价是我们要承担跟进节奏与分发 |
| 允许上游自更新，我方插件事后自检修复 | 仅方案 A/C 外壳可行：桌面版有强制更新策略，无法也不应被我们关闭（改 `app-update.yml` 属于改动上游签名应用） | 用户总能拿到上游新版，但可能先于我们适配，需插件能力探测失败封闭 + 修复入口 |

**推荐**：B 路线下“关掉上游自更新”本就成立，统一由我们下发，以保证版本可验证；同时插件已有能力探测与 `engines.dsh` 门槛作为兜底（用户若自行换更新的 DSH 也不会崩）。若将来叠加桌面外壳（方案 C），则采用“允许上游自更新 + 我方自检修复”，并在包的启动检查里对比 `engines.dsh`，不满足就提示“请更新 Auriga 插件”。

## 6 原型实测（隔离环境）

隔离方式：安装根、`DSH_HOME`、`USERPROFILE/HOME/APPDATA/LOCALAPPDATA` 全部指向 `agent-cache\dsh-pkg\sandbox-*`，不碰全局 DSH、`~/.dsh`（目录修改时间仍为 11-04）与桌面版安装目录。实测用 `clients/installer/scripts/e2e.mjs`，证据在 `D:\project\chatgpt\agent-cache\dsh-pkg\evidence\`。

| 步骤 | 结果 |
| --- | --- |
| 构建 rc.3 包（默认上游）和 rc.4 包（`--upstream 0.2.1-rc.4`） | 成功，包 id `0.1.15-dsh0.2.1-rc.3` / `0.1.15-dsh0.2.1-rc.4`，解开约 0.9 GB；Node SHA-256 与官方一致，dsh 完整性与注册表一致 |
| 安装 rc.3（含冒烟 `--dump-config`） | 约 49 秒；profile `auriga` 含插件与生产依赖，补丁行写入平台地址 |
| 启动（随机端口、`--no-open`） | `/auriga/` 未认证返回 401、未知路径 404（插件路由已注册）；认证后 `/auriga/settings/state` 返回 `mode:connect`，平台地址来自补丁 |
| 在插件设置里改平台地址 | `POST /auriga/settings/save` 200，状态变为新地址，持久化在 profile `auriga-settings/settings.json` |
| 升级到 rc.4 包 | 成功，`previous=rc.3`；会话文件、登录态标记文件、插件设置均保留（补丁文件不被覆盖由单测覆盖）；上游组件换成 0.2.1-rc.4；启动后插件仍加载，平台地址为用户改后的值 |
| 回滚 | `rollback.cmd` 切回 rc.3，插件文件还原，启动后同样通过 |
| 卸载 | `uninstall.ps1` 删除安装根，用户数据默认保留 |

升级失败路径（冒烟失败自动恢复备份与指针）由单测覆盖，未在真实包上制造失败。启动后的 DSH 进程由脚本用 `taskkill /T` 结束，实测后无遗留进程。

## 7 需要用户决定的事项

1. 产品名/品牌：建议 “Auriga DSH”，避免使用 “DeepSeek Harness” 全称（`BRAND_GUIDELINES.md`）。
2. 代码签名：是否采购 Windows 代码签名证书（或云签名服务）。未签名包会被 SmartScreen 警告。
3. 数据目录：默认独立 `%LOCALAPPDATA%\AurigaDSH\home`；是否要与用户现有 `~/.dsh`（桌面版/CLI 历史会话）共享。
4. 是否接受首版无桌面窗口（浏览器访问本机 DSH），还是等方案 C 的桌面外壳。
5. 分发渠道（下载位置、清单签名）——当前不发布公网。

## 8 未完成与风险

见 `.agent-report.md`：开始菜单/快捷方式/注册表卸载项、随包 pnpm（插件管理页）、Linux 版、安装器 exe 封装（IExpress/NSIS/MSI）、体积裁剪（可选平台包）、启动器自动更新、真实平台登录联调。
