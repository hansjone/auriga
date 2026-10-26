// Generated from contracts/network/source-control.schema.json by generate-source-control.mjs. Do not edit.

export type SourceControl =
  | SourceRegister
  | SourceRegisterResult
  | SourceGet
  | SourceGetResult
  | SourceRevoke
  | SourceRevokeResult;
/**
 * issue 要求当前见证；reconcile 只读取 collab 已持久原操作，不新建/改写 IAM 请求。
 */
export type SourceRegister = {
  /**
   * 应用协议版本 major.minor；v1 major 固定 1。
   */
  app_version: string;
  type: 'source_register';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  request_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  epoch: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  boot_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  operation_id: string;
  mode: 'issue' | 'reconcile';
  registration: Registration;
  scope: 'durable_unicast_send';
  /**
   * 无前导零十进制 uint64 字符串，0..18446744073709551615。
   */
  expected_revision: string;
  predecessor_binding: '' | string;
  native_witness: NativeWitness | null;
};
/**
 * 仅原创建回执；不得激活或声明 send_ready。
 */
export type SourceRegisterResult = {
  /**
   * 应用协议版本 major.minor；v1 major 固定 1。
   */
  app_version: string;
  type: 'source_register_result';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  request_id: string;
  server_auth: ServerAuth;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  operation_id: string;
  source: SourceFact | null;
  result: 'registered' | 'not_accepted' | 'not_committed';
  failure_evidence: null | NotAcceptedEvidence | NotCommittedEvidence;
};
/**
 * Current source observation: active requires valid_until; expired/revoked/stopped omit it and grant no permission. Revision is the current named generation revision.
 */
export type SourceGetResult = {
  /**
   * 应用协议版本 major.minor；v1 major 固定 1。
   */
  app_version: string;
  type: 'source_get_result';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  request_id: string;
  server_auth: ServerAuth1;
  source: SourceFact1;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  policy_revision: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  observed_at: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  valid_until?: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  witness_id: string;
};

/**
 * 原 typed-local 六字段；realm/node 仅从认证 ingress 补全为 IAM 八字段，不能由 API projection 编造。
 */
export interface Registration {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  registration_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  allocation_op_id: string;
  allocation_fingerprint: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  registration_revision: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  local: string;
  address_type: 'session';
}
/**
 * 可信插件实际调用 native resolver 后生成；绑定本帧 registration、epoch、boot 与真实当前连接。binding_id 不得为宿主 sessionId。语义及最多 30 秒 freshness 见 README §5.2。
 */
export interface NativeWitness {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  witness_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  binding_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  binding_revision: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  observed_at: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  valid_until: string;
}
/**
 * 总部服务器签名封装；签名输入见 README §4。
 */
export interface ServerAuth {
  version: 1;
  purpose: 'business_delivery' | 'protocol_response' | 'management_state';
  alg: 'Ed25519';
  kid: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  issuer_realm: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  issued_at: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  not_after: string;
  audience: {
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    realm: string;
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    node: string;
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    incarnation: string;
    /**
     * connection_epoch；0 仅用于 READY 被拒时尚无 epoch 的 ERROR。
     */
    epoch: string;
  };
  /**
   * 所响应请求的 request_id；仅 HEAD 等主动提示为 null，此时以 channel_nonce/epoch 关联。
   */
  request_id: string | null;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  channel_nonce: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  keyset_version: string;
  /**
   * 原始 64 字节 Ed25519 签名的规范无填充 base64url。
   */
  signature: string;
}
/**
 * IAM 原事实的受限投影；不含 owner、原兑换连接、宿主标识或 SEND 许可。mutation 为历史回执，只有 Get 为当前观察。
 */
export interface SourceFact {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  source_binding: string;
  registration: Registration1;
  scope: 'durable_unicast_send';
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  revision: string;
  state: 'active' | 'expired' | 'revoked' | 'stopped';
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  not_before: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  expires_at: string;
  predecessor_binding: '' | string;
  native_binding: NativeBinding;
}
/**
 * 原 typed-local 六字段；realm/node 仅从认证 ingress 补全为 IAM 八字段，不能由 API projection 编造。
 */
export interface Registration1 {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  registration_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  allocation_op_id: string;
  allocation_fingerprint: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  registration_revision: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  local: string;
  address_type: 'session';
}
export interface NativeBinding {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  binding_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  binding_revision: string;
}
export interface NotAcceptedEvidence {
  authority: 'collab';
  reason: 'rejected_before_pending';
  request_digest: string;
}
export interface NotCommittedEvidence {
  authority: 'iam';
  reason:
    | 'original_connection_invalid_uncommitted'
    | 'revision_mismatch'
    | 'predecessor_not_head'
    | 'lineage_stopped';
  request_digest: string;
}
export interface SourceGet {
  /**
   * 应用协议版本 major.minor；v1 major 固定 1。
   */
  app_version: string;
  type: 'source_get';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  request_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  epoch: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  boot_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  source_binding: string;
  registration: Registration2;
  native_witness: NativeWitness1;
}
/**
 * 原 typed-local 六字段；realm/node 仅从认证 ingress 补全为 IAM 八字段，不能由 API projection 编造。
 */
export interface Registration2 {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  registration_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  allocation_op_id: string;
  allocation_fingerprint: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  registration_revision: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  local: string;
  address_type: 'session';
}
/**
 * 可信插件实际调用 native resolver 后生成；绑定本帧 registration、epoch、boot 与真实当前连接。binding_id 不得为宿主 sessionId。语义及最多 30 秒 freshness 见 README §5.2。
 */
export interface NativeWitness1 {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  witness_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  binding_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  binding_revision: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  observed_at: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  valid_until: string;
}
/**
 * 总部服务器签名封装；签名输入见 README §4。
 */
export interface ServerAuth1 {
  version: 1;
  purpose: 'business_delivery' | 'protocol_response' | 'management_state';
  alg: 'Ed25519';
  kid: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  issuer_realm: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  issued_at: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  not_after: string;
  audience: {
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    realm: string;
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    node: string;
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    incarnation: string;
    /**
     * connection_epoch；0 仅用于 READY 被拒时尚无 epoch 的 ERROR。
     */
    epoch: string;
  };
  /**
   * 所响应请求的 request_id；仅 HEAD 等主动提示为 null，此时以 channel_nonce/epoch 关联。
   */
  request_id: string | null;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  channel_nonce: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  keyset_version: string;
  /**
   * 原始 64 字节 Ed25519 签名的规范无填充 base64url。
   */
  signature: string;
}
/**
 * IAM 原事实的受限投影；不含 owner、原兑换连接、宿主标识或 SEND 许可。mutation 为历史回执，只有 Get 为当前观察。
 */
export interface SourceFact1 {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  source_binding: string;
  registration: Registration3;
  scope: 'durable_unicast_send';
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  revision: string;
  state: 'active' | 'expired' | 'revoked' | 'stopped';
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  not_before: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  expires_at: string;
  predecessor_binding: '' | string;
  native_binding: NativeBinding1;
}
/**
 * 原 typed-local 六字段；realm/node 仅从认证 ingress 补全为 IAM 八字段，不能由 API projection 编造。
 */
export interface Registration3 {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  registration_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  allocation_op_id: string;
  allocation_fingerprint: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  registration_revision: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  local: string;
  address_type: 'session';
}
export interface NativeBinding1 {
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  binding_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  binding_revision: string;
}
/**
 * 先本地持久停发；无需 native 见证。collab 用原归属锚点及保存的原连接撤销，失效原身份不妨碍既有 outbox。
 */
export interface SourceRevoke {
  /**
   * 应用协议版本 major.minor；v1 major 固定 1。
   */
  app_version: string;
  type: 'source_revoke';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  request_id: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  epoch: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  boot_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  source_binding: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  operation_id: string;
}
/**
 * IAM 已提交终态原回执和许可链停权确认；不是本地 outbox 入队成功。
 */
export interface SourceRevokeResult {
  /**
   * 应用协议版本 major.minor；v1 major 固定 1。
   */
  app_version: string;
  type: 'source_revoke_result';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  request_id: string;
  server_auth: ServerAuth2;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  operation_id: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  source_binding: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  revision: string;
  state: 'revoked' | 'expired';
  lineage_stopped: true;
}
/**
 * 总部服务器签名封装；签名输入见 README §4。
 */
export interface ServerAuth2 {
  version: 1;
  purpose: 'business_delivery' | 'protocol_response' | 'management_state';
  alg: 'Ed25519';
  kid: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  issuer_realm: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  issued_at: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  not_after: string;
  audience: {
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    realm: string;
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    node: string;
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    incarnation: string;
    /**
     * connection_epoch；0 仅用于 READY 被拒时尚无 epoch 的 ERROR。
     */
    epoch: string;
  };
  /**
   * 所响应请求的 request_id；仅 HEAD 等主动提示为 null，此时以 channel_nonce/epoch 关联。
   */
  request_id: string | null;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  channel_nonce: string;
  /**
   * ≥1 的 uint64 十进制字符串。
   */
  keyset_version: string;
  /**
   * 原始 64 字节 Ed25519 签名的规范无填充 base64url。
   */
  signature: string;
}
