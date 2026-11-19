// @ts-nocheck
// Generated from contracts/network by scripts/gen-network-tools.mjs. Do not edit.
/* Inlined Ajv runtime helper license:
The MIT License (MIT)

Copyright (c) 2015-2021 Evgeny Poberezkin

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


*/
'use strict';
export const validate = validate21;
export default validate21;
const schema32 = {
  description:
    '成功响应列出实际完整 from/to；受限首联只给统一“已提交请求”，不返回真实 message/delivery/mail_seq。',
  type: 'object',
  properties: {
    status: {
      type: 'string',
      enum: ['platform_stored', 'request_submitted', 'contact_pending'],
    },
    from: {
      description:
        'from/reply_to 角色：真实 DSH 的 system/session/subagent，禁止 group。',
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
              if: { properties: { type: { enum: ['session', 'subagent'] } } },
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
        { properties: { type: { enum: ['system', 'session', 'subagent'] } } },
      ],
    },
    to: {
      description: 'to 角色：任意合法地址（含 group）。',
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
          if: { properties: { type: { enum: ['session', 'subagent'] } } },
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
    message_id: {
      description:
        '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
      type: 'string',
      pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
    },
    thread_id: {
      description:
        '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
      type: 'string',
      pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
    },
    message_class: {
      type: 'string',
      enum: ['unicast', 'multicast', 'broadcast'],
    },
    dispatch_status: {
      type: 'string',
      enum: ['pending', 'dispatched', 'partial', 'closed'],
    },
    submission_id: {
      description:
        '平台或插件生成的不透明 ID；平台生成的建议 UUIDv7。不承载语义，不可解析。',
      type: 'string',
      pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
    },
    wait_until: {
      description: 'UTC 整秒 RFC 3339，唯一表示。',
      type: 'string',
      format: 'date-time',
      pattern:
        '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
    },
    expires_at: {
      description: 'UTC 整秒 RFC 3339，唯一表示。',
      type: 'string',
      format: 'date-time',
      pattern:
        '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
    },
  },
  required: ['status', 'from', 'to'],
  additionalProperties: false,
  if: { properties: { status: { const: 'request_submitted' } } },
  then: {
    required: ['submission_id', 'wait_until'],
    properties: {
      message_id: false,
      thread_id: false,
      message_class: false,
      dispatch_status: false,
      expires_at: false,
      submission_id: true,
      wait_until: true,
    },
  },
  else: {
    required: ['message_id', 'thread_id', 'message_class', 'expires_at'],
    properties: {
      message_id: true,
      thread_id: true,
      message_class: true,
      expires_at: true,
    },
  },
};
const func0 = Object.prototype.hasOwnProperty;
const pattern5 = new RegExp(
  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
  'u',
);
const pattern7 = new RegExp('^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$', 'u');
const pattern18 = new RegExp(
  '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
  'u',
);
function validate21(
  data,
  {
    instancePath = '',
    parentData,
    parentDataProperty,
    rootData = data,
    dynamicAnchors = {},
  } = {},
) {
  let vErrors = null;
  let errors = 0;
  const evaluated0 = validate21.evaluated;
  if (evaluated0.dynamicProps) {
    evaluated0.props = undefined;
  }
  if (evaluated0.dynamicItems) {
    evaluated0.items = undefined;
  }
  const _errs1 = errors;
  let valid0 = true;
  const _errs2 = errors;
  if (data && typeof data == 'object' && !Array.isArray(data)) {
    if (data.status !== undefined && func0.call(data, 'status')) {
      if ('request_submitted' !== data.status) {
        const err0 = {};
        if (vErrors === null) {
          vErrors = [err0];
        } else {
          vErrors.push(err0);
        }
        errors++;
      }
    }
  }
  var _valid0 = _errs2 === errors;
  errors = _errs1;
  if (vErrors !== null) {
    if (_errs1) {
      vErrors.length = _errs1;
    } else {
      vErrors = null;
    }
  }
  let ifClause0;
  if (_valid0) {
    const _errs4 = errors;
    if (data && typeof data == 'object' && !Array.isArray(data)) {
      let missing0;
      if (
        ((data.submission_id === undefined ||
          !func0.call(data, 'submission_id')) &&
          (missing0 = 'submission_id')) ||
        ((data.wait_until === undefined || !func0.call(data, 'wait_until')) &&
          (missing0 = 'wait_until'))
      ) {
        validate21.errors = [
          {
            instancePath,
            schemaPath: '#/then/required',
            keyword: 'required',
            params: { missingProperty: missing0 },
          },
        ];
        return false;
      } else {
        if (data.message_id !== undefined && func0.call(data, 'message_id')) {
          var valid2 = false;
          validate21.errors = [
            {
              instancePath: instancePath + '/message_id',
              schemaPath: '#/then/properties/message_id/false schema',
              keyword: 'false schema',
              params: {},
            },
          ];
          return false;
        } else {
          var valid2 = true;
        }
        if (valid2) {
          if (data.thread_id !== undefined && func0.call(data, 'thread_id')) {
            var valid2 = false;
            validate21.errors = [
              {
                instancePath: instancePath + '/thread_id',
                schemaPath: '#/then/properties/thread_id/false schema',
                keyword: 'false schema',
                params: {},
              },
            ];
            return false;
          } else {
            var valid2 = true;
          }
          if (valid2) {
            if (
              data.message_class !== undefined &&
              func0.call(data, 'message_class')
            ) {
              var valid2 = false;
              validate21.errors = [
                {
                  instancePath: instancePath + '/message_class',
                  schemaPath: '#/then/properties/message_class/false schema',
                  keyword: 'false schema',
                  params: {},
                },
              ];
              return false;
            } else {
              var valid2 = true;
            }
            if (valid2) {
              if (
                data.dispatch_status !== undefined &&
                func0.call(data, 'dispatch_status')
              ) {
                var valid2 = false;
                validate21.errors = [
                  {
                    instancePath: instancePath + '/dispatch_status',
                    schemaPath:
                      '#/then/properties/dispatch_status/false schema',
                    keyword: 'false schema',
                    params: {},
                  },
                ];
                return false;
              } else {
                var valid2 = true;
              }
              if (valid2) {
                if (
                  data.expires_at !== undefined &&
                  func0.call(data, 'expires_at')
                ) {
                  var valid2 = false;
                  validate21.errors = [
                    {
                      instancePath: instancePath + '/expires_at',
                      schemaPath: '#/then/properties/expires_at/false schema',
                      keyword: 'false schema',
                      params: {},
                    },
                  ];
                  return false;
                } else {
                  var valid2 = true;
                }
              }
            }
          }
        }
      }
    }
    var _valid0 = _errs4 === errors;
    valid0 = _valid0;
    if (valid0) {
      var props0 = {};
      props0.message_id = true;
      props0.thread_id = true;
      props0.message_class = true;
      props0.dispatch_status = true;
      props0.expires_at = true;
      props0.submission_id = true;
      props0.wait_until = true;
      props0.status = true;
    }
    ifClause0 = 'then';
  } else {
    const _errs5 = errors;
    if (data && typeof data == 'object' && !Array.isArray(data)) {
      let missing1;
      if (
        ((data.message_id === undefined || !func0.call(data, 'message_id')) &&
          (missing1 = 'message_id')) ||
        ((data.thread_id === undefined || !func0.call(data, 'thread_id')) &&
          (missing1 = 'thread_id')) ||
        ((data.message_class === undefined ||
          !func0.call(data, 'message_class')) &&
          (missing1 = 'message_class')) ||
        ((data.expires_at === undefined || !func0.call(data, 'expires_at')) &&
          (missing1 = 'expires_at'))
      ) {
        validate21.errors = [
          {
            instancePath,
            schemaPath: '#/else/required',
            keyword: 'required',
            params: { missingProperty: missing1 },
          },
        ];
        return false;
      }
    }
    var _valid0 = _errs5 === errors;
    valid0 = _valid0;
    if (valid0) {
      if (props0 !== true) {
        props0 = props0 || {};
        props0.message_id = true;
        props0.thread_id = true;
        props0.message_class = true;
        props0.expires_at = true;
      }
    }
    ifClause0 = 'else';
  }
  if (!valid0) {
    const err1 = {
      instancePath,
      schemaPath: '#/if',
      keyword: 'if',
      params: { failingKeyword: ifClause0 },
    };
    if (vErrors === null) {
      vErrors = [err1];
    } else {
      vErrors.push(err1);
    }
    errors++;
    validate21.errors = vErrors;
    return false;
  }
  if (errors === 0) {
    if (data && typeof data == 'object' && !Array.isArray(data)) {
      let missing2;
      if (
        ((data.status === undefined || !func0.call(data, 'status')) &&
          (missing2 = 'status')) ||
        ((data.from === undefined || !func0.call(data, 'from')) &&
          (missing2 = 'from')) ||
        ((data.to === undefined || !func0.call(data, 'to')) &&
          (missing2 = 'to'))
      ) {
        validate21.errors = [
          {
            instancePath,
            schemaPath: '#/required',
            keyword: 'required',
            params: { missingProperty: missing2 },
          },
        ];
        return false;
      } else {
        const _errs6 = errors;
        for (const key0 of Object.keys(data)) {
          if (!func0.call(schema32.properties, key0)) {
            validate21.errors = [
              {
                instancePath,
                schemaPath: '#/additionalProperties',
                keyword: 'additionalProperties',
                params: { additionalProperty: key0 },
              },
            ];
            return false;
            break;
          }
        }
        if (_errs6 === errors) {
          if (data.status !== undefined && func0.call(data, 'status')) {
            let data6 = data.status;
            const _errs7 = errors;
            if (typeof data6 !== 'string') {
              validate21.errors = [
                {
                  instancePath: instancePath + '/status',
                  schemaPath: '#/properties/status/type',
                  keyword: 'type',
                  params: { type: 'string' },
                },
              ];
              return false;
            }
            if (!(
              data6 === 'platform_stored' ||
              data6 === 'request_submitted' ||
              data6 === 'contact_pending'
            )) {
              validate21.errors = [
                {
                  instancePath: instancePath + '/status',
                  schemaPath: '#/properties/status/enum',
                  keyword: 'enum',
                  params: { allowedValues: schema32.properties.status.enum },
                },
              ];
              return false;
            }
            var valid3 = _errs7 === errors;
          } else {
            var valid3 = true;
          }
          if (valid3) {
            if (data.from !== undefined && func0.call(data, 'from')) {
              let data7 = data.from;
              const _errs9 = errors;
              if (!(
                data7 &&
                typeof data7 == 'object' &&
                !Array.isArray(data7)
              )) {
                validate21.errors = [
                  {
                    instancePath: instancePath + '/from',
                    schemaPath: '#/properties/from/type',
                    keyword: 'type',
                    params: { type: 'object' },
                  },
                ];
                return false;
              }
              const _errs11 = errors;
              const _errs13 = errors;
              const _errs14 = errors;
              let valid6 = true;
              const _errs15 = errors;
              if (data7 && typeof data7 == 'object' && !Array.isArray(data7)) {
                if (data7.type !== undefined && func0.call(data7, 'type')) {
                  if ('system' !== data7.type) {
                    const err2 = {};
                    if (vErrors === null) {
                      vErrors = [err2];
                    } else {
                      vErrors.push(err2);
                    }
                    errors++;
                  }
                }
              }
              var _valid1 = _errs15 === errors;
              errors = _errs14;
              if (vErrors !== null) {
                if (_errs14) {
                  vErrors.length = _errs14;
                } else {
                  vErrors = null;
                }
              }
              if (_valid1) {
                const _errs17 = errors;
                if (
                  data7 &&
                  typeof data7 == 'object' &&
                  !Array.isArray(data7)
                ) {
                  if (data7.node !== undefined && func0.call(data7, 'node')) {
                    let data9 = data7.node;
                    const _errs18 = errors;
                    if (typeof data9 !== 'string') {
                      validate21.errors = [
                        {
                          instancePath: instancePath + '/from/node',
                          schemaPath:
                            '#/properties/from/allOf/0/allOf/0/then/properties/node/type',
                          keyword: 'type',
                          params: { type: 'string' },
                        },
                      ];
                      return false;
                    }
                    const _errs20 = errors;
                    if (errors === _errs20) {
                      if (typeof data9 === 'string') {
                        if (!pattern5.test(data9)) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/node',
                              schemaPath:
                                '#/properties/from/allOf/0/allOf/0/then/properties/node/allOf/0/pattern',
                              keyword: 'pattern',
                              params: {
                                pattern:
                                  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                              },
                            },
                          ];
                          return false;
                        }
                      } else {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/from/node',
                            schemaPath:
                              '#/properties/from/allOf/0/allOf/0/then/properties/node/allOf/0/type',
                            keyword: 'type',
                            params: { type: 'string' },
                          },
                        ];
                        return false;
                      }
                    }
                    var valid9 = _errs20 === errors;
                    if (valid9) {
                      const _errs22 = errors;
                      const _errs23 = errors;
                      const _errs24 = errors;
                      if ('0' !== data9) {
                        const err3 = {};
                        if (vErrors === null) {
                          vErrors = [err3];
                        } else {
                          vErrors.push(err3);
                        }
                        errors++;
                      }
                      var valid10 = _errs24 === errors;
                      if (valid10) {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/from/node',
                            schemaPath:
                              '#/properties/from/allOf/0/allOf/0/then/properties/node/allOf/1/not',
                            keyword: 'not',
                            params: {},
                          },
                        ];
                        return false;
                      } else {
                        errors = _errs23;
                        if (vErrors !== null) {
                          if (_errs23) {
                            vErrors.length = _errs23;
                          } else {
                            vErrors = null;
                          }
                        }
                      }
                      var valid9 = _errs22 === errors;
                    }
                    var valid8 = _errs18 === errors;
                  } else {
                    var valid8 = true;
                  }
                  if (valid8) {
                    if (
                      data7.local !== undefined &&
                      func0.call(data7, 'local')
                    ) {
                      const _errs25 = errors;
                      if ('0' !== data7.local) {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/from/local',
                            schemaPath:
                              '#/properties/from/allOf/0/allOf/0/then/properties/local/const',
                            keyword: 'const',
                            params: { allowedValue: '0' },
                          },
                        ];
                        return false;
                      }
                      var valid8 = _errs25 === errors;
                    } else {
                      var valid8 = true;
                    }
                  }
                }
                var _valid1 = _errs17 === errors;
                valid6 = _valid1;
                if (valid6) {
                  var props1 = {};
                  props1.node = true;
                  props1.local = true;
                  props1.type = true;
                }
              }
              if (!valid6) {
                const err4 = {
                  instancePath: instancePath + '/from',
                  schemaPath: '#/properties/from/allOf/0/allOf/0/if',
                  keyword: 'if',
                  params: { failingKeyword: 'then' },
                };
                if (vErrors === null) {
                  vErrors = [err4];
                } else {
                  vErrors.push(err4);
                }
                errors++;
                validate21.errors = vErrors;
                return false;
              }
              var valid5 = _errs13 === errors;
              if (valid5) {
                const _errs26 = errors;
                const _errs27 = errors;
                let valid11 = true;
                const _errs28 = errors;
                if (
                  data7 &&
                  typeof data7 == 'object' &&
                  !Array.isArray(data7)
                ) {
                  if (data7.type !== undefined && func0.call(data7, 'type')) {
                    let data11 = data7.type;
                    if (!(data11 === 'session' || data11 === 'subagent')) {
                      const err5 = {};
                      if (vErrors === null) {
                        vErrors = [err5];
                      } else {
                        vErrors.push(err5);
                      }
                      errors++;
                    }
                  }
                }
                var _valid2 = _errs28 === errors;
                errors = _errs27;
                if (vErrors !== null) {
                  if (_errs27) {
                    vErrors.length = _errs27;
                  } else {
                    vErrors = null;
                  }
                }
                if (_valid2) {
                  const _errs30 = errors;
                  if (
                    data7 &&
                    typeof data7 == 'object' &&
                    !Array.isArray(data7)
                  ) {
                    if (data7.node !== undefined && func0.call(data7, 'node')) {
                      let data12 = data7.node;
                      const _errs31 = errors;
                      if (typeof data12 !== 'string') {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/from/node',
                            schemaPath:
                              '#/properties/from/allOf/0/allOf/1/then/properties/node/type',
                            keyword: 'type',
                            params: { type: 'string' },
                          },
                        ];
                        return false;
                      }
                      const _errs33 = errors;
                      if (errors === _errs33) {
                        if (typeof data12 === 'string') {
                          if (!pattern5.test(data12)) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/from/node',
                                schemaPath:
                                  '#/properties/from/allOf/0/allOf/1/then/properties/node/allOf/0/pattern',
                                keyword: 'pattern',
                                params: {
                                  pattern:
                                    '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                },
                              },
                            ];
                            return false;
                          }
                        } else {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/node',
                              schemaPath:
                                '#/properties/from/allOf/0/allOf/1/then/properties/node/allOf/0/type',
                              keyword: 'type',
                              params: { type: 'string' },
                            },
                          ];
                          return false;
                        }
                      }
                      var valid14 = _errs33 === errors;
                      if (valid14) {
                        const _errs35 = errors;
                        const _errs36 = errors;
                        const _errs37 = errors;
                        if ('0' !== data12) {
                          const err6 = {};
                          if (vErrors === null) {
                            vErrors = [err6];
                          } else {
                            vErrors.push(err6);
                          }
                          errors++;
                        }
                        var valid15 = _errs37 === errors;
                        if (valid15) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/node',
                              schemaPath:
                                '#/properties/from/allOf/0/allOf/1/then/properties/node/allOf/1/not',
                              keyword: 'not',
                              params: {},
                            },
                          ];
                          return false;
                        } else {
                          errors = _errs36;
                          if (vErrors !== null) {
                            if (_errs36) {
                              vErrors.length = _errs36;
                            } else {
                              vErrors = null;
                            }
                          }
                        }
                        var valid14 = _errs35 === errors;
                      }
                      var valid13 = _errs31 === errors;
                    } else {
                      var valid13 = true;
                    }
                    if (valid13) {
                      if (
                        data7.local !== undefined &&
                        func0.call(data7, 'local')
                      ) {
                        let data13 = data7.local;
                        const _errs38 = errors;
                        if (typeof data13 !== 'string') {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/local',
                              schemaPath:
                                '#/properties/from/allOf/0/allOf/1/then/properties/local/type',
                              keyword: 'type',
                              params: { type: 'string' },
                            },
                          ];
                          return false;
                        }
                        const _errs40 = errors;
                        if (errors === _errs40) {
                          if (typeof data13 === 'string') {
                            if (!pattern5.test(data13)) {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/local',
                                  schemaPath:
                                    '#/properties/from/allOf/0/allOf/1/then/properties/local/allOf/0/pattern',
                                  keyword: 'pattern',
                                  params: {
                                    pattern:
                                      '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                  },
                                },
                              ];
                              return false;
                            }
                          } else {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/from/local',
                                schemaPath:
                                  '#/properties/from/allOf/0/allOf/1/then/properties/local/allOf/0/type',
                                keyword: 'type',
                                params: { type: 'string' },
                              },
                            ];
                            return false;
                          }
                        }
                        var valid16 = _errs40 === errors;
                        if (valid16) {
                          const _errs42 = errors;
                          const _errs43 = errors;
                          const _errs44 = errors;
                          if ('0' !== data13) {
                            const err7 = {};
                            if (vErrors === null) {
                              vErrors = [err7];
                            } else {
                              vErrors.push(err7);
                            }
                            errors++;
                          }
                          var valid17 = _errs44 === errors;
                          if (valid17) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/from/local',
                                schemaPath:
                                  '#/properties/from/allOf/0/allOf/1/then/properties/local/allOf/1/not',
                                keyword: 'not',
                                params: {},
                              },
                            ];
                            return false;
                          } else {
                            errors = _errs43;
                            if (vErrors !== null) {
                              if (_errs43) {
                                vErrors.length = _errs43;
                              } else {
                                vErrors = null;
                              }
                            }
                          }
                          var valid16 = _errs42 === errors;
                        }
                        var valid13 = _errs38 === errors;
                      } else {
                        var valid13 = true;
                      }
                    }
                  }
                  var _valid2 = _errs30 === errors;
                  valid11 = _valid2;
                  if (valid11) {
                    var props2 = {};
                    props2.node = true;
                    props2.local = true;
                    props2.type = true;
                  }
                }
                if (!valid11) {
                  const err8 = {
                    instancePath: instancePath + '/from',
                    schemaPath: '#/properties/from/allOf/0/allOf/1/if',
                    keyword: 'if',
                    params: { failingKeyword: 'then' },
                  };
                  if (vErrors === null) {
                    vErrors = [err8];
                  } else {
                    vErrors.push(err8);
                  }
                  errors++;
                  validate21.errors = vErrors;
                  return false;
                }
                var valid5 = _errs26 === errors;
                if (valid5) {
                  if (props1 !== true && props2 !== undefined) {
                    if (props2 === true) {
                      props1 = true;
                    } else {
                      props1 = props1 || {};
                      Object.assign(props1, props2);
                    }
                  }
                  const _errs45 = errors;
                  const _errs46 = errors;
                  let valid18 = true;
                  const _errs47 = errors;
                  if (
                    data7 &&
                    typeof data7 == 'object' &&
                    !Array.isArray(data7)
                  ) {
                    if (data7.type !== undefined && func0.call(data7, 'type')) {
                      if ('group' !== data7.type) {
                        const err9 = {};
                        if (vErrors === null) {
                          vErrors = [err9];
                        } else {
                          vErrors.push(err9);
                        }
                        errors++;
                      }
                    }
                  }
                  var _valid3 = _errs47 === errors;
                  errors = _errs46;
                  if (vErrors !== null) {
                    if (_errs46) {
                      vErrors.length = _errs46;
                    } else {
                      vErrors = null;
                    }
                  }
                  if (_valid3) {
                    const _errs49 = errors;
                    if (
                      data7 &&
                      typeof data7 == 'object' &&
                      !Array.isArray(data7)
                    ) {
                      if (
                        data7.node !== undefined &&
                        func0.call(data7, 'node')
                      ) {
                        const _errs50 = errors;
                        if ('0' !== data7.node) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/node',
                              schemaPath:
                                '#/properties/from/allOf/0/allOf/2/then/properties/node/const',
                              keyword: 'const',
                              params: { allowedValue: '0' },
                            },
                          ];
                          return false;
                        }
                        var valid20 = _errs50 === errors;
                      } else {
                        var valid20 = true;
                      }
                      if (valid20) {
                        if (
                          data7.local !== undefined &&
                          func0.call(data7, 'local')
                        ) {
                          let data16 = data7.local;
                          const _errs51 = errors;
                          if (typeof data16 !== 'string') {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/from/local',
                                schemaPath:
                                  '#/properties/from/allOf/0/allOf/2/then/properties/local/type',
                                keyword: 'type',
                                params: { type: 'string' },
                              },
                            ];
                            return false;
                          }
                          const _errs53 = errors;
                          if (errors === _errs53) {
                            if (typeof data16 === 'string') {
                              if (!pattern5.test(data16)) {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/from/local',
                                    schemaPath:
                                      '#/properties/from/allOf/0/allOf/2/then/properties/local/allOf/0/pattern',
                                    keyword: 'pattern',
                                    params: {
                                      pattern:
                                        '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                    },
                                  },
                                ];
                                return false;
                              }
                            } else {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/local',
                                  schemaPath:
                                    '#/properties/from/allOf/0/allOf/2/then/properties/local/allOf/0/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                          }
                          var valid21 = _errs53 === errors;
                          if (valid21) {
                            const _errs55 = errors;
                            const _errs56 = errors;
                            const _errs57 = errors;
                            if ('0' !== data16) {
                              const err10 = {};
                              if (vErrors === null) {
                                vErrors = [err10];
                              } else {
                                vErrors.push(err10);
                              }
                              errors++;
                            }
                            var valid22 = _errs57 === errors;
                            if (valid22) {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/local',
                                  schemaPath:
                                    '#/properties/from/allOf/0/allOf/2/then/properties/local/allOf/1/not',
                                  keyword: 'not',
                                  params: {},
                                },
                              ];
                              return false;
                            } else {
                              errors = _errs56;
                              if (vErrors !== null) {
                                if (_errs56) {
                                  vErrors.length = _errs56;
                                } else {
                                  vErrors = null;
                                }
                              }
                            }
                            var valid21 = _errs55 === errors;
                          }
                          var valid20 = _errs51 === errors;
                        } else {
                          var valid20 = true;
                        }
                      }
                    }
                    var _valid3 = _errs49 === errors;
                    valid18 = _valid3;
                    if (valid18) {
                      var props3 = {};
                      props3.node = true;
                      props3.local = true;
                      props3.type = true;
                    }
                  }
                  if (!valid18) {
                    const err11 = {
                      instancePath: instancePath + '/from',
                      schemaPath: '#/properties/from/allOf/0/allOf/2/if',
                      keyword: 'if',
                      params: { failingKeyword: 'then' },
                    };
                    if (vErrors === null) {
                      vErrors = [err11];
                    } else {
                      vErrors.push(err11);
                    }
                    errors++;
                    validate21.errors = vErrors;
                    return false;
                  }
                  var valid5 = _errs45 === errors;
                  if (valid5) {
                    if (props1 !== true && props3 !== undefined) {
                      if (props3 === true) {
                        props1 = true;
                      } else {
                        props1 = props1 || {};
                        Object.assign(props1, props3);
                      }
                    }
                  }
                }
              }
              if (errors === _errs11) {
                if (
                  data7 &&
                  typeof data7 == 'object' &&
                  !Array.isArray(data7)
                ) {
                  let missing3;
                  if (
                    ((data7.version === undefined ||
                      !func0.call(data7, 'version')) &&
                      (missing3 = 'version')) ||
                    ((data7.realm === undefined ||
                      !func0.call(data7, 'realm')) &&
                      (missing3 = 'realm')) ||
                    ((data7.type === undefined || !func0.call(data7, 'type')) &&
                      (missing3 = 'type')) ||
                    ((data7.node === undefined || !func0.call(data7, 'node')) &&
                      (missing3 = 'node')) ||
                    ((data7.local === undefined ||
                      !func0.call(data7, 'local')) &&
                      (missing3 = 'local'))
                  ) {
                    validate21.errors = [
                      {
                        instancePath: instancePath + '/from',
                        schemaPath: '#/properties/from/allOf/0/required',
                        keyword: 'required',
                        params: { missingProperty: missing3 },
                      },
                    ];
                    return false;
                  } else {
                    const _errs58 = errors;
                    for (const key1 of Object.keys(data7)) {
                      if (!(
                        key1 === 'version' ||
                        key1 === 'realm' ||
                        key1 === 'type' ||
                        key1 === 'node' ||
                        key1 === 'local'
                      )) {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/from',
                            schemaPath:
                              '#/properties/from/allOf/0/additionalProperties',
                            keyword: 'additionalProperties',
                            params: { additionalProperty: key1 },
                          },
                        ];
                        return false;
                        break;
                      }
                    }
                    if (_errs58 === errors) {
                      if (
                        data7.version !== undefined &&
                        func0.call(data7, 'version')
                      ) {
                        let data17 = data7.version;
                        const _errs59 = errors;
                        if (!(
                          typeof data17 == 'number' &&
                          !(data17 % 1) &&
                          !isNaN(data17)
                        )) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/version',
                              schemaPath:
                                '#/properties/from/allOf/0/properties/version/type',
                              keyword: 'type',
                              params: { type: 'integer' },
                            },
                          ];
                          return false;
                        }
                        if (1 !== data17) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/from/version',
                              schemaPath:
                                '#/properties/from/allOf/0/properties/version/const',
                              keyword: 'const',
                              params: { allowedValue: 1 },
                            },
                          ];
                          return false;
                        }
                        var valid23 = _errs59 === errors;
                      } else {
                        var valid23 = true;
                      }
                      if (valid23) {
                        if (
                          data7.realm !== undefined &&
                          func0.call(data7, 'realm')
                        ) {
                          let data18 = data7.realm;
                          const _errs61 = errors;
                          if (typeof data18 !== 'string') {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/from/realm',
                                schemaPath:
                                  '#/properties/from/allOf/0/properties/realm/type',
                                keyword: 'type',
                                params: { type: 'string' },
                              },
                            ];
                            return false;
                          }
                          const _errs63 = errors;
                          if (errors === _errs63) {
                            if (typeof data18 === 'string') {
                              if (!pattern5.test(data18)) {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/from/realm',
                                    schemaPath:
                                      '#/properties/from/allOf/0/properties/realm/allOf/0/pattern',
                                    keyword: 'pattern',
                                    params: {
                                      pattern:
                                        '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                    },
                                  },
                                ];
                                return false;
                              }
                            } else {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/realm',
                                  schemaPath:
                                    '#/properties/from/allOf/0/properties/realm/allOf/0/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                          }
                          var valid24 = _errs63 === errors;
                          if (valid24) {
                            const _errs65 = errors;
                            const _errs66 = errors;
                            const _errs67 = errors;
                            if ('0' !== data18) {
                              const err12 = {};
                              if (vErrors === null) {
                                vErrors = [err12];
                              } else {
                                vErrors.push(err12);
                              }
                              errors++;
                            }
                            var valid25 = _errs67 === errors;
                            if (valid25) {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/realm',
                                  schemaPath:
                                    '#/properties/from/allOf/0/properties/realm/allOf/1/not',
                                  keyword: 'not',
                                  params: {},
                                },
                              ];
                              return false;
                            } else {
                              errors = _errs66;
                              if (vErrors !== null) {
                                if (_errs66) {
                                  vErrors.length = _errs66;
                                } else {
                                  vErrors = null;
                                }
                              }
                            }
                            var valid24 = _errs65 === errors;
                          }
                          var valid23 = _errs61 === errors;
                        } else {
                          var valid23 = true;
                        }
                        if (valid23) {
                          if (
                            data7.type !== undefined &&
                            func0.call(data7, 'type')
                          ) {
                            let data19 = data7.type;
                            const _errs68 = errors;
                            if (typeof data19 !== 'string') {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/type',
                                  schemaPath:
                                    '#/properties/from/allOf/0/properties/type/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                            if (!(
                              data19 === 'system' ||
                              data19 === 'session' ||
                              data19 === 'subagent' ||
                              data19 === 'group'
                            )) {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/from/type',
                                  schemaPath:
                                    '#/properties/from/allOf/0/properties/type/enum',
                                  keyword: 'enum',
                                  params: {
                                    allowedValues:
                                      schema32.properties.from.allOf[0]
                                        .properties.type.enum,
                                  },
                                },
                              ];
                              return false;
                            }
                            var valid23 = _errs68 === errors;
                          } else {
                            var valid23 = true;
                          }
                          if (valid23) {
                            if (
                              data7.node !== undefined &&
                              func0.call(data7, 'node')
                            ) {
                              let data20 = data7.node;
                              const _errs70 = errors;
                              if (errors === _errs70) {
                                if (typeof data20 === 'string') {
                                  if (!pattern5.test(data20)) {
                                    validate21.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/from/node',
                                        schemaPath:
                                          '#/properties/from/allOf/0/properties/node/pattern',
                                        keyword: 'pattern',
                                        params: {
                                          pattern:
                                            '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                        },
                                      },
                                    ];
                                    return false;
                                  }
                                } else {
                                  validate21.errors = [
                                    {
                                      instancePath: instancePath + '/from/node',
                                      schemaPath:
                                        '#/properties/from/allOf/0/properties/node/type',
                                      keyword: 'type',
                                      params: { type: 'string' },
                                    },
                                  ];
                                  return false;
                                }
                              }
                              var valid23 = _errs70 === errors;
                            } else {
                              var valid23 = true;
                            }
                            if (valid23) {
                              if (
                                data7.local !== undefined &&
                                func0.call(data7, 'local')
                              ) {
                                let data21 = data7.local;
                                const _errs72 = errors;
                                if (errors === _errs72) {
                                  if (typeof data21 === 'string') {
                                    if (!pattern5.test(data21)) {
                                      validate21.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/from/local',
                                          schemaPath:
                                            '#/properties/from/allOf/0/properties/local/pattern',
                                          keyword: 'pattern',
                                          params: {
                                            pattern:
                                              '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                          },
                                        },
                                      ];
                                      return false;
                                    }
                                  } else {
                                    validate21.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/from/local',
                                        schemaPath:
                                          '#/properties/from/allOf/0/properties/local/type',
                                        keyword: 'type',
                                        params: { type: 'string' },
                                      },
                                    ];
                                    return false;
                                  }
                                }
                                var valid23 = _errs72 === errors;
                              } else {
                                var valid23 = true;
                              }
                            }
                          }
                        }
                      }
                    }
                  }
                } else {
                  validate21.errors = [
                    {
                      instancePath: instancePath + '/from',
                      schemaPath: '#/properties/from/allOf/0/type',
                      keyword: 'type',
                      params: { type: 'object' },
                    },
                  ];
                  return false;
                }
              }
              var valid4 = _errs11 === errors;
              if (valid4) {
                const _errs74 = errors;
                if (
                  data7 &&
                  typeof data7 == 'object' &&
                  !Array.isArray(data7)
                ) {
                  if (data7.type !== undefined && func0.call(data7, 'type')) {
                    let data22 = data7.type;
                    if (!(
                      data22 === 'system' ||
                      data22 === 'session' ||
                      data22 === 'subagent'
                    )) {
                      validate21.errors = [
                        {
                          instancePath: instancePath + '/from/type',
                          schemaPath:
                            '#/properties/from/allOf/1/properties/type/enum',
                          keyword: 'enum',
                          params: {
                            allowedValues:
                              schema32.properties.from.allOf[1].properties.type
                                .enum,
                          },
                        },
                      ];
                      return false;
                    }
                  }
                }
                var valid4 = _errs74 === errors;
              }
              var valid3 = _errs9 === errors;
            } else {
              var valid3 = true;
            }
            if (valid3) {
              if (data.to !== undefined && func0.call(data, 'to')) {
                let data23 = data.to;
                const _errs76 = errors;
                const _errs78 = errors;
                const _errs79 = errors;
                let valid28 = true;
                const _errs80 = errors;
                if (
                  data23 &&
                  typeof data23 == 'object' &&
                  !Array.isArray(data23)
                ) {
                  if (data23.type !== undefined && func0.call(data23, 'type')) {
                    if ('system' !== data23.type) {
                      const err13 = {};
                      if (vErrors === null) {
                        vErrors = [err13];
                      } else {
                        vErrors.push(err13);
                      }
                      errors++;
                    }
                  }
                }
                var _valid4 = _errs80 === errors;
                errors = _errs79;
                if (vErrors !== null) {
                  if (_errs79) {
                    vErrors.length = _errs79;
                  } else {
                    vErrors = null;
                  }
                }
                if (_valid4) {
                  const _errs82 = errors;
                  if (
                    data23 &&
                    typeof data23 == 'object' &&
                    !Array.isArray(data23)
                  ) {
                    if (
                      data23.node !== undefined &&
                      func0.call(data23, 'node')
                    ) {
                      let data25 = data23.node;
                      const _errs83 = errors;
                      if (typeof data25 !== 'string') {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/to/node',
                            schemaPath:
                              '#/properties/to/allOf/0/then/properties/node/type',
                            keyword: 'type',
                            params: { type: 'string' },
                          },
                        ];
                        return false;
                      }
                      const _errs85 = errors;
                      if (errors === _errs85) {
                        if (typeof data25 === 'string') {
                          if (!pattern5.test(data25)) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/node',
                                schemaPath:
                                  '#/properties/to/allOf/0/then/properties/node/allOf/0/pattern',
                                keyword: 'pattern',
                                params: {
                                  pattern:
                                    '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                },
                              },
                            ];
                            return false;
                          }
                        } else {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/to/node',
                              schemaPath:
                                '#/properties/to/allOf/0/then/properties/node/allOf/0/type',
                              keyword: 'type',
                              params: { type: 'string' },
                            },
                          ];
                          return false;
                        }
                      }
                      var valid31 = _errs85 === errors;
                      if (valid31) {
                        const _errs87 = errors;
                        const _errs88 = errors;
                        const _errs89 = errors;
                        if ('0' !== data25) {
                          const err14 = {};
                          if (vErrors === null) {
                            vErrors = [err14];
                          } else {
                            vErrors.push(err14);
                          }
                          errors++;
                        }
                        var valid32 = _errs89 === errors;
                        if (valid32) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/to/node',
                              schemaPath:
                                '#/properties/to/allOf/0/then/properties/node/allOf/1/not',
                              keyword: 'not',
                              params: {},
                            },
                          ];
                          return false;
                        } else {
                          errors = _errs88;
                          if (vErrors !== null) {
                            if (_errs88) {
                              vErrors.length = _errs88;
                            } else {
                              vErrors = null;
                            }
                          }
                        }
                        var valid31 = _errs87 === errors;
                      }
                      var valid30 = _errs83 === errors;
                    } else {
                      var valid30 = true;
                    }
                    if (valid30) {
                      if (
                        data23.local !== undefined &&
                        func0.call(data23, 'local')
                      ) {
                        const _errs90 = errors;
                        if ('0' !== data23.local) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/to/local',
                              schemaPath:
                                '#/properties/to/allOf/0/then/properties/local/const',
                              keyword: 'const',
                              params: { allowedValue: '0' },
                            },
                          ];
                          return false;
                        }
                        var valid30 = _errs90 === errors;
                      } else {
                        var valid30 = true;
                      }
                    }
                  }
                  var _valid4 = _errs82 === errors;
                  valid28 = _valid4;
                  if (valid28) {
                    var props4 = {};
                    props4.node = true;
                    props4.local = true;
                    props4.type = true;
                  }
                }
                if (!valid28) {
                  const err15 = {
                    instancePath: instancePath + '/to',
                    schemaPath: '#/properties/to/allOf/0/if',
                    keyword: 'if',
                    params: { failingKeyword: 'then' },
                  };
                  if (vErrors === null) {
                    vErrors = [err15];
                  } else {
                    vErrors.push(err15);
                  }
                  errors++;
                  validate21.errors = vErrors;
                  return false;
                }
                var valid27 = _errs78 === errors;
                if (valid27) {
                  const _errs91 = errors;
                  const _errs92 = errors;
                  let valid33 = true;
                  const _errs93 = errors;
                  if (
                    data23 &&
                    typeof data23 == 'object' &&
                    !Array.isArray(data23)
                  ) {
                    if (
                      data23.type !== undefined &&
                      func0.call(data23, 'type')
                    ) {
                      let data27 = data23.type;
                      if (!(data27 === 'session' || data27 === 'subagent')) {
                        const err16 = {};
                        if (vErrors === null) {
                          vErrors = [err16];
                        } else {
                          vErrors.push(err16);
                        }
                        errors++;
                      }
                    }
                  }
                  var _valid5 = _errs93 === errors;
                  errors = _errs92;
                  if (vErrors !== null) {
                    if (_errs92) {
                      vErrors.length = _errs92;
                    } else {
                      vErrors = null;
                    }
                  }
                  if (_valid5) {
                    const _errs95 = errors;
                    if (
                      data23 &&
                      typeof data23 == 'object' &&
                      !Array.isArray(data23)
                    ) {
                      if (
                        data23.node !== undefined &&
                        func0.call(data23, 'node')
                      ) {
                        let data28 = data23.node;
                        const _errs96 = errors;
                        if (typeof data28 !== 'string') {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/to/node',
                              schemaPath:
                                '#/properties/to/allOf/1/then/properties/node/type',
                              keyword: 'type',
                              params: { type: 'string' },
                            },
                          ];
                          return false;
                        }
                        const _errs98 = errors;
                        if (errors === _errs98) {
                          if (typeof data28 === 'string') {
                            if (!pattern5.test(data28)) {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/to/node',
                                  schemaPath:
                                    '#/properties/to/allOf/1/then/properties/node/allOf/0/pattern',
                                  keyword: 'pattern',
                                  params: {
                                    pattern:
                                      '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                  },
                                },
                              ];
                              return false;
                            }
                          } else {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/node',
                                schemaPath:
                                  '#/properties/to/allOf/1/then/properties/node/allOf/0/type',
                                keyword: 'type',
                                params: { type: 'string' },
                              },
                            ];
                            return false;
                          }
                        }
                        var valid36 = _errs98 === errors;
                        if (valid36) {
                          const _errs100 = errors;
                          const _errs101 = errors;
                          const _errs102 = errors;
                          if ('0' !== data28) {
                            const err17 = {};
                            if (vErrors === null) {
                              vErrors = [err17];
                            } else {
                              vErrors.push(err17);
                            }
                            errors++;
                          }
                          var valid37 = _errs102 === errors;
                          if (valid37) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/node',
                                schemaPath:
                                  '#/properties/to/allOf/1/then/properties/node/allOf/1/not',
                                keyword: 'not',
                                params: {},
                              },
                            ];
                            return false;
                          } else {
                            errors = _errs101;
                            if (vErrors !== null) {
                              if (_errs101) {
                                vErrors.length = _errs101;
                              } else {
                                vErrors = null;
                              }
                            }
                          }
                          var valid36 = _errs100 === errors;
                        }
                        var valid35 = _errs96 === errors;
                      } else {
                        var valid35 = true;
                      }
                      if (valid35) {
                        if (
                          data23.local !== undefined &&
                          func0.call(data23, 'local')
                        ) {
                          let data29 = data23.local;
                          const _errs103 = errors;
                          if (typeof data29 !== 'string') {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/local',
                                schemaPath:
                                  '#/properties/to/allOf/1/then/properties/local/type',
                                keyword: 'type',
                                params: { type: 'string' },
                              },
                            ];
                            return false;
                          }
                          const _errs105 = errors;
                          if (errors === _errs105) {
                            if (typeof data29 === 'string') {
                              if (!pattern5.test(data29)) {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/local',
                                    schemaPath:
                                      '#/properties/to/allOf/1/then/properties/local/allOf/0/pattern',
                                    keyword: 'pattern',
                                    params: {
                                      pattern:
                                        '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                    },
                                  },
                                ];
                                return false;
                              }
                            } else {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/to/local',
                                  schemaPath:
                                    '#/properties/to/allOf/1/then/properties/local/allOf/0/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                          }
                          var valid38 = _errs105 === errors;
                          if (valid38) {
                            const _errs107 = errors;
                            const _errs108 = errors;
                            const _errs109 = errors;
                            if ('0' !== data29) {
                              const err18 = {};
                              if (vErrors === null) {
                                vErrors = [err18];
                              } else {
                                vErrors.push(err18);
                              }
                              errors++;
                            }
                            var valid39 = _errs109 === errors;
                            if (valid39) {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/to/local',
                                  schemaPath:
                                    '#/properties/to/allOf/1/then/properties/local/allOf/1/not',
                                  keyword: 'not',
                                  params: {},
                                },
                              ];
                              return false;
                            } else {
                              errors = _errs108;
                              if (vErrors !== null) {
                                if (_errs108) {
                                  vErrors.length = _errs108;
                                } else {
                                  vErrors = null;
                                }
                              }
                            }
                            var valid38 = _errs107 === errors;
                          }
                          var valid35 = _errs103 === errors;
                        } else {
                          var valid35 = true;
                        }
                      }
                    }
                    var _valid5 = _errs95 === errors;
                    valid33 = _valid5;
                    if (valid33) {
                      var props5 = {};
                      props5.node = true;
                      props5.local = true;
                      props5.type = true;
                    }
                  }
                  if (!valid33) {
                    const err19 = {
                      instancePath: instancePath + '/to',
                      schemaPath: '#/properties/to/allOf/1/if',
                      keyword: 'if',
                      params: { failingKeyword: 'then' },
                    };
                    if (vErrors === null) {
                      vErrors = [err19];
                    } else {
                      vErrors.push(err19);
                    }
                    errors++;
                    validate21.errors = vErrors;
                    return false;
                  }
                  var valid27 = _errs91 === errors;
                  if (valid27) {
                    if (props4 !== true && props5 !== undefined) {
                      if (props5 === true) {
                        props4 = true;
                      } else {
                        props4 = props4 || {};
                        Object.assign(props4, props5);
                      }
                    }
                    const _errs110 = errors;
                    const _errs111 = errors;
                    let valid40 = true;
                    const _errs112 = errors;
                    if (
                      data23 &&
                      typeof data23 == 'object' &&
                      !Array.isArray(data23)
                    ) {
                      if (
                        data23.type !== undefined &&
                        func0.call(data23, 'type')
                      ) {
                        if ('group' !== data23.type) {
                          const err20 = {};
                          if (vErrors === null) {
                            vErrors = [err20];
                          } else {
                            vErrors.push(err20);
                          }
                          errors++;
                        }
                      }
                    }
                    var _valid6 = _errs112 === errors;
                    errors = _errs111;
                    if (vErrors !== null) {
                      if (_errs111) {
                        vErrors.length = _errs111;
                      } else {
                        vErrors = null;
                      }
                    }
                    if (_valid6) {
                      const _errs114 = errors;
                      if (
                        data23 &&
                        typeof data23 == 'object' &&
                        !Array.isArray(data23)
                      ) {
                        if (
                          data23.node !== undefined &&
                          func0.call(data23, 'node')
                        ) {
                          const _errs115 = errors;
                          if ('0' !== data23.node) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/node',
                                schemaPath:
                                  '#/properties/to/allOf/2/then/properties/node/const',
                                keyword: 'const',
                                params: { allowedValue: '0' },
                              },
                            ];
                            return false;
                          }
                          var valid42 = _errs115 === errors;
                        } else {
                          var valid42 = true;
                        }
                        if (valid42) {
                          if (
                            data23.local !== undefined &&
                            func0.call(data23, 'local')
                          ) {
                            let data32 = data23.local;
                            const _errs116 = errors;
                            if (typeof data32 !== 'string') {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/to/local',
                                  schemaPath:
                                    '#/properties/to/allOf/2/then/properties/local/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                            const _errs118 = errors;
                            if (errors === _errs118) {
                              if (typeof data32 === 'string') {
                                if (!pattern5.test(data32)) {
                                  validate21.errors = [
                                    {
                                      instancePath: instancePath + '/to/local',
                                      schemaPath:
                                        '#/properties/to/allOf/2/then/properties/local/allOf/0/pattern',
                                      keyword: 'pattern',
                                      params: {
                                        pattern:
                                          '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                      },
                                    },
                                  ];
                                  return false;
                                }
                              } else {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/local',
                                    schemaPath:
                                      '#/properties/to/allOf/2/then/properties/local/allOf/0/type',
                                    keyword: 'type',
                                    params: { type: 'string' },
                                  },
                                ];
                                return false;
                              }
                            }
                            var valid43 = _errs118 === errors;
                            if (valid43) {
                              const _errs120 = errors;
                              const _errs121 = errors;
                              const _errs122 = errors;
                              if ('0' !== data32) {
                                const err21 = {};
                                if (vErrors === null) {
                                  vErrors = [err21];
                                } else {
                                  vErrors.push(err21);
                                }
                                errors++;
                              }
                              var valid44 = _errs122 === errors;
                              if (valid44) {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/local',
                                    schemaPath:
                                      '#/properties/to/allOf/2/then/properties/local/allOf/1/not',
                                    keyword: 'not',
                                    params: {},
                                  },
                                ];
                                return false;
                              } else {
                                errors = _errs121;
                                if (vErrors !== null) {
                                  if (_errs121) {
                                    vErrors.length = _errs121;
                                  } else {
                                    vErrors = null;
                                  }
                                }
                              }
                              var valid43 = _errs120 === errors;
                            }
                            var valid42 = _errs116 === errors;
                          } else {
                            var valid42 = true;
                          }
                        }
                      }
                      var _valid6 = _errs114 === errors;
                      valid40 = _valid6;
                      if (valid40) {
                        var props6 = {};
                        props6.node = true;
                        props6.local = true;
                        props6.type = true;
                      }
                    }
                    if (!valid40) {
                      const err22 = {
                        instancePath: instancePath + '/to',
                        schemaPath: '#/properties/to/allOf/2/if',
                        keyword: 'if',
                        params: { failingKeyword: 'then' },
                      };
                      if (vErrors === null) {
                        vErrors = [err22];
                      } else {
                        vErrors.push(err22);
                      }
                      errors++;
                      validate21.errors = vErrors;
                      return false;
                    }
                    var valid27 = _errs110 === errors;
                    if (valid27) {
                      if (props4 !== true && props6 !== undefined) {
                        if (props6 === true) {
                          props4 = true;
                        } else {
                          props4 = props4 || {};
                          Object.assign(props4, props6);
                        }
                      }
                    }
                  }
                }
                if (errors === _errs76) {
                  if (
                    data23 &&
                    typeof data23 == 'object' &&
                    !Array.isArray(data23)
                  ) {
                    let missing4;
                    if (
                      ((data23.version === undefined ||
                        !func0.call(data23, 'version')) &&
                        (missing4 = 'version')) ||
                      ((data23.realm === undefined ||
                        !func0.call(data23, 'realm')) &&
                        (missing4 = 'realm')) ||
                      ((data23.type === undefined ||
                        !func0.call(data23, 'type')) &&
                        (missing4 = 'type')) ||
                      ((data23.node === undefined ||
                        !func0.call(data23, 'node')) &&
                        (missing4 = 'node')) ||
                      ((data23.local === undefined ||
                        !func0.call(data23, 'local')) &&
                        (missing4 = 'local'))
                    ) {
                      validate21.errors = [
                        {
                          instancePath: instancePath + '/to',
                          schemaPath: '#/properties/to/required',
                          keyword: 'required',
                          params: { missingProperty: missing4 },
                        },
                      ];
                      return false;
                    } else {
                      const _errs123 = errors;
                      for (const key2 of Object.keys(data23)) {
                        if (!(
                          key2 === 'version' ||
                          key2 === 'realm' ||
                          key2 === 'type' ||
                          key2 === 'node' ||
                          key2 === 'local'
                        )) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/to',
                              schemaPath:
                                '#/properties/to/additionalProperties',
                              keyword: 'additionalProperties',
                              params: { additionalProperty: key2 },
                            },
                          ];
                          return false;
                          break;
                        }
                      }
                      if (_errs123 === errors) {
                        if (
                          data23.version !== undefined &&
                          func0.call(data23, 'version')
                        ) {
                          let data33 = data23.version;
                          const _errs124 = errors;
                          if (!(
                            typeof data33 == 'number' &&
                            !(data33 % 1) &&
                            !isNaN(data33)
                          )) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/version',
                                schemaPath:
                                  '#/properties/to/properties/version/type',
                                keyword: 'type',
                                params: { type: 'integer' },
                              },
                            ];
                            return false;
                          }
                          if (1 !== data33) {
                            validate21.errors = [
                              {
                                instancePath: instancePath + '/to/version',
                                schemaPath:
                                  '#/properties/to/properties/version/const',
                                keyword: 'const',
                                params: { allowedValue: 1 },
                              },
                            ];
                            return false;
                          }
                          var valid45 = _errs124 === errors;
                        } else {
                          var valid45 = true;
                        }
                        if (valid45) {
                          if (
                            data23.realm !== undefined &&
                            func0.call(data23, 'realm')
                          ) {
                            let data34 = data23.realm;
                            const _errs126 = errors;
                            if (typeof data34 !== 'string') {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/to/realm',
                                  schemaPath:
                                    '#/properties/to/properties/realm/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                            const _errs128 = errors;
                            if (errors === _errs128) {
                              if (typeof data34 === 'string') {
                                if (!pattern5.test(data34)) {
                                  validate21.errors = [
                                    {
                                      instancePath: instancePath + '/to/realm',
                                      schemaPath:
                                        '#/properties/to/properties/realm/allOf/0/pattern',
                                      keyword: 'pattern',
                                      params: {
                                        pattern:
                                          '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                      },
                                    },
                                  ];
                                  return false;
                                }
                              } else {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/realm',
                                    schemaPath:
                                      '#/properties/to/properties/realm/allOf/0/type',
                                    keyword: 'type',
                                    params: { type: 'string' },
                                  },
                                ];
                                return false;
                              }
                            }
                            var valid46 = _errs128 === errors;
                            if (valid46) {
                              const _errs130 = errors;
                              const _errs131 = errors;
                              const _errs132 = errors;
                              if ('0' !== data34) {
                                const err23 = {};
                                if (vErrors === null) {
                                  vErrors = [err23];
                                } else {
                                  vErrors.push(err23);
                                }
                                errors++;
                              }
                              var valid47 = _errs132 === errors;
                              if (valid47) {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/realm',
                                    schemaPath:
                                      '#/properties/to/properties/realm/allOf/1/not',
                                    keyword: 'not',
                                    params: {},
                                  },
                                ];
                                return false;
                              } else {
                                errors = _errs131;
                                if (vErrors !== null) {
                                  if (_errs131) {
                                    vErrors.length = _errs131;
                                  } else {
                                    vErrors = null;
                                  }
                                }
                              }
                              var valid46 = _errs130 === errors;
                            }
                            var valid45 = _errs126 === errors;
                          } else {
                            var valid45 = true;
                          }
                          if (valid45) {
                            if (
                              data23.type !== undefined &&
                              func0.call(data23, 'type')
                            ) {
                              let data35 = data23.type;
                              const _errs133 = errors;
                              if (typeof data35 !== 'string') {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/type',
                                    schemaPath:
                                      '#/properties/to/properties/type/type',
                                    keyword: 'type',
                                    params: { type: 'string' },
                                  },
                                ];
                                return false;
                              }
                              if (!(
                                data35 === 'system' ||
                                data35 === 'session' ||
                                data35 === 'subagent' ||
                                data35 === 'group'
                              )) {
                                validate21.errors = [
                                  {
                                    instancePath: instancePath + '/to/type',
                                    schemaPath:
                                      '#/properties/to/properties/type/enum',
                                    keyword: 'enum',
                                    params: {
                                      allowedValues:
                                        schema32.properties.to.properties.type
                                          .enum,
                                    },
                                  },
                                ];
                                return false;
                              }
                              var valid45 = _errs133 === errors;
                            } else {
                              var valid45 = true;
                            }
                            if (valid45) {
                              if (
                                data23.node !== undefined &&
                                func0.call(data23, 'node')
                              ) {
                                let data36 = data23.node;
                                const _errs135 = errors;
                                if (errors === _errs135) {
                                  if (typeof data36 === 'string') {
                                    if (!pattern5.test(data36)) {
                                      validate21.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/to/node',
                                          schemaPath:
                                            '#/properties/to/properties/node/pattern',
                                          keyword: 'pattern',
                                          params: {
                                            pattern:
                                              '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                          },
                                        },
                                      ];
                                      return false;
                                    }
                                  } else {
                                    validate21.errors = [
                                      {
                                        instancePath: instancePath + '/to/node',
                                        schemaPath:
                                          '#/properties/to/properties/node/type',
                                        keyword: 'type',
                                        params: { type: 'string' },
                                      },
                                    ];
                                    return false;
                                  }
                                }
                                var valid45 = _errs135 === errors;
                              } else {
                                var valid45 = true;
                              }
                              if (valid45) {
                                if (
                                  data23.local !== undefined &&
                                  func0.call(data23, 'local')
                                ) {
                                  let data37 = data23.local;
                                  const _errs137 = errors;
                                  if (errors === _errs137) {
                                    if (typeof data37 === 'string') {
                                      if (!pattern5.test(data37)) {
                                        validate21.errors = [
                                          {
                                            instancePath:
                                              instancePath + '/to/local',
                                            schemaPath:
                                              '#/properties/to/properties/local/pattern',
                                            keyword: 'pattern',
                                            params: {
                                              pattern:
                                                '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                            },
                                          },
                                        ];
                                        return false;
                                      }
                                    } else {
                                      validate21.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/to/local',
                                          schemaPath:
                                            '#/properties/to/properties/local/type',
                                          keyword: 'type',
                                          params: { type: 'string' },
                                        },
                                      ];
                                      return false;
                                    }
                                  }
                                  var valid45 = _errs137 === errors;
                                } else {
                                  var valid45 = true;
                                }
                              }
                            }
                          }
                        }
                      }
                    }
                  } else {
                    validate21.errors = [
                      {
                        instancePath: instancePath + '/to',
                        schemaPath: '#/properties/to/type',
                        keyword: 'type',
                        params: { type: 'object' },
                      },
                    ];
                    return false;
                  }
                }
                var valid3 = _errs76 === errors;
              } else {
                var valid3 = true;
              }
              if (valid3) {
                if (
                  data.message_id !== undefined &&
                  func0.call(data, 'message_id')
                ) {
                  let data38 = data.message_id;
                  const _errs139 = errors;
                  if (errors === _errs139) {
                    if (typeof data38 === 'string') {
                      if (!pattern7.test(data38)) {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/message_id',
                            schemaPath: '#/properties/message_id/pattern',
                            keyword: 'pattern',
                            params: {
                              pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
                            },
                          },
                        ];
                        return false;
                      }
                    } else {
                      validate21.errors = [
                        {
                          instancePath: instancePath + '/message_id',
                          schemaPath: '#/properties/message_id/type',
                          keyword: 'type',
                          params: { type: 'string' },
                        },
                      ];
                      return false;
                    }
                  }
                  var valid3 = _errs139 === errors;
                } else {
                  var valid3 = true;
                }
                if (valid3) {
                  if (
                    data.thread_id !== undefined &&
                    func0.call(data, 'thread_id')
                  ) {
                    let data39 = data.thread_id;
                    const _errs141 = errors;
                    if (errors === _errs141) {
                      if (typeof data39 === 'string') {
                        if (!pattern7.test(data39)) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/thread_id',
                              schemaPath: '#/properties/thread_id/pattern',
                              keyword: 'pattern',
                              params: {
                                pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
                              },
                            },
                          ];
                          return false;
                        }
                      } else {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/thread_id',
                            schemaPath: '#/properties/thread_id/type',
                            keyword: 'type',
                            params: { type: 'string' },
                          },
                        ];
                        return false;
                      }
                    }
                    var valid3 = _errs141 === errors;
                  } else {
                    var valid3 = true;
                  }
                  if (valid3) {
                    if (
                      data.message_class !== undefined &&
                      func0.call(data, 'message_class')
                    ) {
                      let data40 = data.message_class;
                      const _errs143 = errors;
                      if (typeof data40 !== 'string') {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/message_class',
                            schemaPath: '#/properties/message_class/type',
                            keyword: 'type',
                            params: { type: 'string' },
                          },
                        ];
                        return false;
                      }
                      if (!(
                        data40 === 'unicast' ||
                        data40 === 'multicast' ||
                        data40 === 'broadcast'
                      )) {
                        validate21.errors = [
                          {
                            instancePath: instancePath + '/message_class',
                            schemaPath: '#/properties/message_class/enum',
                            keyword: 'enum',
                            params: {
                              allowedValues:
                                schema32.properties.message_class.enum,
                            },
                          },
                        ];
                        return false;
                      }
                      var valid3 = _errs143 === errors;
                    } else {
                      var valid3 = true;
                    }
                    if (valid3) {
                      if (
                        data.dispatch_status !== undefined &&
                        func0.call(data, 'dispatch_status')
                      ) {
                        let data41 = data.dispatch_status;
                        const _errs145 = errors;
                        if (typeof data41 !== 'string') {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/dispatch_status',
                              schemaPath: '#/properties/dispatch_status/type',
                              keyword: 'type',
                              params: { type: 'string' },
                            },
                          ];
                          return false;
                        }
                        if (!(
                          data41 === 'pending' ||
                          data41 === 'dispatched' ||
                          data41 === 'partial' ||
                          data41 === 'closed'
                        )) {
                          validate21.errors = [
                            {
                              instancePath: instancePath + '/dispatch_status',
                              schemaPath: '#/properties/dispatch_status/enum',
                              keyword: 'enum',
                              params: {
                                allowedValues:
                                  schema32.properties.dispatch_status.enum,
                              },
                            },
                          ];
                          return false;
                        }
                        var valid3 = _errs145 === errors;
                      } else {
                        var valid3 = true;
                      }
                      if (valid3) {
                        if (
                          data.submission_id !== undefined &&
                          func0.call(data, 'submission_id')
                        ) {
                          let data42 = data.submission_id;
                          const _errs147 = errors;
                          if (errors === _errs147) {
                            if (typeof data42 === 'string') {
                              if (!pattern7.test(data42)) {
                                validate21.errors = [
                                  {
                                    instancePath:
                                      instancePath + '/submission_id',
                                    schemaPath:
                                      '#/properties/submission_id/pattern',
                                    keyword: 'pattern',
                                    params: {
                                      pattern:
                                        '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
                                    },
                                  },
                                ];
                                return false;
                              }
                            } else {
                              validate21.errors = [
                                {
                                  instancePath: instancePath + '/submission_id',
                                  schemaPath: '#/properties/submission_id/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                          }
                          var valid3 = _errs147 === errors;
                        } else {
                          var valid3 = true;
                        }
                        if (valid3) {
                          if (
                            data.wait_until !== undefined &&
                            func0.call(data, 'wait_until')
                          ) {
                            let data43 = data.wait_until;
                            const _errs149 = errors;
                            if (errors === _errs149) {
                              if (errors === _errs149) {
                                if (typeof data43 === 'string') {
                                  if (!pattern18.test(data43)) {
                                    validate21.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/wait_until',
                                        schemaPath:
                                          '#/properties/wait_until/pattern',
                                        keyword: 'pattern',
                                        params: {
                                          pattern:
                                            '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
                                        },
                                      },
                                    ];
                                    return false;
                                  }
                                } else {
                                  validate21.errors = [
                                    {
                                      instancePath:
                                        instancePath + '/wait_until',
                                      schemaPath:
                                        '#/properties/wait_until/type',
                                      keyword: 'type',
                                      params: { type: 'string' },
                                    },
                                  ];
                                  return false;
                                }
                              }
                            }
                            var valid3 = _errs149 === errors;
                          } else {
                            var valid3 = true;
                          }
                          if (valid3) {
                            if (
                              data.expires_at !== undefined &&
                              func0.call(data, 'expires_at')
                            ) {
                              let data44 = data.expires_at;
                              const _errs151 = errors;
                              if (errors === _errs151) {
                                if (errors === _errs151) {
                                  if (typeof data44 === 'string') {
                                    if (!pattern18.test(data44)) {
                                      validate21.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/expires_at',
                                          schemaPath:
                                            '#/properties/expires_at/pattern',
                                          keyword: 'pattern',
                                          params: {
                                            pattern:
                                              '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
                                          },
                                        },
                                      ];
                                      return false;
                                    }
                                  } else {
                                    validate21.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/expires_at',
                                        schemaPath:
                                          '#/properties/expires_at/type',
                                        keyword: 'type',
                                        params: { type: 'string' },
                                      },
                                    ];
                                    return false;
                                  }
                                }
                              }
                              var valid3 = _errs151 === errors;
                            } else {
                              var valid3 = true;
                            }
                          }
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    } else {
      validate21.errors = [
        {
          instancePath,
          schemaPath: '#/type',
          keyword: 'type',
          params: { type: 'object' },
        },
      ];
      return false;
    }
  }
  validate21.errors = vErrors;
  return errors === 0;
}
validate21.evaluated = {
  props: true,
  dynamicProps: false,
  dynamicItems: false,
};
