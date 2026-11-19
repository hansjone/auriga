# 共享 DSH 的完整 rc.3 配方

`shared-runtime-profile.example.yml` 是完整 Cordis entry-list。它只使用锁定开发套件内的 `@deepseek-ai/dsh-*` 公开根导出和三个 Auriga 入口，不加载 `dsh-base`、`dsh-web-app` 或旧 Auriga owner 插件。安装方应把它作为池的受信 profile 输入，交由原 Agent renderer 生成每池不可变配置。

## 安装与解析锚

- DSH SDK 固定安装在只读 `/opt/dsh`，包版本为 `0.2.1-rc.3`。
- Auriga 固定安装在只读 `/opt/auriga-dsh`；`/opt/dsh/node_modules/auriga-dsh` 指向该受信安装。安装校验必须确认 manifest 的 `exports["./shared-browser"]` 精确为 `./src/shared-browser/plugin.js`，并确认文件来自同一已验证制品。
- `boot` 的第五参数 `bareModuleBaseUrl` 应为 `file:///opt/dsh/package.json`。配置文件位于 `/run/auriga`；其路径本身不提供 SDK 依赖解析。
- 真实 `ClientModuleRegistry` 还独立使用 entry 所属树的 `baseUrl` 解析客户端包；第五参数只改变 Include 的导入。因此 launcher 必须先用 `src/shared/profile-resolution.js` 的 `resolveSharedRuntimeProfile(rows)` 规范化已经过 Node 封闭允许表检查的逻辑行：只将批准的 SDK 包名转换成同一只读 SDK 内、公开根 export 对应的实际文件 URL，再把所得 entry-list 交给公开 `boot`。不能直接启动未规范化的逻辑输入，也不新增 `/run/node_modules`、挂载或环境搜索路径。

规范化器的 SDK 锚固定为 `/opt/dsh/package.json`。它检查解析结果真实路径在该安装根内、最近的 manifest 包名与逻辑包名一致、文件正是 manifest 的公开根 export。锁定 rc.3 的根 export 均只有 `types/default` 条件；新增不一致的 import/require 条件、别名、包外 symlink、未知包及任意文件 URL均拒绝，防止加载另一份 CJS 类。安装侧仍须保证此 SDK 根只读且来自已验证制品。

Auriga browser 行使用唯一精确的 `file:///opt/auriga-dsh/src/shared-browser/plugin.js`。rc.3 的客户端登记器不会把裸子路径 `auriga-dsh/shared-browser` 当包根；固定文件 URL 经同一公开 Loader 解析并寻找最近 manifest，才能同时登记 Auriga 的 `./client`。这不授权任意 `file:` 插件。Agent 应只接受这个固定只读 URL，并沿原 browser 行的顺序、唯一性和配置检查。`id: auriga-dsh` 对应 Host 的 `name: auriga-dsh/shared`，不能把行 ID 当成旧 owner 根入口。

`shared-ports` 内部使用的 Peer 和模型适配器也必须来自 `/opt/dsh/package.json`，不能依赖 Auriga 开发依赖中的旧 SDK。它们的固定锚解析修复与本配方分别交付；未接通前不应宣称真实完整 Host 已启动。

## 服务闭包与权限

配方显式包含会话投影、JSONL 持久化与查询、JSON storage/domain/cache、working-directory、AgentLoop、空的受控 Agent preset 和默认模型。空 preset 的 `plugins: []` 防止标准 preset 再加载工具。`shell-env` 仅注册每次执行的环境贡献者，不启动 Shell。`commands` 也仅是被动 registry。

原生 SessionController 的文件与技能子插件要求 `fileReferences`、`connection`、`attachments`；WorkspaceController 无条件装载的 DirectoryPicker 子插件要求 `directoryPicker`。这里选用公开 browse provider，不加载原生系统文件对话框。服务存在不赋予成员任意全局目录、文件、凭据或工作区管理权限；共享浏览器和 Host 仍按本人会话及有限 RPC/action 授权。

`credentials-local` 只为原生 Connection 提供宿主内部传输密钥，固定写入已有 `.host` 配额根。配方不安装用户凭据控制器、个人 API key、authorization、旧登录/owner 页面、Shell、terminal、jobs、subagent、stdio MCP 或插件安装器。Connection 的旧 Cookie/WS 入口仍须由 shared-browser 和 Edge 的生产规则拒绝。

原生 UI 行覆盖 chat、工具、附件及真实 `dsh.client.inject` 依赖闭包；它们的 Host 面为公开被动插件。没有加载全局插件管理、模型设置或终端 UI。`host-frontend-static` 与 shared-browser 必须使用同一真实 SDK 的 `dist/index.html`；`web-frontend` 仅为资源包，本身没有可加载的插件根导出。

## 安装时取值

示例中的 webserver 回环地址和端口由现有受信 renderer 替换为池的受控监听地址与分配端口。`shared-ports.configFile` 中的示例 UUID 也由 renderer 替换；示例文件不含密钥。

默认模型示例为 `gpt-6.1-sol`，必须与安装方的 `shared-host.json` 模型路由一致。平台技能只从容器只读挂载 `/run/auriga/skills-active/current` 读取，禁止追加主机目录、用户根、项目技能或 preset 内嵌插件。会话与 storage 写入已有 `/home/dsh/.host`，不新增绕过既有硬配额的成员目录。

## 验证范围

`clients/dsh-plugin/test/shared/profile-packages.test.mjs` 只做无服务启动的公开包验证：真实根 export/版本、配置 schema、必需 inject 服务、原生 client 依赖和 bundle、真实 frontend 资源，以及真实 ClientModuleRegistry 对固定 browser URL 的识别。它同时保留未规范化 `/run` 输入丢失客户端登记的反例，并检查规范化结果与直接公开导入的模块和类身份相同，真实 client 登记恢复。可设置 `AURIGA_DSH_PUBLIC_PACKAGE_ROOT` 指向锁定套件的 `payload/upstream/app`，并设置 `AURIGA_PROFILE_AURIGA_PACKAGE_ROOT` 指向已整合 browser 的 Auriga 包，使用 Node `--expose-internals` 运行。

这些纯验证不替代真实 Loader 的构造与启动审计，也不替代正常成员页面验收。Agent 的封闭包允许表、launcher 规范化器调用和固定 browser URL 例外需先接通；由主控串行完成完整 DSH 与正常浏览器测试后再部署。
