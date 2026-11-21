# 共享 Node HTTP 消息适配

本批支持共享云端原生会话之间的 durable unicast。普通桌面/NATS 互通暂时明确拒绝，旧 owner 权威支路不变。身份和来源登记在 IAM，typed-local、高水位、邮箱、尝试、执行准入、收据及签名复用现有 Collab 实现。

生产入口为 ai-gateway 的 `--bifrost-config`。在既有配置中增加 `sharedMessages`，与模型独立监听，不依赖 NATS runtime 或消息 keyset 的启动时有效性：

```json
{
  "sharedMessages": {
    "listen": "0.0.0.0:8095",
    "tenantId": "<固定业务租户 UUID>",
    "allowedNodeIds": ["<受信 Node UUID>"],
    "messagePublicKeys": {
      "message-kid": "/run/auriga/message-capability-public.pem"
    },
    "tlsCertFile": "/run/auriga/n.crt",
    "tlsKeyFile": "/run/auriga/n.key",
    "clientCAFile": "/run/auriga/node-client-ca.crt",
    "collab": {
      "Target": "127.0.0.1:9090",
      "ServiceTokenFile": "/run/auriga/ai-gateway-service-token"
    }
  }
}
```

远端 Collab RPC 必须额外配置现有 `CertFile/KeyFile/CAFile/ServerName`，仅数字 loopback 可用现有明文内部通道。K message 公钥与 enterprise/model 目的分开，普通 JWT 不可替代。Node客户端证书必须含唯一且在allowlist的 `auriga://node/<UUID>` URI。

Combined 平台启用 `auriga.collab.shared-messages.enabled=true`，并保留既有 `auriga.iam.shared-tasks.enabled=true`、`auriga.collab.network.auto-renew.enabled=true` 和受信 Collab→IAM RPC 配置。安装需先具备当前租户/realm 的正式 msgnet 根、control/publication/announcement；缺失/过期时消息调用明确不可用，模型监听不会因此停止。正常 Flyway 执行 IAM V19、Collab V31。平台装配不在启动时读取 public state 或借用 IAM datasource 处理邮箱。

每个真实原生会话获得稳定 logical node/local，不增加每会话 NATS 连接或心跳。工具任务/JTI 是当前调用围栏，续 cap、新任务和公司 token 正常轮换不重置地址、producer epoch 或邮箱。归属、平台会话、原生 binding/boot 变化拒绝旧事实；不从普通 JSON sender 或物理池 owner 代入身份。

下列为冻结的 Node/Host 映射及权限边界。

# Node→N 共享消息HTTP冻结映射

POST `/internal/shared/messages/send` 或 `/internal/shared/messages/receive`，Content-Type application/json，无query；复用模型相同Node mTLS CA/精确Node UUID allowlist模式，部署tenant固定，不能用户JWT旁路。

三个各自单值的头：`Auriga-Shared-Capability`（原K签名串）、`Auriga-Shared-Session-Id`、`Auriga-Shared-Task-Id`。

send body：`{tenantId,sessionId,taskId,capability,messageId,target,body,contextId,bootId,nativeWitness}`。四个身份字段必须与头及部署tenant精确一致，只作交叉核验；权威仍是K验签+实时IAM Resolve。messageId为稳定UUID，正文body传原JSON，既有msgnet业务预算16384 bytes不放宽。target为`{realm,node,local}`，均canonical uint64 decimal string，local>0；模型工具只需提供aur1地址，Host解析结构，服务端按真实typed-local目录核对类型与当前归属，不要求模型猜对端taskId。不得接受sender/owner字段替代权威。

Node不重试或改写messageId，取消通过HTTP context传递。成功只返回原msgnet-v2完整结果JSON；既有network/shared/common错误脱敏返回，不捏造投递/执行成功。

handler已配置安装与底层消息keyset/连接暂不可用必须分别记录。Node的send/receive操作只有在真正安装本传输后才列入health；底层暂降级调用明确失败，不应移除已安装操作或阻模型/工具宿主启动。

## messageContext（固定Node操作）

POST `/internal/shared/messages/context`，同上三单值头。Node固定UDS操作名`messageContext`。请求：

```json
{
  "tenantId": "UUID",
  "sessionId": "UUID",
  "taskId": "UUID",
  "capability": "K signed token",
  "operationId": "UUID",
  "bootId": "UUID",
  "nativeWitness": {
    "witness_id": "UUID",
    "binding_id": "UUID",
    "binding_revision": "1",
    "observed_at": "2026-11-19T00:00:00Z",
    "valid_until": "2026-11-19T00:00:30Z"
  }
}
```

`nativeWitness`严格保留原SourceControl五键。binding_revision为正canonical uint64 decimal string；两个时间UTC整秒RFC3339，valid_until>observed_at且窗口<=30秒。operationId由Host真实in-process row生成，对完整K binding/native-binding/boot稳定；每次witness_id新建。Host不得提交realm/node/local/source_binding/producerEpoch/owner。服务端按受信Node证书和完整K权威登记并分配真实typed-local来源。

回包JSON固定：

```json
{
  "binding": {},
  "taskId": "UUID",
  "contextId": "UUID",
  "producerEpoch": "1",
  "connection": {
    "transport": "shared_node_http",
    "realm": "1",
    "node": "2",
    "incarnation": "UUID",
    "epoch": "1",
    "bootId": "UUID",
    "channelNonce": "UUID",
    "requestId": "witness_id UUID",
    "validUntil": "2026-11-19T00:00:30Z"
  },
  "registration": {
    "realm": "1",
    "node": "2",
    "registration_id": "UUID",
    "allocation_op_id": "UUID",
    "allocation_fingerprint": "server canonical hash",
    "registration_revision": "1",
    "local": "1",
    "address_type": "session"
  },
  "sourceGet": {}
}
```

binding为原完整SharedDshSessionBinding驼峰protoJSON（含全部placement/auth/session代次），由IAM返回，不能从请求拼装。contextId对同operationId/完整binding/native/boot稳定；registration.local对context稳定。connection全部九键（transport+八字段），incarnation为原正式v2 UUID；epoch/producerEpoch均正canonical uint64字符串；channelNonce为服务端持久随机值，requestId与本次witness_id严格一致。该连接类型不包含也不伪造旧NATS serverId/clientId/ingress。

sourceGet为原正式signed source_get_result，内部source.registration保原六键，不加realm/node。其request_id及server_auth.request_id绑定witness_id；source.native_binding绑定真实binding_id/revision，witness_id及observed_at对应本次见证。服务端来源/连接换代必须显式增加代次、在线拒绝旧代，不允许Host根据缓存或模型字段迁移归属。

## send/receive携带关联与验签

send每次先刷新messageContext，随后携其contextId、bootId、fresh nativeWitness；服务端重核当前完整K归属、同Node/boot/native绑定、当前source/connection代次后才能调用原持久SUBMIT。body不接受普通JWT、sender、source_context或任意签名替换。Node只透传有限固定操作，不重试。

receive为受信后台轮询/执行通道，禁止注册模型可自报sender的receive工具。请求`{tenantId,sessionId,taskId,capability,contextId,bootId,nativeWitness,operation,frame}`，operation闭集`receiveSet|fetch|attemptBegin|executionAdmit|receipt`，frame为原msgnet-v2帧；服务端依当前context重建连接并核真实recipient，不把frame当SharedDelivery权威。fetch完整返回原签名page/item，attempt/admit/receipt完整返回原签名结果，只有Host真实持久接收后才提交原收据，不能把拉取当执行成功。

Host使用现正式`openV2TrustStore`的固定根、keyset与announcement信任链，lazy加载`shared-host.json.messageTrust`（其具体文件配置由Host作者沿现工具实现并补文档）；缺失/过期只消息调用明确降级，不阻模型/工具启动。Host验证sourceGet/page/item/admit等原签名信封及完整connection audience/channelNonce/requestId、native见证和期限。Node/N的mTLS响应保证有限传输边界，不能以回包任意pub或`verified:true`代替正式验签。

首轮messageContext属于新shared_node_http的K已登记session bootstrap见证：仅登记/物化typed-local和来源当前事实，不调用SUBMIT、FETCH或执行，也不默认开启receive_enabled。Host必须完成真实in-process createNativeSourceResolver绑定，再下一业务调用使用fresh见证。旧owner SourceRegister的native resolver门禁保持原样。

正式公开材料：context回包另含`messageTrust:{control,publication,announcement}`，只读取现已安装MsgnetRenewal.publicControl/publication/announcement原签名对象。Host固定配置`messageTrust:{realm,roots:[{kid,public_key_spki}]}`，根来自获准安装；只用原链installControl/update/announce，不从回包取根。messageContext结果可到4MiB，普通业务输入/原帧预算仍65536 bytes，body预算16384不变。

request_id：sourceGet/server_auth使用本次witness_id。send返回原SendResult（原HTTP SUBMIT结果无server_auth，不伪签），稳定messageId为producer幂等键；结果只表示platform_stored/pending，不表示投递或执行。receive各原frame.request_id由受信Host生成，各原结果/server_auth绑定同值；fetch返回原`{app_version,type:"fetch_result",page:<signed FetchPage>,items:[<signed item>...]}`，Host逐page/item验签并核manifest。receiveSet沿原显式开关和receive_version，初始false。

首批范围为shared↔shared，所有目标从真实typed-local目录核对。ordinary desktop交叉暂明确拒绝，旧owner/NATS路径未放宽，不能据本批宣称完整msgnet互通已恢复。稳定context/node/local以完整K session binding+native binding/boot/operation持久登记，与当前调用task/JTI分离；换工具任务、同登录token刷新只重签当前cap，不分配新地址或重置邮箱。cap旧版本/结束task仍不能发起当前调用。

## 普通任务与可选公司材料（Node/Host冻结补充）

普通受控执行/技能helper任务沿enterprise目的签名，但只请求单独`task.execute`，不能与outbound/credentials混发，也不授予Shell/终端/任意插件权限，仍服从既有受控profile。generic credentialVersion是独立task普通授权版本，UAC token正常轮换不改变它。企业能力使用同真实execution归属下独立handle/taskId，仍使用原UAC版本。

AuthorizeSharedCapabilityResponse新增`enterpriseCredentialState`，protoJSON严格枚举字符串：`SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT`（仅有效平台session确实没有uacCredentialId/uacSessionBinding）、`SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE`（当前本人有效材料）；0为`SHARED_ENTERPRISE_CREDENTIAL_STATE_UNSPECIFIED`，Host必须拒绝未知/0。不能依据catch认证错误判断缺材料。平台session过期/撤销、归属不符、已绑定但invalid/foreign UAC均仍拒绝。真实SessionManager.valid保留原uacValidator；若该UAC登录本身已失效，普通任务不能绕过平台登录。

Node任务remembered scopes精确支持task.execute；不能让generic task升级成原始outbound/credentials。Host先开generic核typed状态，ABSENT可用普通受控工具并保持空UAC变量；AVAILABLE再单独开企业子任务，raw=false仍只outbound；close/finally分别结束两task。不改普通用户JWT或全局环境。

同一完整K session的新可信native binding/boot显式注册会原子撤销旧context；保留的旧operation/native key不可复活。此时是新的原生来源，分配新的全局node和producer epoch，旧地址明确拒绝；仅换task/JTI/cap或公司token版本不进入这条换代分支。

context回包另含`mailbox:{receiveEnabled:boolean,receiveVersion:string,headSeq:string,committedCursor:string}`，从真实`collab.network_local_registration.receive_enabled/receive_version`与`collab.network_node.head_seq/committed_cursor`读取。receiveVersion为正十进制字符串，两个cursor为含0的canonical uint64字符串。只有本地inbox cache缺失且服务端false/1/0/0时允许首次空初始化；已启用过/已收到邮件的node丢本地账本应拒绝，不能清空账本重放prompt。原receiveSet显式变更开关，默认不启用。
