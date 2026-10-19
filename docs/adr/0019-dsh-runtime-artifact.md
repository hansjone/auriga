# 0019 DSH 运行时使用固定依赖的离线制品

- 状态：已采纳
- 日期：2026-10-19
- 验证范围：DSH 0.2.0-rc.2、Node 22.19.0、npm 10.9.3
- 证据：原型在 `spike/s4-dsh-runtime` 分支（`spikes/s4-dsh-runtime/`），CI 运行 39200065713 的工件

## 背景

节点代理设计第六节要求平台流水线提供 Node、原版 DSH 及生产依赖的签名压缩包，节点只下载、验签、解压，不执行 npm install。需要验证 Windows/Linux 原生驱动能共享只读运行时，并明确跨平台、离线和版本固定的实际边界。本 ADR 为草稿；原型不进入生产主分支，契约未作修改。

## 提议

### 制品格式与布局

每个目标产出 `dsh-<dsh-version>+node<exact-node-version>-<target>.tar.gz`，根目录同名。目标名使用 Node/npm 的 `win-x64`、`linux-x64`、`linux-arm64`；契约映射为 `windows/amd64`、`linux/amd64`、`linux/arm64`。Linux 此轮只讨论 glibc，不声称支持 Alpine/musl，系统共享库仍由操作系统提供。

根目录包含 `node/node.exe` 或 `node/bin/node`、Node LICENSE、`app/package.json`、`app/package-lock.json`、完整的生产 `app/node_modules/`、`bin/dsh.cmd` 或 `bin/dsh`、`manifest.json`。npm 和 pnpm 不作为运行时必需组件分发。DSH 发布包已包含前端产物，生产依赖含平台可选包，不能只复制顶层 CLI。

启动器按自身位置定位随包 Node 和 DSH 的 `lib/bin.js`，不依赖系统 PATH 中的 Node。节点为实例设置可写 `DSH_HOME`、HOME、缓存和临时目录，以及实例工作目录，再运行 `bin/dsh[.cmd] web --host 127.0.0.1 --port <分配端口> --no-open`。原型用端口 0，生产由节点分配端口。

选择 tar.gz 是因为原型无额外依赖，Node 标准库能生成 gzip；未引入 zstd 库。以后若使用 tar.zst，应显式升级 manifest/制品格式并在驱动里支持，不能只换扩展名。文件和目录按固定顺序归档，mtime/uid/gid 为零、权限规范化，中文和长路径用 PAX。manifest 记录每个普通文件的 sha256、链接目标的 sha256、目录和规范化权限；manifest 自身由归档摘要覆盖，不自引用。

### 原生依赖与构建位置

**DSH 有原生依赖。** 安装发布包实测含 `node-pty`、`koffi`、`sharp`、`node-addon-require-builtin`，Linux 还有 `@deepseek-ai/node-addon-system-linux-<arch>` 的 Node-API flock 和 Landlock launcher；另外携带 ripgrep、LibreOffice 和 sherpa-onnx 等平台程序/动态库。`native/` 不是仅开发时内容；不能把整套运行时视为可任意跨平台复制的 JavaScript。

Windows → Linux x64 的**交叉组装已成功**：下载目标 Node、用 npm `--os=linux --cpu=x64 --libc=glibc` 选择可选依赖，统一关闭安装脚本，实际安装了 Linux 的系统扩展、koffi 和图像/语音二进制；没有在 Windows 执行 Linux 程序。`node-pty` 上游包本身包含多个平台的 prebuild，原型保留这些内容。

锁中有五个带 install script 的包：`@deepseek-ai/dsh-subprocess-local`、`@google/genai`、`koffi`、`node-pty`、`protobufjs`。本版本用预编译包可完成组装；DSH 的 postinstall 只修复 `node-pty/spawn-helper` 的执行位，由归档器提供目标执行权限。Windows 实际通过 koffi 加载、sharp 生成 PNG、PTY 子进程验证。**Linux 运行结果和 Linux arm64 尚未取得，不能宣布通用跨平台支持。**

生产默认先按目标平台构建；只有 Windows 交叉产物在 Linux 离线 CI 中通过 DSH 启动、原生 flock、koffi、sharp、PTY 后，才允许该确切版本/目标交叉发布。未来缺少 prebuild、变更 ABI、安装脚本下载/编译二进制或新增 libc 限制时，应拒绝该路径并回退到目标平台构建，不能因为 npm exit 0 就发布。

### 只读运行与实例可写状态

Windows 本机从归档重新解压后，将所有运行时文件设为 ReadOnly；已有文件写打开实际被拒绝，临时 `DSH_HOME` 在制品外。DSH 在 **6,988 ms** 后完成认证交换并返回 200 HTML，4 个入口 JS/CSS 均返回 200；停止后全量文件集合与摘要仍匹配，未观察到安装目录变更。koffi、sharp、PTY 在此只读状态下也成功。

这个结果只证明所测启动路径无需修改运行时已有文件：Windows ReadOnly 属性**不禁止在目录内新增文件**，不能作为共享目录的安全边界。生产必须按 ADR 0010 使用专用实例账号与 ACL，禁止对共享运行时新增、改写、删除；Linux 使用非 root 身份和只读挂载/权限，不能以 root 下的 chmod 当作有效只读验证。CI 会检查真实文件写入和目录新建拒绝；Windows 此轮仍只测文件属性，完整 ACL 验证待节点驱动实现。

DSH 在实例的 `DSH_HOME` 下初始化 profile、storages 和凭据配置，这是正常可写状态。安装目录不要作为 cwd；`DSH_HOME`、HOME、TMP/TEMP/TMPDIR、XDG 缓存路径应显式分配。未验证所有插件操作、HMR、升级、长时间使用或模型工作中都不写安装目录。

### 离线与启动联网

**本机没有断网或改防火墙，离线结论等待 CI。** 原型工作流提供 Linux `sudo unshare -n` 的无网络命名空间（仅打开 loopback，降回 runner 用户），以及仅在 GitHub-hosted Windows VM 中针对本次解压 `node.exe` 的出站阻断。相对任务示例 `-rn`，这里不启用只映射 root 的 user namespace，避免后续降到 runner uid 失败，同时保证 chmod 只读验证以非 root 执行。Windows 交叉组装的包也由 Linux 离线任务实际运行。Windows 防火墙路径只覆盖所指可执行文件，不保证其他可执行文件的后代无法联网。

探针先对 DSH 输出的登录 URL 完成 303/cookie 交换，再请求 200 HTML 与入口 JS/CSS；直接请求首页得到 401 是 DSH 的认证要求。日志不持久化启动令牌或 cookie，测试没有执行完整浏览器前端，因而不能宣称浏览器所有插件都可离线交互。

源码与同版本安装包核查显示：`DSH_TELEMETRY_DISABLED=1` 通过启动 patch 禁用 `session-telemetry-otel`，`DSH_TELEMETRY_MODE=DISABLED` 也可禁用会话 OTel 投递；默认会话遥测是反馈触发的 `FEEDBACK_ONLY`。Web profile 默认禁用 Desktop 专属产品埋点。CLI 启动与 profile 初始化路径未发现自更新安装；插件管理的 registry/GitHub 访问由显式安装/检查触发。此轮不声称完整网络行为已审计：没有抓包/系统调用跟踪，离线启动成功也只能证明联网不是启动前提，不能证明没有失败的联网尝试。模型 API、官方账号登录、插件安装、语音模型首次下载等仍可能需要网络，不能从本验证推导离线模型可用。

依据为只读源码基线 `639ed015397290b3745d163aafe02ffee4aa3f84` 的 `apps/cli/src/profile-boot.ts`、`packages/boot/app-boot/src/profile-context.ts`、`packages/bundle/base/cordis.patch.yml`、`packages/bundle/web-app/cordis.patch.yml`、`packages/boot/plugin-manager/src/`、`native/system/packages/entry/`，并交叉核对 npm 发布的 0.2.0-rc.2。部署时仍通过节点驱动限制出网。

### 大小、时间与版本固定

本机 Windows：26,678 个文件（含 manifest），文件内容总计 **577,321,378 B**，gzip **181,905,029 B**；启动到认证首页 200 为 **6,988 ms**（单次冷启动样本，含 profile 初始化，不含下载/解压/原生探针）。主要大文件是约 179 MB 的 LibreOffice 可执行文件和约 85 MB 的 Node。未做依赖裁剪。

Windows 交叉组装的 Linux x64：26,020 个文件，文件内容总计 **605,452,972 B**，gzip **179,872,403 B**，构建 216,109 ms；Linux 启动耗时待 CI。文件内容大小不是磁盘分配大小，不含 npm 缓存、构建临时目录。

顶层 DSH 版本不足以固定内容，多个传递依赖使用范围。保留审查后的 npm v3 lock，内含 resolved 与 integrity；每次使用相同 npm、相同安装策略执行 `npm ci`，不运行安装脚本。固定完整 Node 版本，从 nodejs.org 下载并核对该版本官方 `SHASUMS256.txt`，manifest 再记录来源与 sha256。`22.x` 仅用于探索，发布必须固定实际版本。原型锁 sha256 为 `7568ed6aeb263e8605332578aba9b3b099974bf2b80704afd92cc86b7afc168b`。

Windows 同环境首次构建与按锁重建，完整归档 SHA-256 均为 `49954f7b896d47633d9322daabaae85f206390e6d404ae1610f3c1a2c0724d1e`，已证明这组输入的逐字节重建；不能泛化为不同 npm、不同 OS 或未来上游工具都相同。首次构建约 370.8 秒，缓存命中重建约 129.6 秒。CI 还会对 Windows 交叉构建 Linux 包做同样的重建摘要比较。生产应保留源包镜像和已签制品，以防注册表不可用或包撤回。

### 签名与 DshRuntime 契约建议

原型用现生成、仅在内存中的 Ed25519 密钥签署归档 SHA-256 的原始 32 字节，验证正常、篡改摘要、错误公钥，证明工具链可行；没有导出任何签名密钥，也不把测试签验结果当作发行方身份认证。

当前 `contracts/proto/auriga/node/v1/spec.proto` 已有 `ArtifactStatement` / `SignedArtifact`，且注释明确废弃只签 digest 的 `DshRuntime.signature`。生产应直接使用 `dsh.signed`、受信 `kid` 和完整声明的 JCS 签名，核对 purpose/name/version/os/arch/digest 与预期规格，维护反回滚状态。S4 不修改契约，不建议重新启用旧 signature 字段。

建议主控评估以下补充，避免在节点侧猜测：

1. 明确 `artifact_id` 的命名与 `win-x64 ↔ windows/amd64` 映射；DSH 版本和精确 Node 版本分别表达，声明的 OS/架构必须与 manifest 一致。
2. 增加或规定 runtime manifest schema、归档 media type（gzip/zstd）、Linux libc/最低系统需求的元数据位置；摘要已经覆盖 manifest，无须重复建立不一致的信任来源。
3. 同一 DSH 版本升级 Node/依赖时仍会改变制品，应定义可签署的单调 `runtime_revision` 或明确的运行时发布版本用于反回滚。仅按 DSH 版本比较，或把 Node 版本放进不参与 SemVer 排序的 build metadata，都不足以防止旧 Node 制品回滚。
4. 节点验签、校验压缩包摘要后，在隔离暂存目录安全解压（拒绝路径穿越/外链），再核对 manifest，最后原子安装到内容寻址目录并配置只读权限；现有文件存在不能替代完整校验。原型只读取自己从官方源构建的归档，未实现生产级恶意归档解压器。

## 后果与验收门槛

Windows 的基本运行与重建路径已具证据；运行时可以与实例状态分离，节点不需要 npm install。代价是每个平台数百 MB 解压内容，签名、镜像、磁盘回收与目标平台测试都必须纳入流水线。

主控应先在独立 spike 分支复制并运行原型 `ci.yml`。只有 Linux 本机构建、Windows 断网、Windows 交叉构建重现、Linux 离线消费交叉包的相关 JSON 都通过，才可将本 ADR 的对应结论升级为“已验证”；Linux arm64、Windows ACL 与完整浏览器交互仍需另测。

## 采纳说明（主控，2026-10-19）

GitHub Actions 实测全部通过：Linux 本机打包、Windows 交叉打 Linux 包、两者在 Linux 无网络命名空间（只有 lo、无路由）里以非 root 从只读目录启动成功，koffi / sharp / flock / PTY 原生探针通过；Windows 包在虚拟机里阻断出站后启动成功；同一锁文件两次构建的 Linux 包逐字节一致。生产默认在目标平台构建，交叉构建只在该版本通过上述 CI 后才允许发布。
