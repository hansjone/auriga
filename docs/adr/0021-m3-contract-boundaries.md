# 0021 M3首批身份、AI与资源契约边界

- 状态：C3正式契约，待独立审查；实现由I3a/G1/R1/WR1/AR1/E3/Z1分别交付
- 日期：2026-10-22
- 依据：用户已批准D1–D7；本轮用户重申开源通用部署与域名/内网纯IP双场景

## 决定

1. 托管AI使用专用 `ctx=instance/ptyp=user/aud=auriga.ai-gateway`，绑定主人、实例与凭据代次，
   权限只取本人有效权限与模型调用/本人用量的交集。IAM只对svc:workspace签发，gateway在线查workspace绑定。
   **本条取代ADR0016第3条“实例令牌”的歧义**：团队模型及未来团队MCP用专用AI JWT，
   `ait_`仅限workspace实例API，不扩权。未来G2工具权限另定，不能把现行仅模型权限当全部MCP授权。
2. 数据面托管调用必须经节点专有实例socket→节点mTLS→edge内部入口→受信edge mTLS gateway，
   gateway比较JWT、受信转发标记与在线绑定；公网拒收instance，禁止IP/Host/自报头识别实例。
   最大300秒token、正常重叠最多60秒、紧急无重叠；到期/撤销取消流，控制面不可用拒绝团队AI。
3. 首批模型只声明明确的models/chat-completions字段、JSON与SSE。
   `/ai/v1`局部错误用AiError；管理面仍Problem，错误code不分叉。
   模型幂等只保留执行/账本状态，不保prompt/正文重放；同键终态不再执行。
   stop仅数组、单字符串不支持；固定DSH rc.2源码的pi-ai适配器本身在网络前拒绝stop选项，
   此处不是完整OpenAI兼容承诺。实际目标DSH JSON/SSE/工具/错误展示仍须G1/Z1假上游联调验收。
4. 预算按UTC日/月、单币种、准入价格/配额快照，同一DB事务抢主体并发与双周期预占。
   默认2个请求/25%，不限周期必须有正数绝对在途上限；0配额禁止。
   unknown不写0、不因租约退款，不确定预占继续占用。Go生成的BudgetAdmission/UsageRecord是共享消息，
   账本持久化/跨副本原子行为由G1c实现。
5. 首批资源只允许plugin。共享审核/发布者不得为版本作者；L4/private仍未审核且受个人插件策略控制。
   三类签名用途（发行/撤销/降级）与原身份头/面板密钥分离。
   新资源envelope签名覆盖租户/包/版本/单调安全序号/有效期与完整statement；旧PluginRef.signed仍独立核验。
   签名与ACL分别检查，不复用占位签名，不用SemVer替代防回滚高水位。
6. 撤销清单通过ApplySpec传播，独立部署根信任，不通过spec任意改根。
   清单有效期最多24小时；离线过期也必须停止含受管资源的实例，恶意撤销不能等待空闲。
   签名JSON的严格字段/时间原文本/整数映射及Java/Go共享向量在contracts/resource；
   未实现的agent根信任持久化、实际撤销停用、下载认证和renderer消费不能记为已交付。
7. **本条取代ADR0017及旧插件协议的托管“同源会话”推论**：静态面板与实例同源只解决加载；
   账户/敏感操作走门户源，首批不开放托管成员API桥接或实例CORS；CSS自包含，无styles字段。
   最小只读桥接或隔离iframe须另定契约/威胁评审。连接设备凭据仍只在插件Node侧。
8. 契约服务于全部开源部署。NAS只是用户提供的真实部署验收环境，不能把真实地址、私有脚本或单机例外
   代替公开通用能力。允许域名与明示内网IP:port HTTP前台；不降低内部mTLS或身份绑定。
   单IP双端口/管理CSRF入口由独立IP0契约处理，不在本C3改变ADR0020隔离边界。
   D5默认拒内网模型出站；D7不接付费模型/S3/KMS/扫描云/新中间件。

## 单一来源、生成与兼容

`contracts/catalog/m3.json`定义新增权限（中英文/adminOnly/敏感/角色）、域错误HTTP/gRPC与事件来源/subject/ACL；
OpenAPI操作只引用，不另建权限表。JSONSchema、protobuf与OpenAPI各有其传输职责，
跨格式映射写进contracts/ai和resource说明，Java/Go/TS从正式源生成。
Go HTTP模型复用现有OpenAPI Generator固定版本，只生成models和标准库utils，不引入validator依赖。
命令见infra/gen/README；Java CI Linux重生检查漂移，未改根依赖或普通Java构建的Go文件。

protobuf仅新增服务/消息/字段并保留全部原字段号；新ctx值是枚举扩展。
org模型0限额由D3明确为禁止，是先前未定义含义的正式细化；后续实现需要拒绝测试。
HB1已有四个必填nullable心跳计数原样保留，不恢复缺省0。
对签名对象，未知字段不适用普通消费者“忽略额外字段”，须新schemaVersion和重新签名。
现有API路径/字段未删除；新增通道和签名要求由未来相应功能启用，不能称旧agent已经执行。
若后续实现发现需变更本契约，必须先通过独立审查，不能在服务中发明第二套字段或错误码。
