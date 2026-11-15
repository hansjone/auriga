# K4 selector 台账裁决（2026-11-15）

本批仅补齐 K3 接缝，不宣称 C/N 生产接线或 NAS 已交付。

## 隔离与未完成行

TransportReaderEvidence 只在 phase=isolated 时允许缺省 selector，不允许 null、空对象或伪造当前 bundle selector。loaded/handshake_verified 仍必填完整 selector。所有有 selector 的行（含 isolated）均验证 identity_id=selector.installation_id、精确 bundle 版本/完整受签对象摘要及独立 expected_selectors 摘要。无 selector 的 isolated 行必须匹配 C 独立台账 reader_identities，且 expected_selectors 中该 reader 的值显式为 null；缺 key 不等于 null。isolation_enforced=true 仍只能来自实际拒连/断连后的耐久记录，false 返回 identity_quarantined。冻结集合、身份撤销及签名规则保持不变。

理由：隔离行为不依赖材料是否已授权，不能要求为成功隔离伪造一个 selector；已有材料仍必须精确校验，不能借 isolated 绕过绑定。原 K3 有 selector 的合法隔离行继续合法。

未收齐时 POST /transport/confirmation 返回 HTTP202 TransportConfirmationPending：认证域、精确 bundle/阶段、当前控制、challenge/issued_at 与完整冻结 reader 集合，readers 每个目标恰一行，尚未完成者 phase=pending；已成功加载、握手或实际隔离者按真实阶段填写。pending 行可携带已授权 selector；未知材料缺省，不能编造。该响应不带 runtime_auth，不是 SignedTransportConfirmation，任何202都不能推进阶段。即使未握手行显示 loaded，要求 handshake_verified 时仍待投影；不得将 pending 放入已完成签名模型。无记录404、版本摘要冲突409、台账故障503保持原规则。

理由：完整诊断行便于 C/N 查明阻塞目标，同时保持第四签名域只表示满足查询阶段的完成事实，不扩充已冻结签名用途或成功 phase。

## 独立权威映射

只读 POST /api/collab/v2/network/transport/expected-selectors，输入 ExpectedSelectorsQuery，输出 ExpectedSelectors；C 提供，N 经已有固定 HTTPS+mTLS/JWT、collab.msgnet.read 读取，认证 tenant/realm 必须匹配。POST 只查询，绝不分配 nonce、生成材料或推进台账。

查询绑定完整 SignedTransportTrustBundle 摘要、bundle_version、当前 control_revision 与完整 SignedRootControl 摘要，32字节随机 challenge。响应还绑定 bundle 的 generation、phase、authority_id/完整authority_digest、冻结 required_reader_ids、逐 reader 的 reader_identities 和 expected_selectors。两个映射 key 集必须分别恰好等于冻结集合，无重复、无多余、无遗漏；身份从该周期冻结安装映射读取，不能由证据自报。值为完整 LocalProjectionSelector 的 JCS SHA256（sha256:小写十六进制），null 专指该精确 bundle/冻结身份尚无已授权 selector，不表示隔离已经完成。摘要包含 selector 全部字段，不是只对 manifest 摘要再摘要。

C 从独立材料授权台账一致性快照读取，不从证据行、ProjectionReceipt、N 的本地 loaded 或握手反填。精确 bundle/身份已授权 selector 绑定不可覆盖；同 bundle reader 发生身份替换时，旧周期保持旧身份并实际隔离，新身份待新周期独立纳管。尚未授权的 null 可变成首次授权摘要，因此不允许将旧 null 缓存作永久结论。返回/签证据时控制或材料快照变化须重新查询，不能把两个不同快照拼接。完整证据验证须将映射传入 expected_selectors，并将 reader_identities 传入 expected_identities；所有 selector 缺省行必须比对身份。映射本身不证明 loaded/handshake/isolation。

响应 challenge 精确回显，issued_at 不晚于消费时刻，年龄60秒含等号；控制必须等于 N 已接受的最新根控制，revision相同而摘要不同也拒绝。Cache-Control:no-store；控制变化409刷新控制重试，未知精确 bundle404，台账不可用503，跨域/权限403。N 缺映射或验证失败保持 projection_pending，不能从证据补值。

历史 bundle/授权材料/冻结身份映射不可变保留；消息网络离线、reader不在线或错过阶段不阻止通过独立 HTTPS 管理面读取。这里的“离线可读”不承诺断网时联网读取：完全离线缓存仅供审计，不免除新鲜 challenge、60秒和当前控制检查。历史授权撤销不抹除台账，但该历史映射不授予当前授权或阶段推进权限；全局证明仍按 K3 最新允许/撤销规则独立验证。

理由：独立读取保留 K3 的双输入边界，堵住证据自证材料的循环；复用 C 受认证台账的既有信任边界，不新增运行签名域，不向节点暴露服务 JWT 或私钥。只读历史快照让长期离线端无需先补做所有旧握手。

## 接线与兼容

C 实现新增端点和202完整诊断，N 将 TransportSource.ExpectedSelectors 接到该端点，并先验证快照再校验 SignedTransportConfirmation。现有完成响应200保持 SignedTransportConfirmation，不改运行钥用途、旧签名域、CA阶段门禁。新增模型及允许 isolated 缺 selector 是本任务明确要求的兼容扩展；未引入破坏性字段替换。各线旧严格消费者须更新生成物再启用无 selector 行。生产 Java/Go adapter 需新增 expected_selectors 操作并执行 K4 共享向量，不能把参考 Node 判定器当生产实现。
