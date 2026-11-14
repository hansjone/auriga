# K2 增量管理契约与裁决（2026-11-15）

本批只新增浏览器管理协议。机器契约为 `openapi/shared-dsh-admin.yaml`，由 workspace 聚合；严格 JSON 定义为 `shared-dsh-admin.schema.json`。两份文件由 `node contracts/shared-dsh/build-admin.mjs` 生成，`--check` 只读查漂移。`admin-ports.mjs` 仅为测试 oracle。所有新增接口只允许有效 admin 会话，逐次核 `workspace.placements.read/write`；写入还核 Origin/CSRF、严格 `0 <= now-stepUpAt < 600`。跨租户目标404；授权不可核503。使用既有 IAM 生命周期，不新增登录或企业凭据存储。

## U 五项裁决：接受

1. `GET /admin/members/{memberId}/placement` 返回成员与独立授权，尚未分配时省略 placement（成员存在仍200）。已分配 placement、外层 memberId 和 entitlement.memberId 必须一致。ETag 为服务端产生的强不透明标签，覆盖该成员 placement 与 entitlement 的复合修订。它与原切换接口 If-Match 使用同一版本域；U 原样保存、回传，不从 revision 猜标签。任何一项权威变化都使旧标签失效。所有200/202 JSON 响应发 `Cache-Control: no-store`。
2. `PUT /admin/members/{memberId}/dedicated-entitlement` 以该复合 ETag 授予/撤销，返回新授权及新复合 ETag。授权写入与分配 CAS 在同一成员串行边界。授予不创建分配、不切换模式；撤销时若仍 dedicated 或有在途切换则409，须先完成降权。授权不是独立模式已经可用的证明。沿现有 placements.write，不新增第二套角色权限。
3. `POST /admin/placement-rebuild-plans` 持久化完整旧成员名单和每人 targetMode，dedicated 即“继续独立”。计划 ID 由服务端创建，GET 重启后可读取，PUT 在 draft 状态以计划 ETag 整体替换。成员 UUID 不得重复；legacyInstanceId 必须为该租户该成员的唯一旧实例，不得重复或引用其他实例；所有 dedicated 选择须有独立授权，创建/更新及开始执行均重核。首批上限1000，超出分多个计划，禁止静默截断。

   `POST /admin/placement-rebuild-plans/{planId}/start` 要求计划 ETag、导出窗口明确确认、固定 discard/archive-and-reset。开始后名单不可改，保存名单时不删文件或执行重建。W 必须先持久化维护 hold、围栏旧实例与事务 outbox，再202；202只代表登记。进度 GET 按原名单顺序返回每个成员，尚未分配子操作时可省略 operationId，分配后 ID 固定；每成员 state/修订与权威操作一致。failed/blocked 必须带 failureCode，succeeded 不带；completed 仅当全部成员 succeeded，否则未确认停止保持 blocked，不能按完成数忽略失败。名单、子操作与收据均可重启恢复；重试同 start 幂等键只取原结果，不能重新删数据。继续独立的重建不是 dedicated→dedicated 模式切换，因此不复用要求 fromMode != targetMode 的旧 DTO。
4. `GET /admin/placement-capacity` 提供同租户全部池观测值和 GuardianImpact；不新增推送，U 可显式刷新或轮询。observedAt/validUntil 为 UTC 秒，必须 `observedAt <= now < validUntil`；无法获取完整新鲜观测返回503，不用零值冒充。内存数来自运行时权威采样，登记/活跃及预留来自 W，原有20/4/1和80%门槛不改。accepting=false 可表示授权集合或其他准入拒绝；达到任一门槛不得 true。内存采用整数乘法比较，禁止浮点取整跨越80%边界。快照只用于展示，准入时必须重新检查，不保证稍后可用。池 UUID 不重复；impact 及 affected 中 tenant/physicalInstanceId 必须匹配当前租户与所在池，禁止跨租户名单。过期、取消超时仍显示全池范围，不能以容量恢复清除未确认停止影响。影响按 operationId 去重后保留最新 observedAt；blocked/stopping 不可静默丢弃，stopped 也不等同已可重新启动。该管理 API 可暴露物理池 UUID，不扩展原成员接口的信息范围。
5. `GET /admin/members/{memberId}/placement-switches/lookup` 使用请求头 Idempotency-Key 查原切换，键不进 URL。查租户+原操作者+成员+原 create-switch 操作命名空间，读权限不允许枚举其他操作者的键。仅返回已持久化原操作，未找到/过了24小时保留期404；不触发重发，不返回新票据。原 POST 结果不确定时 U 先查证，404不代表操作从未执行，不自动换键重发。

所有新写入的幂等记录按租户、操作者、操作种类、目标及完整请求指纹至少保留24小时，同键异指纹409。相同请求重试先找到原幂等结果，再核原 If-Match，避免成功响应丢失后因修订推进错误返回412；记录不存在才执行CAS。缺 If-Match 为428，标签过期412。创建计划没有旧对象，不要求 If-Match。读取不会延长幂等有效期。原切换写接口的冻结语义不变。

## 其他线逐项裁决与接法

- W：接受补 HTTP 管理协议，由 W 用既有 SharedDshPlacements/SharedDshMaintenance 适配上述契约，禁止把 Java 内部端口冒充浏览器 API。其报告大量字符已被问号替换，无法从损坏内容推断新要求。可辨识的 NodeControl 字段9/10/11、运行收据、资源回报、撤销、guardian 与 placement.switched 映射均已有 K v1；由 W/R 按 schema JSON 显式映射，不新增重复协议。若 pending/switching 要求 Resolve 放行为 ready：拒绝，冻结只准 ready，需在 W 内部另做探测而不签对外票据；本批不改此语义。
- IAM：报告明确无已知 K 缺口。管理授权沿其 PlacementAuthorization 公共端口和既有远程会话/权限协议；部署边界需要实际 RPC 时由主控提供具体输入再裁决，不预造另一套授权服务。
- E：报告未申请新增契约。SharedEntryAuthority/SharedIngressAuthority、主动撤销和权威不可用拒绝、NodeControl 及票据交换已有冻结协议，由 E/W/IAM 实现适配。域名部署的 CORS/预检、cookie 与正常重定向读取属于 E 实现接线；不能靠本批 schema 宣称跨域浏览器已通过。
- R：报告明确未申请契约变更。现有 RuntimeIntent/Receipt/ResourceView/Report/GuardianImpact 和 NodeControl 承载已足够，落盘、XFS、独占锁、supervisor 与采样由 R/W 接入。容量快照聚合若需要新的节点遥测协议，须带现有度量缺口单独交 K，不能伪造新鲜值。
- N、P：指定 `.agent-report.md` 均不存在（首次读取2026-11-15）。本批无法裁决未提供条目，不能报两线无缺口。已冻 SessionBinding、Capability、audience/scope、credentialVersion 与任务围栏继续适用，等待主控补报告再追补；不改变模型、消息或企业能力语义。

## 兼容与交接

无旧字段删除、枚举放宽、旧路径替换、NodeControl 字段重编号或 ready/封存/撤销语义变更，因此无迁移。部署顺序为 W/IAM 实现增量 API → U 消费生成 workspace 类型；服务端未实现新接口时 U 保持不可用提示。严格 schema 和 oracle 不是租户授权实现。全新非作者独审、精确 HEAD CI 和受保护 contracts 审查由主控安排。本批不部署 NAS、不占真实 PG/DSH 槽，也不宣称正常入口已交付。
