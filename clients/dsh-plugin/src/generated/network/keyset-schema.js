// Generated from contracts/network/keyset.schema.json; do not edit.
export const definitions = {
  PublicKey: {
    type: 'object',
    properties: {
      kid: {
        type: 'string',
        pattern: '^[A-Za-z0-9._-]{1,128}$',
      },
      alg: {
        const: 'Ed25519',
      },
      purpose: {
        enum: ['business_delivery', 'protocol_response', 'management_state'],
      },
      state: {
        enum: ['active', 'next', 'retired', 'revoked'],
        description:
          'active 仅在有效期内签发/验证；next 提前公布，激活前不得用于签发或接受新封装；retired 停止签发，在有效窗口内仍可验旧封装；revoked 立即停签且不得用于接受任何封装。kid/公钥不可复用，retired/revoked 不得重新激活。',
      },
      not_before: {
        description: 'UTC 整秒 RFC 3339，唯一表示。',
        type: 'string',
        format: 'date-time',
        pattern:
          '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
      },
      not_after: {
        description: 'UTC 整秒 RFC 3339，唯一表示。',
        type: 'string',
        format: 'date-time',
        pattern:
          '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
      },
      public_key_spki: {
        type: 'string',
        pattern: '^[A-Za-z0-9_-]{59}$',
        description: 'Ed25519 SPKI DER 的规范无填充 base64url。',
      },
    },
    required: [
      'kid',
      'alg',
      'purpose',
      'state',
      'not_before',
      'not_after',
      'public_key_spki',
    ],
    additionalProperties: false,
  },
  KeysetPayload: {
    type: 'object',
    properties: {
      schema_version: {
        const: 1,
      },
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
          {
            not: {
              const: '0',
            },
          },
        ],
      },
      keyset_version: {
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
          {
            not: {
              const: '0',
            },
          },
        ],
      },
      issued_at: {
        description: 'UTC 整秒 RFC 3339，唯一表示。',
        type: 'string',
        format: 'date-time',
        pattern:
          '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
      },
      valid_until: {
        description: 'UTC 整秒 RFC 3339，唯一表示。',
        type: 'string',
        format: 'date-time',
        pattern:
          '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
      },
      keys: {
        type: 'array',
        items: {
          $ref: '#/definitions/PublicKey',
        },
        maxItems: 128,
      },
    },
    required: [
      'schema_version',
      'realm',
      'keyset_version',
      'issued_at',
      'valid_until',
      'keys',
    ],
    additionalProperties: false,
  },
  SignedKeyset: {
    description:
      '根签名输入为 UTF-8(Auriga.NetworkKeyset.v1) || 0x00 || JCS(keyset)。离线根由发布设施管理，不在 collab 业务签名器中。',
    type: 'object',
    properties: {
      keyset: {
        $ref: '#/definitions/KeysetPayload',
      },
      root_auth: {
        type: 'object',
        properties: {
          alg: {
            const: 'Ed25519',
          },
          kid: {
            type: 'string',
            pattern: '^[A-Za-z0-9._-]{1,128}$',
          },
          signature: {
            description: '原始 64 字节 Ed25519 签名的规范无填充 base64url。',
            type: 'string',
            pattern: '^[A-Za-z0-9_-]{85}[AQgw]$',
          },
        },
        required: ['alg', 'kid', 'signature'],
        additionalProperties: false,
      },
    },
    required: ['keyset', 'root_auth'],
    additionalProperties: false,
  },
};
