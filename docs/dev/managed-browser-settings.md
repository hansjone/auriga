# 托管浏览器的身份、会话与目录边界

托管身份来自实例绑定的设备凭据，不来自浏览器的本地登录状态。平台签发的逐请求身份头仍须匹配实例主人；管理员权限不自动获得其他成员实例的使用权。插件只在独立验证过的设备访问令牌和凭据文件均未到期时接受业务请求；明确吊销立即拒绝，短暂 IAM 故障最多保留原令牌剩余期限，不延长验证窗口。消息网络故障与身份有效性分别处理。

失效后 HTTP 业务及新连接拒绝，已有浏览器 WebSocket 在一秒内关闭；首页提示从门户重新进入，健康检查仍可读取但 `ready=false`。状态诊断保留托管模式及绑定成员，不退成“未登录”。成员名称由既有受鉴权目录读取，读取中明确显示正在加载；同一凭据的瞬时重试不清空已核实名称。

## DSH 0.2.0-rc.2 原生模型设置的限制

该版本 `@deepseek-ai/dsh-client-ui-settings` 的公开说明明确：非 loopback 页面没有 Host 持久化设置。其客户端用 `remote.$host.isLoopback` 决定镜像是否读取；远程页面的镜像从 `unavailable` 起步，`ensure()` 不调用 `settings.describe()`。模型页因此显示 `settings are unavailable in this browser`，即使真实门户主人直接调用 `settings/describe` 已成功。不能据此推断 Auriga 身份门拒绝了请求。

Host 的该包 Config 只有代码工作工具 `enabled` 偏好，没有托管主人持久化开关。Connection 的公开 transport 组合提供 `__DSH_TRANSPORT__.ownsHost`，上游用于声明页面拥有 Host 的特权设置席位（例如页面自己的 worker Host）。Auriga 在自己控制的托管页面组合中，通过公开 `webserver/index-inject` 的结构化 global 声明同一席位，普通 HTTP/WebSocket carrier 保持不变；不修改已经运行的 Connection/Host 事实或镜像私有字段，不改上游包。

该声明只装配于 managed 模式：只有逐请求身份头匹配实例主人、Origin 通过、且设备凭据仍有效时才可读取页面和使用业务 API。它是前端设置持久化选择，不是服务端授权；缓存页面不能在吊销后继续调用业务。非主人、无签名、过期及伪造请求仍由原门锁拒绝。其他模式不注入；已有 transport 声明冲突时拒绝输出页面，不覆盖现有 carrier。原生插件安装等平台管理动作仍受独立保护。

## 托管设置写入策略

采用服务端拦截，而非启动后还原：阻止错误配置进入持久层及实时服务，避免重启前托管凭据被导向其他地址。仅 managed 组合包装公开 `settings.update/replace/mutate`，HTTP 与 WebSocket RPC 使用同一服务。一次操作数组先完整授权，混合合法/非法写整体拒绝；原生配置文件编辑 `prepareDocument` 拒绝，不能绕过字段策略。拒绝信息明确说明平台托管，并提供中英文提示；Host 控制器保留其 `settings/rejected` 错误映射。

主人允许修改的命名空间和字段（仍须通过 Host schema/CAS 校验）：

- `locale`：preference；`ui-theme`：preference、fontSize。
- `ui-settings`：enabled（开发工具显示偏好）；`ui-settings-general`：welcomeNoticeVersion。
- `ui-settings-account`：version、step、purpose、process、completion、usage、developerTools（引导/显示偏好）。
- `ui-conversation`：busyEnter；`ui-chat`：transcriptView、performanceUsage、linkOpening。
- `agent-default-model`：provider、model、reasoningEffort；`agent-preset-registry`：selectedDefault。
- `subagent-model-selection-settings`：enabled、allowedModels。
- `llm-pi-ai.providers` 下除 `auriga-managed` 之外的个人模型路由，可增加、编辑、删除。整节 replace、根/父路径 set/unset 被拒；update 只接受个人路由合并，mutate 须明确到个人路由路径。

`auriga-managed` 整个路由（含地址、模型、Authorization）、Auriga 插件自身、网络/传输、权限/沙箱和托管凭据配置，以及未列出的命名空间/字段均默认拒绝。新增 Host 命名空间不自动获得写权限。个人模型不得引用 `AURIGA_` 保留凭据名，credentials 的 set/unset 同样拒绝该前缀；真正的托管设备/模型凭据来自节点管理的文件，不在个人凭据存储中。个人 API key 仍通过原生 credentials 服务保存。这里保护配置入口，不宣称阻止拥有相同 OS 权限的模型工具直接改文件。

已有 agent 任务/模型流不会因浏览器凭据失效自动取消；当前门禁只阻止新的业务 HTTP/WS 并关闭浏览器连接，不能把它当作后台任务取消机制。首次设备验证期间首页也可能显示会话不可用；后台任务取消及首页“验证中”状态另行处理。

每次 DSH 升级须重验真实 apiproxy 的根外 list/createDirectory 拒绝，以及 HTTP/RPC 的个人设置成功、平台设置拒绝；公开 picker capability 允许消费者缓存，单元测试不能替代这项宿主集成检查。

## 工作区目录选择

上游 `@deepseek-ai/dsh-host-directory-picker-browse` Config 只有 `maxEntries`，没有根目录或默认目录选项；`workspace-controller.documentsDirectory` 只影响首次自动工作区创建，不限制浏览器的选择与创建。`DSH_WORKSPACE_ROOT` 不是该浏览后端的约束配置。

Auriga 使用公开 `directoryPicker.capability()` 包装 browse 的 `list/createDirectory`：空路径默认 workspace，面包屑与 Home 以 workspace 为根，外部目录和指向根外的符号链接不作为候选，创建目录的父路径与单段名称均须通过检查。原生 chooser 只能在返回后校验所选目录；云端 Linux 使用 browse。现有 `workspaceRegistry.create` 继续拒绝根外注册。

这是目录选择器约束，不是模型工具的文件系统沙箱；底层 Node 路径接口也不承诺抵御同一用户并发换链接的竞争。更强的文件系统隔离应由运行时挂载和独立权限承担。
