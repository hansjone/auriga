// Generated from contracts/network by scripts/gen-network-tools.mjs. Do not edit.

/**
 * 成功响应列出实际完整 from/to；受限首联只给统一“已提交请求”，不返回真实 message/delivery/mail_seq。
 */
export interface SendResult {
  status: 'platform_stored' | 'request_submitted' | 'contact_pending';
  /**
   * from/reply_to 角色：真实 DSH 的 system/session/subagent，禁止 group。
   */
  from: {
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
    type?: 'system' | 'session' | 'subagent';
    [k: string]: unknown;
  };
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
  message_id?: string;
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  thread_id?: string;
  message_class?: 'unicast' | 'multicast' | 'broadcast';
  dispatch_status?: 'pending' | 'dispatched' | 'partial' | 'closed';
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  submission_id?: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  wait_until?: string;
  /**
   * UTC 整秒 RFC 3339，唯一表示。
   */
  expires_at?: string;
}
