# 调用点迁移门禁（PR #325 第三轮）

`shell-auth-call-sites.json` 的 `retainedChannels` 是独立维护的保留通道白名单。每项必须有精确文件、通道名、允许的传输特征、锚定的路由模板、理由和设计章节；不接受目录通配或无理由豁免。白名单变更随 D 的实现 diff 交独审，不由业务生成器自动放行。sources 摘要仅为历史记录，不参加门禁。

托管 heartbeat 只保留 `/api/workspace/v1/instance/heartbeat`；managed host-auth/keys/door/session 保留 edge 准入、受保护本地 key、HTTP/WS 服务端和连接清理。standalone owner 的身份源/OIDC 继续沿原 issuer、DNS、重定向和 owner 守卫。DSH 本地 host、生成页面和设置脚本保留同源桥接，个人模型保留宿主原生能力/流处理。安装器同源制品更新和这些通道的追踪包装不携成员平台凭据。逐文件依据位于白名单，不据此放行平台登录、刷新、Team、平台模型或资源 API 直连。

## 两种状态

legacy 按“文件 + 规范化字面路由特征”核对历史 calls：忽略源码注释、引号选择、调用前后空白和换行，允许删除和普通格式变化；不是按整行原文或全文哈希锁源码。它不分析 HTTP 方法、同一路径的调用次数，不执行常量折叠、动态拼接或跨函数数据流。因此 `fetch('/api/' + family + '/v1/new')` 可能不能被 legacy 识别，测试显式记录此局限；legacy 通过不等于完成迁移。D 仍须逐调用核对身份/方法并测试，不得借这阶段声明零直连。

broker 要求 calls 清空。扫描所有非 generated 的 JS/TS：可识别的平台路由仅在精确文件的 routePatterns 内放行；可识别的 fetch/注入传输调用、WS 客户端和 Node 网络导入仅在该文件 transports 中放行。两个条件分别检查，心跳白名单不能新增成员 API 或 WebSocket。纯注释、类型声明、协议的 `type:'fetch'` 不再被误认成网络传输。21 个实际保留文件作为正例整体检查；白名单外新增直连、白名单内新增错误路由/传输作为负例。

白名单是保留通道的结构约束，不证明动态目标在运行时可信。例如页面 fetch(url) 仍必须经过完整同源检查，owner OIDC 仍必须验证受信 issuer/DNS，制品下载仍须安装器来源及完整性校验；追踪包装的调用者也要单独通过门禁。扫描不替代 D/C 的正常入口测试和平台凭据不进入插件/页面的行为断言。

## broker 固定接缝和已知局限

协议约定后端业务代理的固定接缝变量名为 `broker`，调用形式为 `broker.call('固定operation', input)`；操作必须存在于固定业务清单。禁止将这个接缝改名、解构或用动态 operation。扫描拒绝常见 `const bridge=broker`、`import {broker as bridge}`、`const {call}=broker`；规范调用和这些负例均有测试。

这里没有编译器 AST、导入来源/类型追踪或完整别名分析，不能把无来源信息的 `other.call(...)` 断言为 broker。D 的实际适配器必须遵守固定接缝约定，独审核对从 IPC 适配器到它的真实绑定，不得用改名绕过。未知调用形态/动态代码、重命名的注入传输以及保留文件中的动态目标仍需实现测试与独审；broker 运行时继续按 schema/固定操作清单拒绝未知操作。扫描结果只作这道结构门禁的证据。
