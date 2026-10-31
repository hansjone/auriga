# 安装 Auriga 桌面插件

需要管理员提供 **Auriga 平台地址、成员账号和插件发行包**。无需下载 Auriga 源码或在自己的电脑部署平台。

要让 DSH 对话使用团队中转站，另按 [原生模型配置步骤](relay-models.zh.md)
取得自己的成员虚拟 Key 并配置提供方；插件登录完成后仍需确认原生会话实际选择了该模型。

## 1. 检查 DSH

使用 **DSH 0.2.1-rc.3**（最低版本和当前验收目标）、Node.js 22.12+。在终端执行 `dsh --version`，桌面版也可在“关于”查看版本。`0.2.0-rc.2` 不满足要求；更新版本先由管理员确认兼容性。请从 [DSH 官方项目](https://github.com/deepseek-ai/deepseek-harness)获取对应 Windows、macOS 或 Linux 的发行版。

桌面版优先用插件管理页；CLI 方式还需要 PATH 中的 `dsh` 和 pnpm。不要绕过版本检查。

## 2. 下载并安装

从管理员指定的 [Auriga Releases](https://github.com/hansjone/auriga/releases) 选择 `plugin-v<版本>`，下载同一版本的 `auriga-dsh-<版本>.tgz` 和 `.tgz.sha256`。首次正式发布前可能还没有插件资产，请向管理员获取打包脚本生成的包。以下 `0.1.0-rc.1` 换成实际版本。

先核对 SHA256 与 `.sha256` 文件首列完全一致：

- Windows PowerShell：`Get-FileHash .\auriga-dsh-0.1.0-rc.1.tgz -Algorithm SHA256`
- macOS：`shasum -a 256 auriga-dsh-0.1.0-rc.1.tgz`
- Linux：`sha256sum -c auriga-dsh-0.1.0-rc.1.tgz.sha256`

**桌面版**：进入 DSH 插件管理页，选择添加插件，安装来源填已下载 `.tgz` 的**绝对路径**，确认并启用。界面中的路径含空格也无需加引号。若发行版没有文件来源输入，用其配套 CLI 安装到同一个 profile。

**CLI**：以下以浏览器版 `web` profile 为例；桌面版须用其自带 CLI 和 `desktop` profile（npm 安装的 `dsh` 不能管理桌面版保留 profile），其他 profile 换成自己的名称。

```powershell
# Windows PowerShell；路径含空格时保留引号
 dsh plugin --profile web add "C:\Downloads\auriga-dsh-0.1.0-rc.1.tgz"
```

```sh
# macOS / Linux
 dsh plugin --profile web add "$HOME/Downloads/auriga-dsh-0.1.0-rc.1.tgz"
```

DSH 将安装命令交给 pnpm，也支持 HTTPS 压缩包 URL：

```sh
dsh plugin --profile web add "https://github.com/hansjone/auriga/releases/download/plugin-v0.1.0-rc.1/auriga-dsh-0.1.0-rc.1.tgz"
```

URL 必须对应实际已发布版本；需要独立核验摘要时，优先下载校验后本地安装。安装运行依赖需要访问包仓库；tgz 不是完整离线依赖镜像。此包已含界面，无需构建权限或 Vite。

安装完成按 DSH 提示重新加载；仍无入口时保存工作并重新打开自己的 DSH。CLI 浏览器版运行 `dsh --profile web`，打开它打印的本机地址。应看到侧栏 **Auriga** 和设置中的 **Auriga** 项。

## 3. 连接团队并登录

1. 打开 **DSH 设置 → Auriga → 总部连接**，“平台地址”填管理员提供的根地址，例如 `https://auriga.example.com` 或 `https://192.0.2.10:8443`。不要加 `/admin`、`/api`、账号密码或访问令牌。
2. 点击“测试连接”，成功后点击“保存”。HTTP 仅允许回环和明确内网 IP；域名和公网 IP 请用 HTTPS，证书须受本机信任。
3. 点击“登录”，在普通登录页输入团队账号及平台要求的验证信息。登录后返回当前 DSH，无需另开“总部登录”流程。
4. 在设置查看当前身份。“平台接口可达”仅表示地址可访问；Team 通信、模型授权各自以界面状态为准。Team 提示未配置时请管理员按[网络安装说明](../dev/network-installation.md)配置团队公开信任材料。

退出选择 **DSH 设置 → Auriga → 账号与安全 → 退出本机登录**，之后应恢复登录页。退出不停止 DSH 进程或已有任务；切换团队地址后需重新登录。

## 常见问题

- **版本不兼容 / native connection admission required**：升级至 `0.2.1-rc.3` 或管理员验证过的更新 DSH；旧版缺少原生准入 API，不能强行启动。
- **已安装但无入口**：检查是否装到当前 profile、是否启用，再重新加载自己的 DSH。不要用 GitHub 源码目录代替 tgz。
- **地址无效 / HTTPS required**：只填协议、域名或 IP、端口。HTTP 仅支持回环和明确内网 IP；不要关闭 TLS 校验。
- **连接失败 / 登录不可用**：检查平台地址可达；请管理员核实证书、`/oauth2/jwks` 和团队账号身份源。自动发现要求恰好一个 `team-account` 表单身份源，多身份源需管理员预配置。
- **401 / 需要重新登录**：重新登录并确认团队地址；测试连接不代表账号已授权。
- **登录短暂等待**：原生登录会先验签；若新令牌的生效时间仅略微超前，最多等待 30 秒真实时间，再按原有时间、身份和用途规则完整校验。默认令牌校验仍只允许 5 秒偏差；超出等待上限、签名无效或等待后仍无效均拒绝。持续失败请管理员检查两端时钟。
- **安装下载失败**：检查 pnpm、代理和 npm 网络；无需允许本插件执行安装构建脚本。

依据：[DSH 官方打包教程](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/user/develop/basic/publish.md)、[CLI 插件管理](https://github.com/deepseek-ai/deepseek-harness/blob/master/apps/cli/reference/README.md)。维护者验收边界见[发行说明](../dev/plugin-distribution.md)。
