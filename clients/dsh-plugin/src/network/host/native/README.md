# 原生 HostPort（N1，安装能力未验证）

当前不能作为已兼容的 rc.3 生产入口：可信隔离句柄发行者、rc.3 pending-request API 和
dsh-session 源码摘要均未验证。index.js 的两个原生工厂保持失败封闭，J4 必须维持
unconfigured，不建连、不注册发送工具，不能改用底层操作构造器绕过门禁。

## 返工1：来源与发行物

安装门禁使用模块私有 WeakSet 核 security 句柄来源，不导出登记/品牌铸造函数。
只读宿主没有可验证的隔离服务构造器，登记表当前为空；普通对象、原型复制、Proxy、公开
pin 和 no-op 断言均不能启用入口。真实发行者须由另一个受审宿主工程提供，不冒充已实现。

readRuntimeFingerprint 只供诊断：版本/build/dirty 从实际 manifest 文件读取，不接受自报值；
读取 controller、persistence、dsh-session 三份字节并计算 SHA-256。文件路径由调用方指定，
所以结果不证明当前加载的就是这些字节，也不获得来源品牌。ASAR 须由 Electron 宿主采集。
controller/persistence pin 来自 failure-validation §1.2；dsh-session 无可核 pin，显式为 null，
完整指纹验证拒绝。不能用另一份安装包、任意计算出的摘要或文件重读补出加载证明/绕过 TOCTOU。

## 底层操作契约

host-port.js 的 createHostPortOperations 和 source-context.js 的 createSourceResolver 是供可信
组件组合的底层算法，未从 index.js 导出；它们不核安装身份、不授予联网权。算法测试显式
导入底层端口，用抽象 authority 夹具，与生产入口拒绝测试分开，不能当真实宿主或隔离证明。

- getSession 沿原身份读归档/stat/inspect，不查前台、不重建、不把读故障当不存在。
- 新建和已有会话统一经过 durable + 严格 awaited flush 屏障，包括 cold adoption；
  核 ID/cwd/preset，provider/session 变化、归档、false/异常均不报成功。丢创建响应只查原 ID。
- prompt 只允许 queue，原 requestId/AbortSignal，同 session 串行，跨 session 并行。
  30 秒上限到时 abort 并释放锁；调用不明保持本进程不重做，最多留 1,024 项，满后拒绝新增。
  重启后的不重做由核心耐久 ledger 保证，不能以 timeout 当作“未执行”。
- 历史只认 user/message.source.rpcId；live pending 必须由经过验证的 hasPromptRequest
  authority 提供完整证据。缺方法或返回 unverified 时拒绝判断不存在；冷恢复后再次核证据。
  不再访问未经验证的 native inbox 属性。
- requestId 与正文摘要一一对应由核心 recipient/messageDigest 耐久记录保证；不得同 ID 换正文。
  底层 requestId 查询不是独立正文冲突校验器。
- origin=subagent 拒普通入口；仅有 parentSession 的 lineage 暂不支持，需补 ownership/fork 证据。
  parentSession 也可能来自 fork，不能直接认作子 agent。N2 专用 delivery 未实施。
- 来源投影核真实 Agent/Session 对象身份、ownership、唯一 bound/open local，仅释放四个引用。
  它不证明“当前正在执行本次工具回调”，不是可序列化 IPC 能力；J4 须在真实回调中另核授权。

native 失败仍归 host_incompatible；J4 不得把全部瞬时错误永久缓存为不兼容，应沿原身份对账。
历史扫描暂为 O(n)；缺可信 revision/完整 pending API，故未加入可能漏判的缓存。
普通 prompt 可先调度后 flush，不承诺副作用恰好一次，结果不明不得自动重做。

## 只读源码证据（以任务指定 oclaw 为根）

- dsh-team/lib/dsh-adapter/team-session.js:130：controller.prompt 的 request/signal；
  同目录 README.md:29：queue 与 rc.2 版本声明；README.md:36：user/message/source.rpcId。
- team-session.js:229,233：sessionPersistence、sessionController 等服务注入。
- dsh-team/lib/dsh-adapter/team-workspace.js:24：persistence.open(id,'read')；
  dsh-team/test-compat/team-probe-plugin.mjs:38：persistence.flush()。
- 在 dsh-team/lib/dsh-adapter 与 test-compat 检索 nextTurn、nextStep、hasPromptRequest、
  snapshotEvents、session/flush，找不到 rc.3 原生字段/实现。旧核验日志不替代指定宿主源码。

因此没有保留声称 rc.3 inbox 字段存在的类型或夹具；夹具仅用抽象 pending-evidence Map。
宿主工程还须核真实服务对象、session writer、flush 参与者；stat/方法名/flush=true 不充分。
还须证明工具不能经文件/备份、环境/句柄、内存/调试、日志/转储/导出/IPC 读写凭据与各账本。
IPC 认证对端并绑定当前 local/精确操作，不信工具自报身份。J10 再核真实 rc.3 创建/恢复、
首消息竞争、强杀/满盘、双 DSH 与同机隔离；本目录不启动服务或修改宿主系统权限。
