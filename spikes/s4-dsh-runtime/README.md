# S4：DSH 运行时离线制品原型

本目录是技术验证，不进入生产主分支。工具仅用 Node 22 标准库、构建机已有的 npm 和系统 `tar`；不安装系统软件、不改 DSH 源码、不启动项目服务。制品执行时不需要 npm、pnpm 或联网安装。

## 使用

从 worktree 根目录运行。固定构建机 Node **22.19.0 / npm 10.9.3**，制品 Node 也固定 **22.19.0**。默认锁已纳入 `locks/`，覆盖三种目标的可选依赖；切勿只固定 DSH 顶层版本而每次重新解析传递依赖。

```powershell
node spikes/s4-dsh-runtime/build.mjs --dsh 0.2.0-rc.2 --node 22.19.0 --target win-x64 --lock spikes/s4-dsh-runtime/locks/dsh-0.2.0-rc.2.npm-10.9.3.package-lock.json
node spikes/s4-dsh-runtime/smoke.mjs --build-result spikes/s4-dsh-runtime/out/build-win-x64.json --result spikes/s4-dsh-runtime/out/smoke-windows-online.json
node --test spikes/s4-dsh-runtime/prototype.test.mjs
```

`--target` 支持 `win-x64`、`linux-x64`、`linux-arm64`。Windows 可交叉组装 Linux 包：把目标改为 `linux-x64` 即可；是否能在目标机器启动必须看 Linux CI，不能从安装成功推出。`linux-arm64` 参数已实现，尚未实测。

首次调查新 DSH 版本可省略 `--lock`，工具先 `npm install --package-lock-only`，保存锁，再 `npm ci`。审核并保存输出的锁后，所有后续构建必须传 `--lock`。`--node 22.x` 会从官方索引解析实际版本并记录，但可复现发布只能使用完整版本号。`--out` 必须在本 spike 目录内；`--npm-cli` 可指定已有 npm CLI 路径，无需全局安装。

输出是 `out/<artifact-id>.tar.gz`、`.tar.gz.sha256`、`.package-lock.json` 和 `build-<target>.json`。`.work/` 保存下载、npm 缓存、安装临时目录和测试解压目录；`out/`、`.work/` 都被忽略，不提交数百 MB 二进制。工具不自动递归清理目录。磁盘应为多次构建、解压预留数 GB 空间。

## 制品布局与启动

```text
dsh-0.2.0-rc.2+node22.19.0-win-x64/
  node/node.exe             # Linux 为 node/bin/node
  node/LICENSE
  app/package.json
  app/package-lock.json
  app/node_modules/         # 原版 DSH、前端、生产依赖、平台可选依赖与原生文件
  bin/dsh.cmd               # Linux 为 bin/dsh
  manifest.json
```

设置 `DSH_HOME` 指向实例独占的可写目录、工作目录指向实例工作空间，然后执行：

```powershell
$env:DSH_HOME = '<instance-directory>/dsh-home'
$env:DSH_TELEMETRY_DISABLED = '1'
& '<runtime-directory>/bin/dsh.cmd' web --host 127.0.0.1 --port 0 --no-open
```

Linux 同样设置环境变量，执行 `bin/dsh web --host 127.0.0.1 --port 0 --no-open`。脚本始终引用制品内的 Node 和 CLI，转发参数和退出码。也可直接执行 `<runtime>/node/{node.exe|bin/node} <runtime>/app/node_modules/@deepseek-ai/dsh/lib/bin.js web ...`，smoke 使用此形式以准确管理进程。

DSH 输出的本地启动 URL 含临时登录令牌。探针在内存中请求该 URL，取得 **303 与 cookie**，再带 cookie 请求 `/`，检查 **200 HTML** 和入口 JS/CSS 的 **200 非空响应**；不能将未经认证的 `/` 返回 401 判为启动失败。日志保存前去除 URL 令牌，不保存签名密钥或认证 cookie。测试没有执行浏览器 JS，所以不等价于完整浏览器交互验收。

## 只读、离线和原生依赖

smoke 只改变它新解压出的文件属性，`DSH_HOME`、HOME、缓存、临时目录均在另一个实例目录。启动前、退出后都按 manifest 检查文件全集及摘要，结束时恢复属性。Windows 使用文件 ReadOnly 属性，验证已有文件无法以写方式打开；**该属性不能禁止目录内新增文件，生产必须用账号与 ACL 保证目录只读**。Linux 使用非 root 身份和 0555/0444，并实际检查文件写入与目录新建均被拒绝。

DSH 不是纯 JavaScript。发布依赖包括 `node-pty`、`koffi`、`sharp`、`node-addon-require-builtin`、Linux 的 `@deepseek-ai/node-addon-system`（flock、Landlock helper），以及 ripgrep、LibreOffice、语音库等平台二进制。`native-probe.mjs` 使用制品 Node，实际载入 koffi、生成 1px PNG、用 PTY 执行一次无网络命令并退出；Linux 另外执行 native flock。包里保留上游 `node-pty` 自带的其他平台预编译文件，没有修改 DSH 或裁剪其生产依赖。

构建统一使用 `npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false --os=... --cpu=...`，Linux 加 `--libc=glibc`。跳过脚本才能避免在构建机执行目标平台程序；这个版本所需的预编译文件已随 npm 包发布。跳过的 5 个 install-script 包及版本记录在 manifest/build JSON；其中 DSH 的脚本仅修复 `spawn-helper` 执行位，归档器对 `bin/`、`prebuilds/` 等目录规范化执行权限。本原型只保证 DSH 启动入口，不生成 npm 依赖命令的 `.bin` 链接；未来插件若依赖这些命令，需另行验证。

没有引入 zstd 依赖，输出 **tar.gz**。归档排序固定，uid/gid/mtime 固定为 0，权限规范化，gzip 不带构建时间；中文与长路径用 PAX。manifest 列出目录、普通文件的 sha256 和链接目标的 sha256，不包含自身以避免递归；整个归档摘要覆盖 manifest。

`verify.mjs` 的接口示例：

```text
node spikes/s4-dsh-runtime/verify.mjs --archive <archive-path> --sha256 <expected-sha256>
```

先核对输入摘要，再现生成 Ed25519 密钥，在内存签署 32 字节 SHA-256，并验证正确签名、摘要篡改、错误公钥三种情况。**这只是签验流程演示，不是对发行方身份的持久证明**：没有导出公钥或私钥，也没有把测试公钥作为信任根。生产应注入平台 signer、由节点从受信 `kid` 取公钥，并按现有 `SignedArtifact` 签署完整声明。

## 本机实测与证据

实测日期 2026-10-19；Node 22.19.0、npm 10.9.3、Windows x64。精确结果见 `results/`；构建包本身仍留在忽略的 `out/`。

- Windows 原生构建成功，文件共 26,678 个（含 manifest），解压文件合计 **577,321,378 字节**（约 550.58 MiB），gzip **181,905,029 字节**（约 173.48 MiB）。首次构建 370,806 ms，按锁重建 129,551 ms；不是启动耗时。
- 同锁、同 Node、同 npm 重建，两次归档 SHA-256 都是 `49954f7b896d47633d9322daabaae85f206390e6d404ae1610f3c1a2c0724d1e`。
- 在 Windows 使用同锁交叉组装 Linux x64 成功，文件共 26,020 个，解压文件合计 **605,452,972 字节**，gzip **179,872,403 字节**；不能据此声称 Linux 可启动或可离线。
- Windows 在线只读启动 **6,988 ms**，认证交换 303、首页 200、4 个 JS/CSS 均 200，koffi/sharp/PTY 成功，进程已退出、安装树未变、属性已恢复；详见 `results/windows-smoke.json`。初次探针因遗漏 DSH 认证交换未取得 200，已修正后重测，不把失败结果当成验证成功。
- **没有在本机断网、设置防火墙，或运行 Linux 制品。离线和 Linux 运行验证必须等待 GitHub Actions。**

## 主控如何跑 CI

1. 在单独 spike 分支保留本目录，将 `ci.yml` 复制为 `.github/workflows/s4-dsh-runtime.yml`，再由主控推送。执行工程师没有修改 workflows、提交或推送。
2. 用 Actions 的 **S4 DSH runtime spike → Run workflow**，选择该分支。默认 DSH `0.2.0-rc.2`、Node `22.19.0`；改 DSH 版本前必须先提供相应命名的审核锁文件。
3. 四项任务：Linux 本机打包后 `sudo unshare -n`、开启该新命名空间的 loopback、降为 runner 用户后启动；Windows 本机打包后仅给本次解压的 `node.exe` 加出站阻断规则，finally 移除；Windows 交叉构建 Linux 并按锁重建比摘要；Linux 下载 Windows 产物，在同样离线命名空间启动并测 native flock / PTY。这里以 `-n` 代替任务示例 `-rn`：`-r` 还创建只映射 root 的 user namespace，与随后降到 runner uid 冲突；无网边界仍由独立 network namespace 保证。
4. 查看工件 `s4-linux-native`、`s4-windows-native`、`s4-windows-cross-linux-x64`、`s4-linux-consume-cross`。各有 build/smoke/reproducibility JSON、锁与包；跨包消费任务只上传 JSON。所有相关 JSON 的 `status` 都必须是 `passed`。
5. 特别检查 smoke 的 `httpStatus: 200`、`authenticationExchangeStatus: 303`、非空 `assets`、`startupMs`、`nativeProbe`、`installTreeUnchanged: true`、`childStopped: true`、`attributesRestored: true`；Linux 的 `readOnly.fullDirectoryEnforcement: true` 与 `networkEvidence.noRoutes: true`；Windows 的 `programSpecificBlockRuleVerified` 和 `firewallRemoved`；交叉构建的 `builder.platform: win32` 与 `target: linux-x64`；重建的 `sameArchiveBytes: true`。

离线选项有 GitHub-hosted CI 环境检查，本机只允许 `--network online`。Windows 防火墙仅阻断所指 Node 程序的出站，不能代表其使用其他程序的后代都被阻断；Linux 命名空间的无网边界覆盖所有后代。隔离测试证明不依赖网络启动，**不证明进程没有尝试联网**。本轮没有抓包/系统调用审计，也未验证模型调用、登录官方账号、插件安装、语音模型首次下载和长时间运行。

会话遥测可通过非空 `DSH_TELEMETRY_DISABLED=1` 禁用（`0` 也被视为禁用）；`DSH_TELEMETRY_MODE=DISABLED` 是另一层配置。只读源码审查没有发现 CLI 启动时自更新安装路径；插件管理的 registry/GitHub 访问由安装/检查操作触发。Web 默认不启用 Desktop 专属产品埋点。以上是源码审查与指定 profile 的结论，不能外推为所有插件完全不联网；生产仍应执行出网限制。详细取舍及契约建议见 [ADR 0019](../../docs/adr/0019-dsh-runtime-artifact.md)。

参考：[npm 的平台与脚本选项](https://docs.npmjs.com/cli/v10/using-npm/config/)、[npm ci](https://docs.npmjs.com/cli/v10/commands/npm-ci/)、[Node 官方发行校验和](https://nodejs.org/dist/v22.19.0/SHASUMS256.txt)。源码核查基线为 DeepSeek Harness `639ed015397290b3745d163aafe02ffee4aa3f84`，同时核对安装的 `0.2.0-rc.2` 发布包。
