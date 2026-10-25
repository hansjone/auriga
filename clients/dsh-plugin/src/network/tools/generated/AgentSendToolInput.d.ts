// Generated from contracts/network by test/network/tools/generate.mjs. Do not edit.

/**
 * 普通 agent 发送工具：仅 target、body、可选 thread。
 */
export interface AgentSendToolInput {
  /**
   * aur1:[realm.]node.local，各段无前导零小写十六进制、1..16 位；段值约束（realm≥1、node=0 仅 group 等）由解析器按 README §2 判定。
   */
  target: string;
  /**
   * 普通发送只接受信息/请求类 body；control 走独立有专权的单播入口。
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
  } & {
    subtype?: 'text' | 'progress' | 'status' | 'artifact_ref';
    provenance?: never;
    [k: string]: unknown;
  };
  /**
   * 平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。
   */
  thread?: string;
}
