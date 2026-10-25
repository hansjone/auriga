// Generated from contracts/network by test/network/tools/generate.mjs. Do not edit.
export const inputSchema = {
  description: '普通 agent 发送工具：仅 target、body、可选 thread。',
  type: 'object',
  properties: {
    target: {
      description:
        'aur1:[realm.]node.local，各段无前导零小写十六进制、1..16 位；段值约束（realm≥1、node=0 仅 group 等）由解析器按 README §2 判定。',
      type: 'string',
      pattern:
        '^aur1:(?:0|[1-9a-f][0-9a-f]{0,15})(?:\\.(?:0|[1-9a-f][0-9a-f]{0,15})){1,2}$',
    },
    body: {
      description:
        '普通发送只接受信息/请求类 body；control 走独立有专权的单播入口。',
      type: 'object',
      allOf: [
        {
          description:
            '受支持的 body 子类型；subtype 必填，control 动作未知时失败关闭。',
          type: 'object',
          properties: {
            subtype: {
              type: 'string',
              enum: [
                'text',
                'progress',
                'status',
                'artifact_ref',
                'conversation_opened',
                'execution_waiting',
                'group_invitation',
                'control',
              ],
            },
            text: {
              description: '正文文本；UTF-8 编码后另受 body 16KiB 字节上限。',
              type: 'string',
              maxLength: 16384,
            },
            provenance: {
              description: '仅插件确定性回信使用 host；模型产出省略或 model。',
              type: 'string',
              enum: ['model', 'host'],
            },
            revision: {
              description:
                '无前导零十进制 uint64 字符串，0..18446744073709551615。',
              type: 'string',
              pattern:
                '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
            },
            state: { type: 'string', pattern: '^[a-z][a-z0-9_]{0,63}$' },
            artifact: {
              type: 'object',
              properties: {
                resource_id: {
                  description:
                    '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
                  type: 'string',
                  pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
                },
                digest: { type: 'string', pattern: '^sha256:[0-9a-f]{64}$' },
                size: {
                  description:
                    '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                  type: 'string',
                  pattern:
                    '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                },
              },
              required: ['resource_id', 'digest', 'size'],
              additionalProperties: false,
            },
            group_address: {
              description:
                '总部组空间；local=1 为隐式组织广播，普通组 local≥2。',
              type: 'object',
              allOf: [
                {
                  description:
                    '完整结构化地址；type 与 node/local 必须一致：system=node≥1,local=0；session/subagent=node≥1,local≥1；group=node=0,local≥1（local=1 为组织广播）。',
                  type: 'object',
                  properties: {
                    version: { type: 'integer', const: 1 },
                    realm: {
                      description: '≥1 的 uint64 十进制字符串。',
                      type: 'string',
                      allOf: [
                        {
                          description:
                            '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                          type: 'string',
                          pattern:
                            '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                        },
                        { not: { const: '0' } },
                      ],
                    },
                    type: {
                      type: 'string',
                      enum: ['system', 'session', 'subagent', 'group'],
                    },
                    node: {
                      description:
                        '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                      type: 'string',
                      pattern:
                        '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                    },
                    local: {
                      description:
                        '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                      type: 'string',
                      pattern:
                        '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                    },
                  },
                  required: ['version', 'realm', 'type', 'node', 'local'],
                  additionalProperties: false,
                  allOf: [
                    {
                      if: { properties: { type: { const: 'system' } } },
                      then: {
                        properties: {
                          node: {
                            description: '≥1 的 uint64 十进制字符串。',
                            type: 'string',
                            allOf: [
                              {
                                description:
                                  '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                                type: 'string',
                                pattern:
                                  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                              },
                              { not: { const: '0' } },
                            ],
                          },
                          local: { const: '0' },
                        },
                      },
                    },
                    {
                      if: {
                        properties: { type: { enum: ['session', 'subagent'] } },
                      },
                      then: {
                        properties: {
                          node: {
                            description: '≥1 的 uint64 十进制字符串。',
                            type: 'string',
                            allOf: [
                              {
                                description:
                                  '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                                type: 'string',
                                pattern:
                                  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                              },
                              { not: { const: '0' } },
                            ],
                          },
                          local: {
                            description: '≥1 的 uint64 十进制字符串。',
                            type: 'string',
                            allOf: [
                              {
                                description:
                                  '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                                type: 'string',
                                pattern:
                                  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                              },
                              { not: { const: '0' } },
                            ],
                          },
                        },
                      },
                    },
                    {
                      if: { properties: { type: { const: 'group' } } },
                      then: {
                        properties: {
                          node: { const: '0' },
                          local: {
                            description: '≥1 的 uint64 十进制字符串。',
                            type: 'string',
                            allOf: [
                              {
                                description:
                                  '无前导零十进制 uint64 字符串，0..18446744073709551615。',
                                type: 'string',
                                pattern:
                                  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                              },
                              { not: { const: '0' } },
                            ],
                          },
                        },
                      },
                    },
                  ],
                },
                { properties: { type: { const: 'group' } } },
              ],
            },
            invitation_id: {
              description:
                '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
              type: 'string',
              pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
            },
            invite_expires_at: {
              description: 'UTC 整秒 RFC 3339，唯一表示。',
              type: 'string',
              format: 'date-time',
              pattern:
                '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
            },
            action: {
              type: 'string',
              enum: ['wake', 'cancel', 'queue', 'steer', 'start'],
            },
            target_operation_id: {
              description:
                '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
              type: 'string',
              pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
            },
            expected_state_version: {
              description:
                '无前导零十进制 uint64 字符串，0..18446744073709551615。',
              type: 'string',
              pattern:
                '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
            },
          },
          required: ['subtype'],
          additionalProperties: false,
          allOf: [
            {
              if: { properties: { subtype: { const: 'text' } } },
              then: { required: ['text'], properties: { text: true } },
            },
            {
              if: { properties: { subtype: { enum: ['progress', 'status'] } } },
              then: {
                required: ['revision', 'state'],
                properties: { revision: true, state: true },
              },
            },
            {
              if: { properties: { subtype: { const: 'artifact_ref' } } },
              then: { required: ['artifact'], properties: { artifact: true } },
            },
            {
              if: {
                properties: {
                  subtype: {
                    enum: ['conversation_opened', 'execution_waiting'],
                  },
                },
              },
              then: {
                required: ['provenance'],
                properties: { provenance: { const: 'host' } },
              },
            },
            {
              if: { properties: { subtype: { const: 'group_invitation' } } },
              then: {
                required: [
                  'group_address',
                  'invitation_id',
                  'invite_expires_at',
                ],
                properties: {
                  group_address: {
                    type: 'object',
                    properties: { local: { not: { const: '1' } } },
                  },
                  text: { type: 'string', maxLength: 512 },
                  invitation_id: true,
                  invite_expires_at: true,
                },
              },
            },
            {
              if: { properties: { subtype: { const: 'control' } } },
              then: { required: ['action'], properties: { action: true } },
              else: {
                properties: {
                  action: false,
                  target_operation_id: false,
                  expected_state_version: false,
                },
              },
            },
          ],
        },
        {
          properties: {
            subtype: { enum: ['text', 'progress', 'status', 'artifact_ref'] },
            provenance: false,
          },
        },
      ],
    },
    thread: {
      description:
        '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
      type: 'string',
      pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
    },
  },
  required: ['target', 'body'],
  additionalProperties: false,
};
export const encoding = {
  maxPayloadBytes: 65536,
  maxBodyBytes: 16384,
  maxJsonDepth: 16,
  maxObjectMembers: 128,
  maxArrayItems: 256,
  maxStringBytes: 16384,
  maxTotalValues: 8192,
  maxExtMembers: 32,
  maxFlags: 16,
  maxDisplayLabelChars: 64,
  maxContactPreviewChars: 280,
  maxInvitationNoteChars: 512,
};
export const opaqueIdPattern = '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$';
