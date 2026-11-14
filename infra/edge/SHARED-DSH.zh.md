# 共享 DSH 入口接线说明（E 线）

基线：`f6001a02`，权威协议为 `contracts/shared-dsh/README.md`、schema 与 `shared-dsh-edge.yaml`。本线不修改契约，也不修改原生 browserPeer。

当前产物是可独立测试的 E 数据面与接线接口；尚未在 main 启用共享运行时。不能仅配置一个共享池的 owner 路由就上线。

## 联合接线

1. W/IAM 完成后，由启动组合根设置 `Gateway.Shared`。`SharedEntryAuthority.IsShared` 从权威分配判断物理实例模式；`Check` 调用 W Resolve，必须核当前成员、平台会话、ready 状态及完整 Binding（包括 revision/epoch），错绑定返回 `shared.binding_mismatch`，撤销返回 `shared.revoked`，未 ready 返回 `common.conflict`，权威不可达失败关闭。不得用路由投影、推送缓存或有效期代替 Resolve。
2. `Watch` 先订阅，后进行最终 Check。适配器必须在退出、禁用、切换、平台会话到期以及失去权威联系时取消连接，并在返回的清理函数中释放订阅。测试替身仅用于证明取消能关闭真实升级连接，不能替代生产订阅适配器。节点只能从既有 NodeControl 接撤销，不订阅 NATS。
3. W 票据使用独立的受信 Ed25519 公钥集合；E 身份使用独立私钥/kid。不复用 owner v1 或其他能力密钥。密钥从部署的受保护配置注入，禁止接收浏览器 jku 等输入。
4. 建立受保护的持久化 NATS KV bucket（生产复制数按集群部署），MaxAge 至少 12 小时，注入 `NATSSharedEntryStore`。一次 Create 同时保存票据消费与完整 cookie 绑定，只保存 cookie 哈希。浏览器无 KV 权限。该记录不会在已消费票据的 60 秒有效期内被提前删除；禁止运维重置活动 bucket。边缘多副本共用同一 bucket。
5. R 注册共享实例时必须设置 `ingress.Instance.Shared=true`。节点组合根注入 `Config.Shared`，使用独立 E 公钥集合和 W/IAM 检查/撤销适配器；缺适配器时共享实例拒绝。不要把共享实例注册成默认独立实例。节点旧文件/日志管理路由对共享实例关闭，成员文件功能由后续成员路径提供。
6. U 从 W `POST /me/placement/open` 获取票据，以 JSON body POST 到可信 `exchangeUrl`，不把票据放 URL。domain 跨源 JSON 仅允许部署门户 Origin 和 Content-Type 预检；请求需携带 credentials 以收实例 cookie。成功后转到正常实例根（domain `/`；IP `/i/<physicalInstanceId>/`）。普通实例写操作与 WS 保留现有 Origin 门禁。旧 `/open/<id>` 独立容器流程不生成共享 v2 cookie，其旧票据也不能通过共享入口；共享门户改用 W open。
7. P 就绪后验证真实 DSH Web/WS、原生 Cookie、browserPeer 与 `/connect`。节点入口仍复用已有仅信 edge 的 mTLS；原始 DSH 端口由 R/部署网络确保不公开，本线没有修改容器端口发布或防火墙，须联合实测。这里通过随机端口替身测 HTTP/WS，不占真实 PG/DSH 槽。

## 数据面与验证

交换严格检查 body（包括重复字段）、签名用途、规范 JSON、完整绑定、期限以及原子一次性消费；cookie 为 HttpOnly、host-only，沿当前 Secure/Path 命名规则。逐请求签发 v2 身份，绑定去部署前缀后的原始 escaped path、大写 method 与平台会话。节点拒绝缺失/多值身份、错误签名/用途、方法/路径差异、跨成员、旧 epoch/revision、过期、重放及权威失效。节点身份一次性消费通过受保护的 bbolt 控制 DB 事务持久化至到期，重启继续拒绝重放。组合根须注入 `SharedIdentityReplayStore{DB: db}`，DB 生命周期由节点管理；缺存储拒绝。每物理实例保留唯一 ingress，禁止给同实例配置不同 DB 的并行入口。

两个模块的 `shared_wire.go` 是模块本地 wire 校验，不新增对外共享类型。共享库抽取需 K 分配路径。`TestSharedKOracleInterop` 直接使用 K 的 `EntryDouble`/`exchangeTicket`/签名 oracle 双向验签，需先在 `services/contracts` 执行锁定的 `npm ci --ignore-scripts --no-audit --no-fund`；Go 工作流在 Linux/Windows 上显式安装这些锁定依赖并单独执行互通测试，缺依赖时测试直接失败。其余测试不依赖 Node。所有测试签名密钥运行时生成。

## 已接受风险

首期互信团队仍接受：原生 owner 旁路；跨成员读取文件和会话；驱动他人会话后以其企业身份操作、审计错归该成员；一个进程故障影响全池。平台身份检查和撤销不代表原生 DSH 已隔离，也不代表原生 Cookie/browserPeer、`/connect` 不能以 owner 操作。真实反例、部署网络边界与完整门户验收由主控按 W→IAM→E→R→N→P→U→V 串行接线后保留证据。

## 实例模式缓存失效

`Gateway.Shared` 按物理实例 UUID 缓存成功的 `IsShared` 结果，查询失败不入缓存。缓存仅选择入口认证流程，共享成员的 Binding、epoch、revision 和平台会话仍逐请求查权威。已缓存的独立实例在分配服务故障时沿旧实例认证流程继续服务，未知实例查询失败返回503。

缓存以控制面事件失效，不设按时间自动过期。联合接线时，控制面应在 W 权威状态提交后、发布新路由及节点配置并放行入口前，在每个 edge 副本调用 `SharedEntry.InvalidateInstanceMode(id)`。失效与正在进行的模式查询交错时，旧查询返回503并丢弃结果。删除事件同时释放缓存项，重启或更换 SharedEntry 时缓存为空。成员在共享与独立模式之间切换若分配新物理 UUID，首次请求自然重新查询。多副本需先完成全部失效，再放行新入口和节点注册。

独审低项②的 bbolt 写锁与过期记录扫描吞吐，留给 V 线按真实静态资源负载压测。