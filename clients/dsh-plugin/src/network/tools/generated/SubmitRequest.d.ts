// Generated from contracts/network by test/network/tools/generate.mjs. Do not edit.

/**
 * 插件经 HTTPS 私有接口提交；来源只以 source_binding 或 human_intent_id 引用，总部据此生成 from/sender_kind，并按 to 生成 message_class。
 */
export type SubmitRequest = {
  kind: 'first_contact' | 'message' | 'reply' | 'control';
  /**
   * to 角色：任意合法地址（含 group）。
   */
  to: {
    version: 1;
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    realm: string;
    type: 'system' | 'session' | 'subagent' | 'group';
    /**
     * 无前导零十进制 uint64 字符串，0..18446744073709551615。
     */
    node: string;
    /**
     * 无前导零十进制 uint64 字符串，0..18446744073709551615。
     */
    local: string;
  };
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  thread_id?: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  in_reply_to?: string;
  /**
   * 回应组播/广播请求时平台固化的原消息与原 recipient。
   */
  origin?: {
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    message_id: string;
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    recipient_id: string;
  };
  contact_scope?: 'conversation' | 'human_dialogue' | 'group_join';
  reply_mode?: 'private' | 'group';
  /**
   * 仅 information/request；control 由 kind=control 推出。
   */
  intent?: 'information' | 'request';
  durability: 'durable' | 'ephemeral';
  dispatch_mode?: 'atomic' | 'paged';
  ttl?: number;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  response_deadline?: string;
  /**
   * 受支持的 body 子类型；subtype 必填，control 动作未知时失败关闭。
   */
  body: {
    subtype:
      | 'text'
      | 'progress'
      | 'status'
      | 'artifact_ref'
      | 'conversation_opened'
      | 'execution_waiting'
      | 'group_invitation'
      | 'control';
    /**
     * 正文文本；UTF-8 编码后另受 body 16KiB 字节上限。
     */
    text?: string;
    /**
     * 仅插件确定性回信使用 host；模型产出省略或 model。
     */
    provenance?: 'model' | 'host';
    /**
     * 无前导零十进制 uint64 字符串，0..18446744073709551615。
     */
    revision?: string;
    state?: string;
    artifact?: {
      /**
       * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
       */
      resource_id: string;
      digest: string;
      /**
       * 无前导零十进制 uint64 字符串，0..18446744073709551615。
       */
      size: string;
    };
    /**
     * 总部组空间；local=1 为隐式组织广播，普通组 local≥2。
     */
    group_address?: {
      version: 1;
      /**
       * ≥1 的 uint64 十进制字符串。
       */
      realm: string;
      type: 'system' | 'session' | 'subagent' | 'group';
      /**
       * 无前导零十进制 uint64 字符串，0..18446744073709551615。
       */
      node: string;
      /**
       * 无前导零十进制 uint64 字符串，0..18446744073709551615。
       */
      local: string;
    } & {
      type?: 'group';
      [k: string]: unknown;
    };
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    invitation_id?: string;
    /**
     * UTC 整秒 RFC 3339，唯一表示。
     */
    invite_expires_at?: string;
    action?: 'wake' | 'cancel' | 'queue' | 'steer' | 'start';
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    target_operation_id?: string;
    /**
     * 无前导零十进制 uint64 字符串，0..18446744073709551615。
     */
    expected_state_version?: string;
  };
  /**
   * v1 生产方恒为 []；未知非关键 flag 由消费方保留并忽略，不能改变授权/执行语义。
   *
   * @maxItems 16
   */
  flags?:
    | []
    | [string]
    | [string, string]
    | [string, string, string]
    | [string, string, string, string]
    | [string, string, string, string, string]
    | [string, string, string, string, string, string]
    | [string, string, string, string, string, string, string]
    | [string, string, string, string, string, string, string, string]
    | [string, string, string, string, string, string, string, string, string]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ]
    | [
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
        string,
      ];
  /**
   * 命名空间扩展；e2e_signature 为端到端签名预留，v1 恒为 null，无信任效力。
   */
  ext?: {
    /**
     * 端到端签名预留：v1 生产方恒为 null；消费方对非空值按未知可选扩展保留并忽略，无信任效力。
     */
    e2e_signature?: null | {
      [k: string]: unknown;
    };
    /**
     * This interface was referenced by `undefined`'s JSON-Schema definition
     * via the `patternProperty` "^[a-z][a-z0-9-]{0,31}(?:\.[a-z][a-z0-9_-]{0,31}){1,4}$".
     */
    [k: string]: unknown;
  };
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  correlation_id?: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  causation_id?: string;
  source: (
    | {
        source_binding: unknown;
        [k: string]: unknown;
      }
    | {
        human_intent_id: unknown;
        [k: string]: unknown;
      }
  ) & {
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    source_binding?: string;
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    human_intent_id?: string;
  };
  idempotency: {
    /**
     * ≥1 的 uint64 十进制字符串。
     */
    producer_epoch: string;
    /**
     * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
     */
    key: string;
  };
};
