# K3 消费方须知：实现接缝的最终冻结

本文补充 auto-renewal-v2.md；冲突处以本批明确列出的裁决为准。仅冻结契约，不代表 C/N/I/P 的生产接线或 NAS 已交付。

## 运输授权转换（C/I 报告）

运输序列以 `(generation,bundle_version)` 比较。正常换授权保持 generation、bundle_version 严格增加；根紧急恢复可提高 generation 并以根签 first_bundle_version 重新起序列，绝不清空历史、防回退账本或 CA 墓碑。业务 keyset 的版本与运输版本独立，不能相互作为最低值。

新增 RootControl.transport_transition 是根签的精确起点锚，绑定完整旧 SignedTransportTrustBundle 摘要、旧新 generation、新授权ID/完整摘要和首 bundle_version。TransportPredecessor 携带旧 bundle、根签旧授权及旧控制。旧控制只验证历史合法性，不覆盖本地最高控制；旧包使用旧授权公钥验签，不能使用新运行公钥验旧包。所有 tenant/realm 必须等于独立安装域，旧授权必须被其历史控制允许且当时未撤销。新授权必须被最新控制允许、未撤销，具有 network_transport、已生效，scope 是根授权子集；控制绑定不能超过本地已持久最高控制。bundle 当前签名 kid 必须等于新授权 runtime_kid。

正常 `authority_transition` 只允许旧 retire_old→新 dual_trust。新旧授权在最新控制仍同时允许；scope 和 required_reader_ids 完全相同、旧 CA 对象逐字保留，至少增加一个新 ca_id。旧周期先完成再换授权；新周期重新取得双信任持久确认、换叶子和真实握手确认后才能退休旧 CA。根签锚只授权这一准确起点，后续阶段按普通阶段规则推进；不能反复用锚越过已持久更高版本。

`emergency_recovery` 可从任意旧阶段进入新代际 dual_trust；旧授权必须已被最新根控制撤销，全部 CA 的 ca_id 和证书DER均与前驱不重合。前驱只作历史证明，旧 CA 不作为新连接信任根。干净控制面先完成隔离和旧连接驱逐，未确认/未换身份目标保持隔离；新投影/握手完成后再纳管。不可将受侵旧身份成功回执解释成恢复。此转换是事故恢复，不能承诺零中断。

首次或长期离线端仍验证同一完整前驱、历史根链和最新根锚；本地见过同代同版本时摘要必须与前驱一致；本地旧版本/旧代际可验证根锚后追平，本地版本高于前驱或代际高于新包时拒绝。无本地历史允许空 seen 值，不能从收到的前驱覆盖已有值。`transport_transition` boundary_cases 是跨授权转换的生产 adapter 协议，原 consumer cases 中 transport 字段仍覆盖同授权周期；实现应根据授权/代际变化选择该转换验证后，进入共同持久化与普通阶段验证路径。不能因为旧 consumer 输入格式不能表达新证明就跳过验证。

## 全局 CA 确认（N 报告）

新增只读 POST `/api/collab/v2/network/transport/confirmation`，输入 TransportConfirmationQuery，输出 SignedTransportConfirmation。签名域 UTF-8(`Auriga.NetworkTransportConfirmation.v2`) 加 NUL 加 JCS(payload)，由当前仍允许的运行授权签名。HTTP 内部 mTLS+JWT，tenant/realm 从认证主体绑定，跨域403；查不到精确版本404，同版不同摘要409，台账不可用503，Cache-Control:no-store。该 POST 只查询，不写状态。请求 challenge 为32字节随机数的规范 base64url，响应精确回显，issued_at 不得晚于消费者 now，最大年龄60秒（60接受、61拒绝），当前根控制更新立即使旧证据失效。

C 在一个一致性快照中读取冻结 reader 集合和逐目标台账，只输出真实核签、真实材料绑定后的结果；不能由 bundle 声明、进程启动、单个本地 reader 成功推断全体完成。required_reader_ids 是本周期起点冻结集合，在三个阶段中保持不变。新增 reader 在纳管前默认隔离，不能挤入旧周期伪装已确认。

证据绑定完整 bundle 摘要/版本/阶段、tenant/realm、当前授权/控制。逐行 reader_id 唯一且恰好覆盖冻结集合，identity_id 必须与 selector.installation_id 一致，selector 的 nonce/material_revision/manifest_digest/清单和 bundle 绑定必须等于 C 为该身份存储的精确授权材料。expected_selectors 是该台账的完整 selector 摘要映射，绝不能从证据行反填。C 只在 loaded 后接受同代 handshake_verified，叶子替换即重新等待确认。

推进 dual_trust→new_leaves 查 dual_trust 的 loaded 证据；推进 new_leaves→retire_old 查 new_leaves 的 handshake_verified 证据。isolated 行只有 C 已完成 IAM/workspace/network/broker 实际拒连和断连且耐久记录 isolation_enforced=true 才可从在线集合排除，单纯计划隔离不算。持久旧证据只作审计，离线端重新联网先取最新控制和当前阶段的全局证明，不要求自身先参与旧阶段握手；当前阶段 new_leaves/retire_old 必须同时提供上一步精确 bundle 及其证据，不能把当前包自己的回执解读成前置阶段确认。读者证据不包含私钥。

签名证据的信任边界仍是已接受的“持运行钥的 collab 声明其 PG 台账”，并非根对每个握手亲签。N 的 Trust.SetTransportConfirmation 必须接此已验真证明；无证据继续 projection_pending。

## UID 与材料权限（N/I 冲突的唯一裁决）

edge 的 reader 和专属 projector 都使用 UID/GID 65532；network/broker 的 reader/监督器及各自 projector 都使用10001。双方 SO_PEERCRED 只接受各自组件 UID。每组件独立材料卷和控制卷，均由该 UID 所有，目录0700、材料文件0600、socket0600；projector 读写材料卷，reader 只读材料卷，双方仅在本组件控制卷读写。reader 自有账本另放自己的耐久状态卷。初始化由 D 的安装步骤预建正确 owner；在线 projector cap_drop ALL/no-new-privileges，不 chown、不使用 root。collab10002独占KEK不变。

这是对 K2 “所有 projector10001、edge peer10001、控制卷0710/0660”的明确语义替换。I 改 edge peer 准入；N 改 edge projector 启动/peer 预期；D 改 edge 专属容器UID与卷owner。三者必须同批接线。相同 UID 不区分组件，独立挂载命名空间仍必需；禁止把多个组件材料卷挂给同一 projector。

## 节点确认身份（I 报告）

新增独立 NodeControlPins，绝不扩展 InstallationPins 的投影 component 枚举。NodeControlPin 只含 target_id/node_id/identity_id/Ed25519 signing_spki/identity_revision；无 sealing_spki、gateway 或私钥权限。node.key 保持原 ECDSA 的 NodeControl mTLS 身份，agent 在本地生成单独 Ed25519 确认钥（受保护0600文件/私有ACL）。

D/干净安装管理通路登记公钥：以已认证 NodeControl node_id 和固定 target_id 映射，C/I 只读可信 pin。不存在自注册/上传新公钥即信任的接口；未登记则保持隔离。identity_id、target_id、node_id 在本安装域各唯一，SPKI解析算法与规范DER要求沿 InstallationPins。身份更新必须由独立安装通路提高 identity_revision，撤销旧 identity_id、冻结新挑战并保持隔离，旧身份签名不能授权自己的替代者。

I 先核认证连接 node_id、目标、identity_id/revision 与 pin 全等，再执行既有 control_receipt 的真实 Ed25519 签名/挑战/控制摘要/落盘验证；identity_auth.kid=pin.identity_id。节点仅接收公有 ControlPackage/运输包并签 InstallationReceipt，不得请求任何 ProjectionMaterial/ProtocolMaterial/密封私钥。泄露身份无论确认成功仍隔离；重新纳管须独立登记和新链READY。

## 主动复核所得接缝及各线工作

- C 的待发公有包查询和逐目标隔离查询属于缺生产台账提供者，不能把 enforceIsolation 当查询。新增只读端点给出固定模型；无记录、台账故障、未READY均默认隔离。I 的 target_id 与 IAM设备ID/workspace node_id 映射由可信安装记录提供，禁止接收回执自报映射。C 实现 Access/Nonces 耐久端口；这部分是实现工作，不能用新契约伪称已接通。
- C 的 Java RenewalStatusOutput 生成视图合并两个分支的字段和 state 枚举并仅保留共同必填字段。HTTP 原始 schema 仍严格 oneOf，生成模型是结构超集，边界须验证原 schema；C 可移除临时 JsonValue 包装，getState 可表达 ready/projection_pending。禁止把未发布对象补伪造版本。生成视图不修改受签 schema。
- I 尚未执行的279条生产向量是实现覆盖欠账，不是279条缺契约。原三组全部保留，新增 K3 boundary_cases 同样交 Java/Go 原样消费，Node 参考 adapter 不替代独立生产适配器。TLS根链、混合恢复、迁移、时钟和私钥匹配已有向量；I 必须补生产接线与执行记录。
- N/P 的实际热激活、真实握手/断连、C/I持久提供者、D跨组件卷不可读和NAS正常入口验收仍由各实现线完成。契约向量不代表这些功能已交付。
- web gen:api 仍只生成 web，插件一律 pnpm gen:plugin-network。禁止重新在 gen-api import clients 生成器。K3 不修改公网TLS、NodeControl证书或发行信任域。

只读台账端点：GET /api/collab/v2/network/isolation/{target_id} 返回 IsolationRecord；GET /api/collab/v2/network/controls/pending/{target_id} 返回最早待确认 ControlPackage（无待发返回404）；GET /api/collab/v2/network/transport/history/{digest} 返回 TransportPredecessor。三者均内部 mTLS/JWT、固定安装映射和 no-store，不接受自报目标映射。NodeControl 通过 workspace 后端读取再运输公有包，不把服务 JWT 发给节点。

主动复核补充：长期离线端可能错过首个换授权 bundle。C 必须不可变保存 TransportTransitionProof，通过 GET /transport/transitions/{authority_digest} 读取；其中历史 control 精确锚定 first_bundle，predecessor 验历史签名。先按 transport_transition 完整验证该起点（本地更旧可追平），再独立验当前包签名、安装scope、当前最新控制允许/撤销及控制绑定、generation 与起点一致、bundle_version 不低于起点且不低于本地最高，同版本要求完整摘要相同。当前包不能以历史控制作为允许列表。当前 new_leaves/retire_old 另取其 previous_bundle_digest 对应的历史包与新鲜的前置阶段全局确认；必须分别为 dual_trust/loaded、new_leaves/handshake_verified。当前 dual_trust 只安装双信任，不切新叶子或退休旧CA。历史根锚可在新根控制中不再重复，但历史证明不能豁免最新撤销或降低本地最高代际/版本。以上可直接验证起点和当前两份快照，无需逐日补齐所有中间清单；本地CA墓碑仍拒绝已见旧材料复活。

transport_catchup 在同一共享 JSON 中验证从根锚起点追平到当前包。消费方须覆盖后续版本、同版精确重试、最新根控制回退与撤销、起点及本地最高版本的边界，并验证 new_leaves 和 retire_old 所需的前置阶段确认证据。实现线须同时执行 transport_transition 起点与 transport_catchup 追平用例。

## 首审返工的消费方迁移

运行钥用途从三种扩充到四种，增加 SignedTransportConfirmation，固定签名域为 `Auriga.NetworkTransportConfirmation.v2`。C 的签发白名单和 N/I/P 的验证白名单增加此域，并在签发和验证时检查 `network_transport` 能力。已包含该能力的根授权继续适用，缺该能力时返回 scope_denied，扩权通过离线根重签授权。确认证据沿用同一 collab 台账信任边界。

restart_recovery 必须将未成熟的旧 next 保留原身份与期限并置为 retired 或 revoked。少于24小时的旧 next 不能原样保留或激活，恰满授权成熟期的 next 继续按原激活和按用途保留规则处理。C/N/P 采用新增的成熟期等号及少一秒正负向量。
