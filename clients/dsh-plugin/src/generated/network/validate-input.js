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
export const validate = validate20;
export default validate20;
const schema31 = {
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
const func0 = Object.prototype.hasOwnProperty;
const func81 = function ucs2length(str) {
  const len = str.length;
  let length = 0;
  let pos = 0;
  let value;
  while (pos < len) {
    length++;
    value = str.charCodeAt(pos++);
    if (value >= 0xd800 && value <= 0xdbff && pos < len) {
      // high surrogate, and there is a next character
      value = str.charCodeAt(pos);
      if ((value & 0xfc00) === 0xdc00) pos++; // low surrogate
    }
  }
  return length;
};
const pattern4 = new RegExp(
  '^aur1:(?:0|[1-9a-f][0-9a-f]{0,15})(?:\\.(?:0|[1-9a-f][0-9a-f]{0,15})){1,2}$',
  'u',
);
const pattern5 = new RegExp(
  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
  'u',
);
const pattern6 = new RegExp('^[a-z][a-z0-9_]{0,63}$', 'u');
const pattern7 = new RegExp('^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$', 'u');
const pattern8 = new RegExp('^sha256:[0-9a-f]{64}$', 'u');
const pattern18 = new RegExp(
  '^[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$',
  'u',
);
function validate20(
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
  const evaluated0 = validate20.evaluated;
  if (evaluated0.dynamicProps) {
    evaluated0.props = undefined;
  }
  if (evaluated0.dynamicItems) {
    evaluated0.items = undefined;
  }
  if (errors === 0) {
    if (data && typeof data == 'object' && !Array.isArray(data)) {
      let missing0;
      if (
        ((data.target === undefined || !func0.call(data, 'target')) &&
          (missing0 = 'target')) ||
        ((data.body === undefined || !func0.call(data, 'body')) &&
          (missing0 = 'body'))
      ) {
        validate20.errors = [
          {
            instancePath,
            schemaPath: '#/required',
            keyword: 'required',
            params: { missingProperty: missing0 },
          },
        ];
        return false;
      } else {
        const _errs1 = errors;
        for (const key0 of Object.keys(data)) {
          if (!(key0 === 'target' || key0 === 'body' || key0 === 'thread')) {
            validate20.errors = [
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
        if (_errs1 === errors) {
          if (data.target !== undefined && func0.call(data, 'target')) {
            let data0 = data.target;
            const _errs2 = errors;
            if (errors === _errs2) {
              if (typeof data0 === 'string') {
                if (!pattern4.test(data0)) {
                  validate20.errors = [
                    {
                      instancePath: instancePath + '/target',
                      schemaPath: '#/properties/target/pattern',
                      keyword: 'pattern',
                      params: {
                        pattern:
                          '^aur1:(?:0|[1-9a-f][0-9a-f]{0,15})(?:\\.(?:0|[1-9a-f][0-9a-f]{0,15})){1,2}$',
                      },
                    },
                  ];
                  return false;
                }
              } else {
                validate20.errors = [
                  {
                    instancePath: instancePath + '/target',
                    schemaPath: '#/properties/target/type',
                    keyword: 'type',
                    params: { type: 'string' },
                  },
                ];
                return false;
              }
            }
            var valid0 = _errs2 === errors;
          } else {
            var valid0 = true;
          }
          if (valid0) {
            if (data.body !== undefined && func0.call(data, 'body')) {
              let data1 = data.body;
              const _errs4 = errors;
              if (!(
                data1 &&
                typeof data1 == 'object' &&
                !Array.isArray(data1)
              )) {
                validate20.errors = [
                  {
                    instancePath: instancePath + '/body',
                    schemaPath: '#/properties/body/type',
                    keyword: 'type',
                    params: { type: 'object' },
                  },
                ];
                return false;
              }
              const _errs6 = errors;
              const _errs8 = errors;
              const _errs9 = errors;
              let valid3 = true;
              const _errs10 = errors;
              if (data1 && typeof data1 == 'object' && !Array.isArray(data1)) {
                if (
                  data1.subtype !== undefined &&
                  func0.call(data1, 'subtype')
                ) {
                  if ('text' !== data1.subtype) {
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
              var _valid0 = _errs10 === errors;
              errors = _errs9;
              if (vErrors !== null) {
                if (_errs9) {
                  vErrors.length = _errs9;
                } else {
                  vErrors = null;
                }
              }
              if (_valid0) {
                const _errs12 = errors;
                if (
                  data1 &&
                  typeof data1 == 'object' &&
                  !Array.isArray(data1)
                ) {
                  let missing1;
                  if (
                    (data1.text === undefined || !func0.call(data1, 'text')) &&
                    (missing1 = 'text')
                  ) {
                    validate20.errors = [
                      {
                        instancePath: instancePath + '/body',
                        schemaPath:
                          '#/properties/body/allOf/0/allOf/0/then/required',
                        keyword: 'required',
                        params: { missingProperty: missing1 },
                      },
                    ];
                    return false;
                  }
                }
                var _valid0 = _errs12 === errors;
                valid3 = _valid0;
                if (valid3) {
                  var props0 = {};
                  props0.text = true;
                  props0.subtype = true;
                }
              }
              if (!valid3) {
                const err1 = {
                  instancePath: instancePath + '/body',
                  schemaPath: '#/properties/body/allOf/0/allOf/0/if',
                  keyword: 'if',
                  params: { failingKeyword: 'then' },
                };
                if (vErrors === null) {
                  vErrors = [err1];
                } else {
                  vErrors.push(err1);
                }
                errors++;
                validate20.errors = vErrors;
                return false;
              }
              var valid2 = _errs8 === errors;
              if (valid2) {
                const _errs13 = errors;
                const _errs14 = errors;
                let valid5 = true;
                const _errs15 = errors;
                if (
                  data1 &&
                  typeof data1 == 'object' &&
                  !Array.isArray(data1)
                ) {
                  if (
                    data1.subtype !== undefined &&
                    func0.call(data1, 'subtype')
                  ) {
                    let data3 = data1.subtype;
                    if (!(data3 === 'progress' || data3 === 'status')) {
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
                    data1 &&
                    typeof data1 == 'object' &&
                    !Array.isArray(data1)
                  ) {
                    let missing2;
                    if (
                      ((data1.revision === undefined ||
                        !func0.call(data1, 'revision')) &&
                        (missing2 = 'revision')) ||
                      ((data1.state === undefined ||
                        !func0.call(data1, 'state')) &&
                        (missing2 = 'state'))
                    ) {
                      validate20.errors = [
                        {
                          instancePath: instancePath + '/body',
                          schemaPath:
                            '#/properties/body/allOf/0/allOf/1/then/required',
                          keyword: 'required',
                          params: { missingProperty: missing2 },
                        },
                      ];
                      return false;
                    }
                  }
                  var _valid1 = _errs17 === errors;
                  valid5 = _valid1;
                  if (valid5) {
                    var props1 = {};
                    props1.revision = true;
                    props1.state = true;
                    props1.subtype = true;
                  }
                }
                if (!valid5) {
                  const err3 = {
                    instancePath: instancePath + '/body',
                    schemaPath: '#/properties/body/allOf/0/allOf/1/if',
                    keyword: 'if',
                    params: { failingKeyword: 'then' },
                  };
                  if (vErrors === null) {
                    vErrors = [err3];
                  } else {
                    vErrors.push(err3);
                  }
                  errors++;
                  validate20.errors = vErrors;
                  return false;
                }
                var valid2 = _errs13 === errors;
                if (valid2) {
                  if (props0 !== true && props1 !== undefined) {
                    if (props1 === true) {
                      props0 = true;
                    } else {
                      props0 = props0 || {};
                      Object.assign(props0, props1);
                    }
                  }
                  const _errs18 = errors;
                  const _errs19 = errors;
                  let valid7 = true;
                  const _errs20 = errors;
                  if (
                    data1 &&
                    typeof data1 == 'object' &&
                    !Array.isArray(data1)
                  ) {
                    if (
                      data1.subtype !== undefined &&
                      func0.call(data1, 'subtype')
                    ) {
                      if ('artifact_ref' !== data1.subtype) {
                        const err4 = {};
                        if (vErrors === null) {
                          vErrors = [err4];
                        } else {
                          vErrors.push(err4);
                        }
                        errors++;
                      }
                    }
                  }
                  var _valid2 = _errs20 === errors;
                  errors = _errs19;
                  if (vErrors !== null) {
                    if (_errs19) {
                      vErrors.length = _errs19;
                    } else {
                      vErrors = null;
                    }
                  }
                  if (_valid2) {
                    const _errs22 = errors;
                    if (
                      data1 &&
                      typeof data1 == 'object' &&
                      !Array.isArray(data1)
                    ) {
                      let missing3;
                      if (
                        (data1.artifact === undefined ||
                          !func0.call(data1, 'artifact')) &&
                        (missing3 = 'artifact')
                      ) {
                        validate20.errors = [
                          {
                            instancePath: instancePath + '/body',
                            schemaPath:
                              '#/properties/body/allOf/0/allOf/2/then/required',
                            keyword: 'required',
                            params: { missingProperty: missing3 },
                          },
                        ];
                        return false;
                      }
                    }
                    var _valid2 = _errs22 === errors;
                    valid7 = _valid2;
                    if (valid7) {
                      var props2 = {};
                      props2.artifact = true;
                      props2.subtype = true;
                    }
                  }
                  if (!valid7) {
                    const err5 = {
                      instancePath: instancePath + '/body',
                      schemaPath: '#/properties/body/allOf/0/allOf/2/if',
                      keyword: 'if',
                      params: { failingKeyword: 'then' },
                    };
                    if (vErrors === null) {
                      vErrors = [err5];
                    } else {
                      vErrors.push(err5);
                    }
                    errors++;
                    validate20.errors = vErrors;
                    return false;
                  }
                  var valid2 = _errs18 === errors;
                  if (valid2) {
                    if (props0 !== true && props2 !== undefined) {
                      if (props2 === true) {
                        props0 = true;
                      } else {
                        props0 = props0 || {};
                        Object.assign(props0, props2);
                      }
                    }
                    const _errs23 = errors;
                    const _errs24 = errors;
                    let valid9 = true;
                    const _errs25 = errors;
                    if (
                      data1 &&
                      typeof data1 == 'object' &&
                      !Array.isArray(data1)
                    ) {
                      if (
                        data1.subtype !== undefined &&
                        func0.call(data1, 'subtype')
                      ) {
                        let data5 = data1.subtype;
                        if (!(
                          data5 === 'conversation_opened' ||
                          data5 === 'execution_waiting'
                        )) {
                          const err6 = {};
                          if (vErrors === null) {
                            vErrors = [err6];
                          } else {
                            vErrors.push(err6);
                          }
                          errors++;
                        }
                      }
                    }
                    var _valid3 = _errs25 === errors;
                    errors = _errs24;
                    if (vErrors !== null) {
                      if (_errs24) {
                        vErrors.length = _errs24;
                      } else {
                        vErrors = null;
                      }
                    }
                    if (_valid3) {
                      const _errs27 = errors;
                      if (
                        data1 &&
                        typeof data1 == 'object' &&
                        !Array.isArray(data1)
                      ) {
                        let missing4;
                        if (
                          (data1.provenance === undefined ||
                            !func0.call(data1, 'provenance')) &&
                          (missing4 = 'provenance')
                        ) {
                          validate20.errors = [
                            {
                              instancePath: instancePath + '/body',
                              schemaPath:
                                '#/properties/body/allOf/0/allOf/3/then/required',
                              keyword: 'required',
                              params: { missingProperty: missing4 },
                            },
                          ];
                          return false;
                        } else {
                          if (
                            data1.provenance !== undefined &&
                            func0.call(data1, 'provenance')
                          ) {
                            if ('host' !== data1.provenance) {
                              validate20.errors = [
                                {
                                  instancePath:
                                    instancePath + '/body/provenance',
                                  schemaPath:
                                    '#/properties/body/allOf/0/allOf/3/then/properties/provenance/const',
                                  keyword: 'const',
                                  params: { allowedValue: 'host' },
                                },
                              ];
                              return false;
                            }
                          }
                        }
                      }
                      var _valid3 = _errs27 === errors;
                      valid9 = _valid3;
                      if (valid9) {
                        var props3 = {};
                        props3.provenance = true;
                        props3.subtype = true;
                      }
                    }
                    if (!valid9) {
                      const err7 = {
                        instancePath: instancePath + '/body',
                        schemaPath: '#/properties/body/allOf/0/allOf/3/if',
                        keyword: 'if',
                        params: { failingKeyword: 'then' },
                      };
                      if (vErrors === null) {
                        vErrors = [err7];
                      } else {
                        vErrors.push(err7);
                      }
                      errors++;
                      validate20.errors = vErrors;
                      return false;
                    }
                    var valid2 = _errs23 === errors;
                    if (valid2) {
                      if (props0 !== true && props3 !== undefined) {
                        if (props3 === true) {
                          props0 = true;
                        } else {
                          props0 = props0 || {};
                          Object.assign(props0, props3);
                        }
                      }
                      const _errs29 = errors;
                      const _errs30 = errors;
                      let valid12 = true;
                      const _errs31 = errors;
                      if (
                        data1 &&
                        typeof data1 == 'object' &&
                        !Array.isArray(data1)
                      ) {
                        if (
                          data1.subtype !== undefined &&
                          func0.call(data1, 'subtype')
                        ) {
                          if ('group_invitation' !== data1.subtype) {
                            const err8 = {};
                            if (vErrors === null) {
                              vErrors = [err8];
                            } else {
                              vErrors.push(err8);
                            }
                            errors++;
                          }
                        }
                      }
                      var _valid4 = _errs31 === errors;
                      errors = _errs30;
                      if (vErrors !== null) {
                        if (_errs30) {
                          vErrors.length = _errs30;
                        } else {
                          vErrors = null;
                        }
                      }
                      if (_valid4) {
                        const _errs33 = errors;
                        if (
                          data1 &&
                          typeof data1 == 'object' &&
                          !Array.isArray(data1)
                        ) {
                          let missing5;
                          if (
                            ((data1.group_address === undefined ||
                              !func0.call(data1, 'group_address')) &&
                              (missing5 = 'group_address')) ||
                            ((data1.invitation_id === undefined ||
                              !func0.call(data1, 'invitation_id')) &&
                              (missing5 = 'invitation_id')) ||
                            ((data1.invite_expires_at === undefined ||
                              !func0.call(data1, 'invite_expires_at')) &&
                              (missing5 = 'invite_expires_at'))
                          ) {
                            validate20.errors = [
                              {
                                instancePath: instancePath + '/body',
                                schemaPath:
                                  '#/properties/body/allOf/0/allOf/4/then/required',
                                keyword: 'required',
                                params: { missingProperty: missing5 },
                              },
                            ];
                            return false;
                          } else {
                            if (
                              data1.group_address !== undefined &&
                              func0.call(data1, 'group_address')
                            ) {
                              let data8 = data1.group_address;
                              const _errs34 = errors;
                              if (errors === _errs34) {
                                if (
                                  data8 &&
                                  typeof data8 == 'object' &&
                                  !Array.isArray(data8)
                                ) {
                                  if (
                                    data8.local !== undefined &&
                                    func0.call(data8, 'local')
                                  ) {
                                    const _errs37 = errors;
                                    const _errs38 = errors;
                                    if ('1' !== data8.local) {
                                      const err9 = {};
                                      if (vErrors === null) {
                                        vErrors = [err9];
                                      } else {
                                        vErrors.push(err9);
                                      }
                                      errors++;
                                    }
                                    var valid16 = _errs38 === errors;
                                    if (valid16) {
                                      validate20.errors = [
                                        {
                                          instancePath:
                                            instancePath +
                                            '/body/group_address/local',
                                          schemaPath:
                                            '#/properties/body/allOf/0/allOf/4/then/properties/group_address/properties/local/not',
                                          keyword: 'not',
                                          params: {},
                                        },
                                      ];
                                      return false;
                                    } else {
                                      errors = _errs37;
                                      if (vErrors !== null) {
                                        if (_errs37) {
                                          vErrors.length = _errs37;
                                        } else {
                                          vErrors = null;
                                        }
                                      }
                                    }
                                  }
                                } else {
                                  validate20.errors = [
                                    {
                                      instancePath:
                                        instancePath + '/body/group_address',
                                      schemaPath:
                                        '#/properties/body/allOf/0/allOf/4/then/properties/group_address/type',
                                      keyword: 'type',
                                      params: { type: 'object' },
                                    },
                                  ];
                                  return false;
                                }
                              }
                              var valid14 = _errs34 === errors;
                            } else {
                              var valid14 = true;
                            }
                            if (valid14) {
                              if (
                                data1.text !== undefined &&
                                func0.call(data1, 'text')
                              ) {
                                let data10 = data1.text;
                                const _errs39 = errors;
                                if (errors === _errs39) {
                                  if (typeof data10 === 'string') {
                                    if (func81(data10) > 512) {
                                      validate20.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/body/text',
                                          schemaPath:
                                            '#/properties/body/allOf/0/allOf/4/then/properties/text/maxLength',
                                          keyword: 'maxLength',
                                          params: { limit: 512 },
                                        },
                                      ];
                                      return false;
                                    }
                                  } else {
                                    validate20.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/body/text',
                                        schemaPath:
                                          '#/properties/body/allOf/0/allOf/4/then/properties/text/type',
                                        keyword: 'type',
                                        params: { type: 'string' },
                                      },
                                    ];
                                    return false;
                                  }
                                }
                                var valid14 = _errs39 === errors;
                              } else {
                                var valid14 = true;
                              }
                            }
                          }
                        }
                        var _valid4 = _errs33 === errors;
                        valid12 = _valid4;
                        if (valid12) {
                          var props4 = {};
                          props4.group_address = true;
                          props4.text = true;
                          props4.invitation_id = true;
                          props4.invite_expires_at = true;
                          props4.subtype = true;
                        }
                      }
                      if (!valid12) {
                        const err10 = {
                          instancePath: instancePath + '/body',
                          schemaPath: '#/properties/body/allOf/0/allOf/4/if',
                          keyword: 'if',
                          params: { failingKeyword: 'then' },
                        };
                        if (vErrors === null) {
                          vErrors = [err10];
                        } else {
                          vErrors.push(err10);
                        }
                        errors++;
                        validate20.errors = vErrors;
                        return false;
                      }
                      var valid2 = _errs29 === errors;
                      if (valid2) {
                        if (props0 !== true && props4 !== undefined) {
                          if (props4 === true) {
                            props0 = true;
                          } else {
                            props0 = props0 || {};
                            Object.assign(props0, props4);
                          }
                        }
                        const _errs41 = errors;
                        const _errs42 = errors;
                        let valid17 = true;
                        const _errs43 = errors;
                        if (
                          data1 &&
                          typeof data1 == 'object' &&
                          !Array.isArray(data1)
                        ) {
                          if (
                            data1.subtype !== undefined &&
                            func0.call(data1, 'subtype')
                          ) {
                            if ('control' !== data1.subtype) {
                              const err11 = {};
                              if (vErrors === null) {
                                vErrors = [err11];
                              } else {
                                vErrors.push(err11);
                              }
                              errors++;
                            }
                          }
                        }
                        var _valid5 = _errs43 === errors;
                        errors = _errs42;
                        if (vErrors !== null) {
                          if (_errs42) {
                            vErrors.length = _errs42;
                          } else {
                            vErrors = null;
                          }
                        }
                        let ifClause0;
                        if (_valid5) {
                          const _errs45 = errors;
                          if (
                            data1 &&
                            typeof data1 == 'object' &&
                            !Array.isArray(data1)
                          ) {
                            let missing6;
                            if (
                              (data1.action === undefined ||
                                !func0.call(data1, 'action')) &&
                              (missing6 = 'action')
                            ) {
                              validate20.errors = [
                                {
                                  instancePath: instancePath + '/body',
                                  schemaPath:
                                    '#/properties/body/allOf/0/allOf/5/then/required',
                                  keyword: 'required',
                                  params: { missingProperty: missing6 },
                                },
                              ];
                              return false;
                            }
                          }
                          var _valid5 = _errs45 === errors;
                          valid17 = _valid5;
                          if (valid17) {
                            var props5 = {};
                            props5.action = true;
                            props5.subtype = true;
                          }
                          ifClause0 = 'then';
                        } else {
                          const _errs46 = errors;
                          if (
                            data1 &&
                            typeof data1 == 'object' &&
                            !Array.isArray(data1)
                          ) {
                            if (
                              data1.action !== undefined &&
                              func0.call(data1, 'action')
                            ) {
                              var valid19 = false;
                              validate20.errors = [
                                {
                                  instancePath: instancePath + '/body/action',
                                  schemaPath:
                                    '#/properties/body/allOf/0/allOf/5/else/properties/action/false schema',
                                  keyword: 'false schema',
                                  params: {},
                                },
                              ];
                              return false;
                            } else {
                              var valid19 = true;
                            }
                            if (valid19) {
                              if (
                                data1.target_operation_id !== undefined &&
                                func0.call(data1, 'target_operation_id')
                              ) {
                                var valid19 = false;
                                validate20.errors = [
                                  {
                                    instancePath:
                                      instancePath +
                                      '/body/target_operation_id',
                                    schemaPath:
                                      '#/properties/body/allOf/0/allOf/5/else/properties/target_operation_id/false schema',
                                    keyword: 'false schema',
                                    params: {},
                                  },
                                ];
                                return false;
                              } else {
                                var valid19 = true;
                              }
                              if (valid19) {
                                if (
                                  data1.expected_state_version !== undefined &&
                                  func0.call(data1, 'expected_state_version')
                                ) {
                                  var valid19 = false;
                                  validate20.errors = [
                                    {
                                      instancePath:
                                        instancePath +
                                        '/body/expected_state_version',
                                      schemaPath:
                                        '#/properties/body/allOf/0/allOf/5/else/properties/expected_state_version/false schema',
                                      keyword: 'false schema',
                                      params: {},
                                    },
                                  ];
                                  return false;
                                } else {
                                  var valid19 = true;
                                }
                              }
                            }
                          }
                          var _valid5 = _errs46 === errors;
                          valid17 = _valid5;
                          if (valid17) {
                            if (props5 !== true) {
                              props5 = props5 || {};
                              props5.action = true;
                              props5.target_operation_id = true;
                              props5.expected_state_version = true;
                            }
                          }
                          ifClause0 = 'else';
                        }
                        if (!valid17) {
                          const err12 = {
                            instancePath: instancePath + '/body',
                            schemaPath: '#/properties/body/allOf/0/allOf/5/if',
                            keyword: 'if',
                            params: { failingKeyword: ifClause0 },
                          };
                          if (vErrors === null) {
                            vErrors = [err12];
                          } else {
                            vErrors.push(err12);
                          }
                          errors++;
                          validate20.errors = vErrors;
                          return false;
                        }
                        var valid2 = _errs41 === errors;
                        if (valid2) {
                          if (props0 !== true && props5 !== undefined) {
                            if (props5 === true) {
                              props0 = true;
                            } else {
                              props0 = props0 || {};
                              Object.assign(props0, props5);
                            }
                          }
                        }
                      }
                    }
                  }
                }
              }
              if (errors === _errs6) {
                if (
                  data1 &&
                  typeof data1 == 'object' &&
                  !Array.isArray(data1)
                ) {
                  let missing7;
                  if (
                    (data1.subtype === undefined ||
                      !func0.call(data1, 'subtype')) &&
                    (missing7 = 'subtype')
                  ) {
                    validate20.errors = [
                      {
                        instancePath: instancePath + '/body',
                        schemaPath: '#/properties/body/allOf/0/required',
                        keyword: 'required',
                        params: { missingProperty: missing7 },
                      },
                    ];
                    return false;
                  } else {
                    const _errs47 = errors;
                    for (const key1 of Object.keys(data1)) {
                      if (
                        !func0.call(
                          schema31.properties.body.allOf[0].properties,
                          key1,
                        )
                      ) {
                        validate20.errors = [
                          {
                            instancePath: instancePath + '/body',
                            schemaPath:
                              '#/properties/body/allOf/0/additionalProperties',
                            keyword: 'additionalProperties',
                            params: { additionalProperty: key1 },
                          },
                        ];
                        return false;
                        break;
                      }
                    }
                    if (_errs47 === errors) {
                      if (
                        data1.subtype !== undefined &&
                        func0.call(data1, 'subtype')
                      ) {
                        let data15 = data1.subtype;
                        const _errs48 = errors;
                        if (typeof data15 !== 'string') {
                          validate20.errors = [
                            {
                              instancePath: instancePath + '/body/subtype',
                              schemaPath:
                                '#/properties/body/allOf/0/properties/subtype/type',
                              keyword: 'type',
                              params: { type: 'string' },
                            },
                          ];
                          return false;
                        }
                        if (!(
                          data15 === 'text' ||
                          data15 === 'progress' ||
                          data15 === 'status' ||
                          data15 === 'artifact_ref' ||
                          data15 === 'conversation_opened' ||
                          data15 === 'execution_waiting' ||
                          data15 === 'group_invitation' ||
                          data15 === 'control'
                        )) {
                          validate20.errors = [
                            {
                              instancePath: instancePath + '/body/subtype',
                              schemaPath:
                                '#/properties/body/allOf/0/properties/subtype/enum',
                              keyword: 'enum',
                              params: {
                                allowedValues:
                                  schema31.properties.body.allOf[0].properties
                                    .subtype.enum,
                              },
                            },
                          ];
                          return false;
                        }
                        var valid20 = _errs48 === errors;
                      } else {
                        var valid20 = true;
                      }
                      if (valid20) {
                        if (
                          data1.text !== undefined &&
                          func0.call(data1, 'text')
                        ) {
                          let data16 = data1.text;
                          const _errs50 = errors;
                          if (errors === _errs50) {
                            if (typeof data16 === 'string') {
                              if (func81(data16) > 16384) {
                                validate20.errors = [
                                  {
                                    instancePath: instancePath + '/body/text',
                                    schemaPath:
                                      '#/properties/body/allOf/0/properties/text/maxLength',
                                    keyword: 'maxLength',
                                    params: { limit: 16384 },
                                  },
                                ];
                                return false;
                              }
                            } else {
                              validate20.errors = [
                                {
                                  instancePath: instancePath + '/body/text',
                                  schemaPath:
                                    '#/properties/body/allOf/0/properties/text/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                          }
                          var valid20 = _errs50 === errors;
                        } else {
                          var valid20 = true;
                        }
                        if (valid20) {
                          if (
                            data1.provenance !== undefined &&
                            func0.call(data1, 'provenance')
                          ) {
                            let data17 = data1.provenance;
                            const _errs52 = errors;
                            if (typeof data17 !== 'string') {
                              validate20.errors = [
                                {
                                  instancePath:
                                    instancePath + '/body/provenance',
                                  schemaPath:
                                    '#/properties/body/allOf/0/properties/provenance/type',
                                  keyword: 'type',
                                  params: { type: 'string' },
                                },
                              ];
                              return false;
                            }
                            if (!(data17 === 'model' || data17 === 'host')) {
                              validate20.errors = [
                                {
                                  instancePath:
                                    instancePath + '/body/provenance',
                                  schemaPath:
                                    '#/properties/body/allOf/0/properties/provenance/enum',
                                  keyword: 'enum',
                                  params: {
                                    allowedValues:
                                      schema31.properties.body.allOf[0]
                                        .properties.provenance.enum,
                                  },
                                },
                              ];
                              return false;
                            }
                            var valid20 = _errs52 === errors;
                          } else {
                            var valid20 = true;
                          }
                          if (valid20) {
                            if (
                              data1.revision !== undefined &&
                              func0.call(data1, 'revision')
                            ) {
                              let data18 = data1.revision;
                              const _errs54 = errors;
                              if (errors === _errs54) {
                                if (typeof data18 === 'string') {
                                  if (!pattern5.test(data18)) {
                                    validate20.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/body/revision',
                                        schemaPath:
                                          '#/properties/body/allOf/0/properties/revision/pattern',
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
                                  validate20.errors = [
                                    {
                                      instancePath:
                                        instancePath + '/body/revision',
                                      schemaPath:
                                        '#/properties/body/allOf/0/properties/revision/type',
                                      keyword: 'type',
                                      params: { type: 'string' },
                                    },
                                  ];
                                  return false;
                                }
                              }
                              var valid20 = _errs54 === errors;
                            } else {
                              var valid20 = true;
                            }
                            if (valid20) {
                              if (
                                data1.state !== undefined &&
                                func0.call(data1, 'state')
                              ) {
                                let data19 = data1.state;
                                const _errs56 = errors;
                                if (errors === _errs56) {
                                  if (typeof data19 === 'string') {
                                    if (!pattern6.test(data19)) {
                                      validate20.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/body/state',
                                          schemaPath:
                                            '#/properties/body/allOf/0/properties/state/pattern',
                                          keyword: 'pattern',
                                          params: {
                                            pattern: '^[a-z][a-z0-9_]{0,63}$',
                                          },
                                        },
                                      ];
                                      return false;
                                    }
                                  } else {
                                    validate20.errors = [
                                      {
                                        instancePath:
                                          instancePath + '/body/state',
                                        schemaPath:
                                          '#/properties/body/allOf/0/properties/state/type',
                                        keyword: 'type',
                                        params: { type: 'string' },
                                      },
                                    ];
                                    return false;
                                  }
                                }
                                var valid20 = _errs56 === errors;
                              } else {
                                var valid20 = true;
                              }
                              if (valid20) {
                                if (
                                  data1.artifact !== undefined &&
                                  func0.call(data1, 'artifact')
                                ) {
                                  let data20 = data1.artifact;
                                  const _errs58 = errors;
                                  if (errors === _errs58) {
                                    if (
                                      data20 &&
                                      typeof data20 == 'object' &&
                                      !Array.isArray(data20)
                                    ) {
                                      let missing8;
                                      if (
                                        ((data20.resource_id === undefined ||
                                          !func0.call(data20, 'resource_id')) &&
                                          (missing8 = 'resource_id')) ||
                                        ((data20.digest === undefined ||
                                          !func0.call(data20, 'digest')) &&
                                          (missing8 = 'digest')) ||
                                        ((data20.size === undefined ||
                                          !func0.call(data20, 'size')) &&
                                          (missing8 = 'size'))
                                      ) {
                                        validate20.errors = [
                                          {
                                            instancePath:
                                              instancePath + '/body/artifact',
                                            schemaPath:
                                              '#/properties/body/allOf/0/properties/artifact/required',
                                            keyword: 'required',
                                            params: {
                                              missingProperty: missing8,
                                            },
                                          },
                                        ];
                                        return false;
                                      } else {
                                        const _errs60 = errors;
                                        for (const key2 of Object.keys(
                                          data20,
                                        )) {
                                          if (!(
                                            key2 === 'resource_id' ||
                                            key2 === 'digest' ||
                                            key2 === 'size'
                                          )) {
                                            validate20.errors = [
                                              {
                                                instancePath:
                                                  instancePath +
                                                  '/body/artifact',
                                                schemaPath:
                                                  '#/properties/body/allOf/0/properties/artifact/additionalProperties',
                                                keyword: 'additionalProperties',
                                                params: {
                                                  additionalProperty: key2,
                                                },
                                              },
                                            ];
                                            return false;
                                            break;
                                          }
                                        }
                                        if (_errs60 === errors) {
                                          if (
                                            data20.resource_id !== undefined &&
                                            func0.call(data20, 'resource_id')
                                          ) {
                                            let data21 = data20.resource_id;
                                            const _errs61 = errors;
                                            if (errors === _errs61) {
                                              if (typeof data21 === 'string') {
                                                if (!pattern7.test(data21)) {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/artifact/resource_id',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/artifact/properties/resource_id/pattern',
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
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/artifact/resource_id',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/artifact/properties/resource_id/type',
                                                    keyword: 'type',
                                                    params: { type: 'string' },
                                                  },
                                                ];
                                                return false;
                                              }
                                            }
                                            var valid21 = _errs61 === errors;
                                          } else {
                                            var valid21 = true;
                                          }
                                          if (valid21) {
                                            if (
                                              data20.digest !== undefined &&
                                              func0.call(data20, 'digest')
                                            ) {
                                              let data22 = data20.digest;
                                              const _errs63 = errors;
                                              if (errors === _errs63) {
                                                if (
                                                  typeof data22 === 'string'
                                                ) {
                                                  if (!pattern8.test(data22)) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/artifact/digest',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/artifact/properties/digest/pattern',
                                                        keyword: 'pattern',
                                                        params: {
                                                          pattern:
                                                            '^sha256:[0-9a-f]{64}$',
                                                        },
                                                      },
                                                    ];
                                                    return false;
                                                  }
                                                } else {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/artifact/digest',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/artifact/properties/digest/type',
                                                      keyword: 'type',
                                                      params: {
                                                        type: 'string',
                                                      },
                                                    },
                                                  ];
                                                  return false;
                                                }
                                              }
                                              var valid21 = _errs63 === errors;
                                            } else {
                                              var valid21 = true;
                                            }
                                            if (valid21) {
                                              if (
                                                data20.size !== undefined &&
                                                func0.call(data20, 'size')
                                              ) {
                                                let data23 = data20.size;
                                                const _errs65 = errors;
                                                if (errors === _errs65) {
                                                  if (
                                                    typeof data23 === 'string'
                                                  ) {
                                                    if (
                                                      !pattern5.test(data23)
                                                    ) {
                                                      validate20.errors = [
                                                        {
                                                          instancePath:
                                                            instancePath +
                                                            '/body/artifact/size',
                                                          schemaPath:
                                                            '#/properties/body/allOf/0/properties/artifact/properties/size/pattern',
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
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/artifact/size',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/artifact/properties/size/type',
                                                        keyword: 'type',
                                                        params: {
                                                          type: 'string',
                                                        },
                                                      },
                                                    ];
                                                    return false;
                                                  }
                                                }
                                                var valid21 =
                                                  _errs65 === errors;
                                              } else {
                                                var valid21 = true;
                                              }
                                            }
                                          }
                                        }
                                      }
                                    } else {
                                      validate20.errors = [
                                        {
                                          instancePath:
                                            instancePath + '/body/artifact',
                                          schemaPath:
                                            '#/properties/body/allOf/0/properties/artifact/type',
                                          keyword: 'type',
                                          params: { type: 'object' },
                                        },
                                      ];
                                      return false;
                                    }
                                  }
                                  var valid20 = _errs58 === errors;
                                } else {
                                  var valid20 = true;
                                }
                                if (valid20) {
                                  if (
                                    data1.group_address !== undefined &&
                                    func0.call(data1, 'group_address')
                                  ) {
                                    let data24 = data1.group_address;
                                    const _errs67 = errors;
                                    if (!(
                                      data24 &&
                                      typeof data24 == 'object' &&
                                      !Array.isArray(data24)
                                    )) {
                                      validate20.errors = [
                                        {
                                          instancePath:
                                            instancePath +
                                            '/body/group_address',
                                          schemaPath:
                                            '#/properties/body/allOf/0/properties/group_address/type',
                                          keyword: 'type',
                                          params: { type: 'object' },
                                        },
                                      ];
                                      return false;
                                    }
                                    const _errs69 = errors;
                                    const _errs71 = errors;
                                    const _errs72 = errors;
                                    let valid24 = true;
                                    const _errs73 = errors;
                                    if (
                                      data24 &&
                                      typeof data24 == 'object' &&
                                      !Array.isArray(data24)
                                    ) {
                                      if (
                                        data24.type !== undefined &&
                                        func0.call(data24, 'type')
                                      ) {
                                        if ('system' !== data24.type) {
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
                                    var _valid6 = _errs73 === errors;
                                    errors = _errs72;
                                    if (vErrors !== null) {
                                      if (_errs72) {
                                        vErrors.length = _errs72;
                                      } else {
                                        vErrors = null;
                                      }
                                    }
                                    if (_valid6) {
                                      const _errs75 = errors;
                                      if (
                                        data24 &&
                                        typeof data24 == 'object' &&
                                        !Array.isArray(data24)
                                      ) {
                                        if (
                                          data24.node !== undefined &&
                                          func0.call(data24, 'node')
                                        ) {
                                          let data26 = data24.node;
                                          const _errs76 = errors;
                                          if (typeof data26 !== 'string') {
                                            validate20.errors = [
                                              {
                                                instancePath:
                                                  instancePath +
                                                  '/body/group_address/node',
                                                schemaPath:
                                                  '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/0/then/properties/node/type',
                                                keyword: 'type',
                                                params: { type: 'string' },
                                              },
                                            ];
                                            return false;
                                          }
                                          const _errs78 = errors;
                                          if (errors === _errs78) {
                                            if (typeof data26 === 'string') {
                                              if (!pattern5.test(data26)) {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/node',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/0/then/properties/node/allOf/0/pattern',
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
                                              validate20.errors = [
                                                {
                                                  instancePath:
                                                    instancePath +
                                                    '/body/group_address/node',
                                                  schemaPath:
                                                    '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/0/then/properties/node/allOf/0/type',
                                                  keyword: 'type',
                                                  params: { type: 'string' },
                                                },
                                              ];
                                              return false;
                                            }
                                          }
                                          var valid27 = _errs78 === errors;
                                          if (valid27) {
                                            const _errs80 = errors;
                                            const _errs81 = errors;
                                            const _errs82 = errors;
                                            if ('0' !== data26) {
                                              const err14 = {};
                                              if (vErrors === null) {
                                                vErrors = [err14];
                                              } else {
                                                vErrors.push(err14);
                                              }
                                              errors++;
                                            }
                                            var valid28 = _errs82 === errors;
                                            if (valid28) {
                                              validate20.errors = [
                                                {
                                                  instancePath:
                                                    instancePath +
                                                    '/body/group_address/node',
                                                  schemaPath:
                                                    '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/0/then/properties/node/allOf/1/not',
                                                  keyword: 'not',
                                                  params: {},
                                                },
                                              ];
                                              return false;
                                            } else {
                                              errors = _errs81;
                                              if (vErrors !== null) {
                                                if (_errs81) {
                                                  vErrors.length = _errs81;
                                                } else {
                                                  vErrors = null;
                                                }
                                              }
                                            }
                                            var valid27 = _errs80 === errors;
                                          }
                                          var valid26 = _errs76 === errors;
                                        } else {
                                          var valid26 = true;
                                        }
                                        if (valid26) {
                                          if (
                                            data24.local !== undefined &&
                                            func0.call(data24, 'local')
                                          ) {
                                            const _errs83 = errors;
                                            if ('0' !== data24.local) {
                                              validate20.errors = [
                                                {
                                                  instancePath:
                                                    instancePath +
                                                    '/body/group_address/local',
                                                  schemaPath:
                                                    '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/0/then/properties/local/const',
                                                  keyword: 'const',
                                                  params: { allowedValue: '0' },
                                                },
                                              ];
                                              return false;
                                            }
                                            var valid26 = _errs83 === errors;
                                          } else {
                                            var valid26 = true;
                                          }
                                        }
                                      }
                                      var _valid6 = _errs75 === errors;
                                      valid24 = _valid6;
                                      if (valid24) {
                                        var props6 = {};
                                        props6.node = true;
                                        props6.local = true;
                                        props6.type = true;
                                      }
                                    }
                                    if (!valid24) {
                                      const err15 = {
                                        instancePath:
                                          instancePath + '/body/group_address',
                                        schemaPath:
                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/0/if',
                                        keyword: 'if',
                                        params: { failingKeyword: 'then' },
                                      };
                                      if (vErrors === null) {
                                        vErrors = [err15];
                                      } else {
                                        vErrors.push(err15);
                                      }
                                      errors++;
                                      validate20.errors = vErrors;
                                      return false;
                                    }
                                    var valid23 = _errs71 === errors;
                                    if (valid23) {
                                      const _errs84 = errors;
                                      const _errs85 = errors;
                                      let valid29 = true;
                                      const _errs86 = errors;
                                      if (
                                        data24 &&
                                        typeof data24 == 'object' &&
                                        !Array.isArray(data24)
                                      ) {
                                        if (
                                          data24.type !== undefined &&
                                          func0.call(data24, 'type')
                                        ) {
                                          let data28 = data24.type;
                                          if (!(
                                            data28 === 'session' ||
                                            data28 === 'subagent'
                                          )) {
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
                                      var _valid7 = _errs86 === errors;
                                      errors = _errs85;
                                      if (vErrors !== null) {
                                        if (_errs85) {
                                          vErrors.length = _errs85;
                                        } else {
                                          vErrors = null;
                                        }
                                      }
                                      if (_valid7) {
                                        const _errs88 = errors;
                                        if (
                                          data24 &&
                                          typeof data24 == 'object' &&
                                          !Array.isArray(data24)
                                        ) {
                                          if (
                                            data24.node !== undefined &&
                                            func0.call(data24, 'node')
                                          ) {
                                            let data29 = data24.node;
                                            const _errs89 = errors;
                                            if (typeof data29 !== 'string') {
                                              validate20.errors = [
                                                {
                                                  instancePath:
                                                    instancePath +
                                                    '/body/group_address/node',
                                                  schemaPath:
                                                    '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/node/type',
                                                  keyword: 'type',
                                                  params: { type: 'string' },
                                                },
                                              ];
                                              return false;
                                            }
                                            const _errs91 = errors;
                                            if (errors === _errs91) {
                                              if (typeof data29 === 'string') {
                                                if (!pattern5.test(data29)) {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/group_address/node',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/node/allOf/0/pattern',
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
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/node',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/node/allOf/0/type',
                                                    keyword: 'type',
                                                    params: { type: 'string' },
                                                  },
                                                ];
                                                return false;
                                              }
                                            }
                                            var valid32 = _errs91 === errors;
                                            if (valid32) {
                                              const _errs93 = errors;
                                              const _errs94 = errors;
                                              const _errs95 = errors;
                                              if ('0' !== data29) {
                                                const err17 = {};
                                                if (vErrors === null) {
                                                  vErrors = [err17];
                                                } else {
                                                  vErrors.push(err17);
                                                }
                                                errors++;
                                              }
                                              var valid33 = _errs95 === errors;
                                              if (valid33) {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/node',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/node/allOf/1/not',
                                                    keyword: 'not',
                                                    params: {},
                                                  },
                                                ];
                                                return false;
                                              } else {
                                                errors = _errs94;
                                                if (vErrors !== null) {
                                                  if (_errs94) {
                                                    vErrors.length = _errs94;
                                                  } else {
                                                    vErrors = null;
                                                  }
                                                }
                                              }
                                              var valid32 = _errs93 === errors;
                                            }
                                            var valid31 = _errs89 === errors;
                                          } else {
                                            var valid31 = true;
                                          }
                                          if (valid31) {
                                            if (
                                              data24.local !== undefined &&
                                              func0.call(data24, 'local')
                                            ) {
                                              let data30 = data24.local;
                                              const _errs96 = errors;
                                              if (typeof data30 !== 'string') {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/local',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/local/type',
                                                    keyword: 'type',
                                                    params: { type: 'string' },
                                                  },
                                                ];
                                                return false;
                                              }
                                              const _errs98 = errors;
                                              if (errors === _errs98) {
                                                if (
                                                  typeof data30 === 'string'
                                                ) {
                                                  if (!pattern5.test(data30)) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/local',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/local/allOf/0/pattern',
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
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/group_address/local',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/local/allOf/0/type',
                                                      keyword: 'type',
                                                      params: {
                                                        type: 'string',
                                                      },
                                                    },
                                                  ];
                                                  return false;
                                                }
                                              }
                                              var valid34 = _errs98 === errors;
                                              if (valid34) {
                                                const _errs100 = errors;
                                                const _errs101 = errors;
                                                const _errs102 = errors;
                                                if ('0' !== data30) {
                                                  const err18 = {};
                                                  if (vErrors === null) {
                                                    vErrors = [err18];
                                                  } else {
                                                    vErrors.push(err18);
                                                  }
                                                  errors++;
                                                }
                                                var valid35 =
                                                  _errs102 === errors;
                                                if (valid35) {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/group_address/local',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/then/properties/local/allOf/1/not',
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
                                                var valid34 =
                                                  _errs100 === errors;
                                              }
                                              var valid31 = _errs96 === errors;
                                            } else {
                                              var valid31 = true;
                                            }
                                          }
                                        }
                                        var _valid7 = _errs88 === errors;
                                        valid29 = _valid7;
                                        if (valid29) {
                                          var props7 = {};
                                          props7.node = true;
                                          props7.local = true;
                                          props7.type = true;
                                        }
                                      }
                                      if (!valid29) {
                                        const err19 = {
                                          instancePath:
                                            instancePath +
                                            '/body/group_address',
                                          schemaPath:
                                            '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/1/if',
                                          keyword: 'if',
                                          params: { failingKeyword: 'then' },
                                        };
                                        if (vErrors === null) {
                                          vErrors = [err19];
                                        } else {
                                          vErrors.push(err19);
                                        }
                                        errors++;
                                        validate20.errors = vErrors;
                                        return false;
                                      }
                                      var valid23 = _errs84 === errors;
                                      if (valid23) {
                                        if (
                                          props6 !== true &&
                                          props7 !== undefined
                                        ) {
                                          if (props7 === true) {
                                            props6 = true;
                                          } else {
                                            props6 = props6 || {};
                                            Object.assign(props6, props7);
                                          }
                                        }
                                        const _errs103 = errors;
                                        const _errs104 = errors;
                                        let valid36 = true;
                                        const _errs105 = errors;
                                        if (
                                          data24 &&
                                          typeof data24 == 'object' &&
                                          !Array.isArray(data24)
                                        ) {
                                          if (
                                            data24.type !== undefined &&
                                            func0.call(data24, 'type')
                                          ) {
                                            if ('group' !== data24.type) {
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
                                        var _valid8 = _errs105 === errors;
                                        errors = _errs104;
                                        if (vErrors !== null) {
                                          if (_errs104) {
                                            vErrors.length = _errs104;
                                          } else {
                                            vErrors = null;
                                          }
                                        }
                                        if (_valid8) {
                                          const _errs107 = errors;
                                          if (
                                            data24 &&
                                            typeof data24 == 'object' &&
                                            !Array.isArray(data24)
                                          ) {
                                            if (
                                              data24.node !== undefined &&
                                              func0.call(data24, 'node')
                                            ) {
                                              const _errs108 = errors;
                                              if ('0' !== data24.node) {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/node',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/2/then/properties/node/const',
                                                    keyword: 'const',
                                                    params: {
                                                      allowedValue: '0',
                                                    },
                                                  },
                                                ];
                                                return false;
                                              }
                                              var valid38 = _errs108 === errors;
                                            } else {
                                              var valid38 = true;
                                            }
                                            if (valid38) {
                                              if (
                                                data24.local !== undefined &&
                                                func0.call(data24, 'local')
                                              ) {
                                                let data33 = data24.local;
                                                const _errs109 = errors;
                                                if (
                                                  typeof data33 !== 'string'
                                                ) {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/group_address/local',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/2/then/properties/local/type',
                                                      keyword: 'type',
                                                      params: {
                                                        type: 'string',
                                                      },
                                                    },
                                                  ];
                                                  return false;
                                                }
                                                const _errs111 = errors;
                                                if (errors === _errs111) {
                                                  if (
                                                    typeof data33 === 'string'
                                                  ) {
                                                    if (
                                                      !pattern5.test(data33)
                                                    ) {
                                                      validate20.errors = [
                                                        {
                                                          instancePath:
                                                            instancePath +
                                                            '/body/group_address/local',
                                                          schemaPath:
                                                            '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/2/then/properties/local/allOf/0/pattern',
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
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/local',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/2/then/properties/local/allOf/0/type',
                                                        keyword: 'type',
                                                        params: {
                                                          type: 'string',
                                                        },
                                                      },
                                                    ];
                                                    return false;
                                                  }
                                                }
                                                var valid39 =
                                                  _errs111 === errors;
                                                if (valid39) {
                                                  const _errs113 = errors;
                                                  const _errs114 = errors;
                                                  const _errs115 = errors;
                                                  if ('0' !== data33) {
                                                    const err21 = {};
                                                    if (vErrors === null) {
                                                      vErrors = [err21];
                                                    } else {
                                                      vErrors.push(err21);
                                                    }
                                                    errors++;
                                                  }
                                                  var valid40 =
                                                    _errs115 === errors;
                                                  if (valid40) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/local',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/2/then/properties/local/allOf/1/not',
                                                        keyword: 'not',
                                                        params: {},
                                                      },
                                                    ];
                                                    return false;
                                                  } else {
                                                    errors = _errs114;
                                                    if (vErrors !== null) {
                                                      if (_errs114) {
                                                        vErrors.length =
                                                          _errs114;
                                                      } else {
                                                        vErrors = null;
                                                      }
                                                    }
                                                  }
                                                  var valid39 =
                                                    _errs113 === errors;
                                                }
                                                var valid38 =
                                                  _errs109 === errors;
                                              } else {
                                                var valid38 = true;
                                              }
                                            }
                                          }
                                          var _valid8 = _errs107 === errors;
                                          valid36 = _valid8;
                                          if (valid36) {
                                            var props8 = {};
                                            props8.node = true;
                                            props8.local = true;
                                            props8.type = true;
                                          }
                                        }
                                        if (!valid36) {
                                          const err22 = {
                                            instancePath:
                                              instancePath +
                                              '/body/group_address',
                                            schemaPath:
                                              '#/properties/body/allOf/0/properties/group_address/allOf/0/allOf/2/if',
                                            keyword: 'if',
                                            params: { failingKeyword: 'then' },
                                          };
                                          if (vErrors === null) {
                                            vErrors = [err22];
                                          } else {
                                            vErrors.push(err22);
                                          }
                                          errors++;
                                          validate20.errors = vErrors;
                                          return false;
                                        }
                                        var valid23 = _errs103 === errors;
                                        if (valid23) {
                                          if (
                                            props6 !== true &&
                                            props8 !== undefined
                                          ) {
                                            if (props8 === true) {
                                              props6 = true;
                                            } else {
                                              props6 = props6 || {};
                                              Object.assign(props6, props8);
                                            }
                                          }
                                        }
                                      }
                                    }
                                    if (errors === _errs69) {
                                      if (
                                        data24 &&
                                        typeof data24 == 'object' &&
                                        !Array.isArray(data24)
                                      ) {
                                        let missing9;
                                        if (
                                          ((data24.version === undefined ||
                                            !func0.call(data24, 'version')) &&
                                            (missing9 = 'version')) ||
                                          ((data24.realm === undefined ||
                                            !func0.call(data24, 'realm')) &&
                                            (missing9 = 'realm')) ||
                                          ((data24.type === undefined ||
                                            !func0.call(data24, 'type')) &&
                                            (missing9 = 'type')) ||
                                          ((data24.node === undefined ||
                                            !func0.call(data24, 'node')) &&
                                            (missing9 = 'node')) ||
                                          ((data24.local === undefined ||
                                            !func0.call(data24, 'local')) &&
                                            (missing9 = 'local'))
                                        ) {
                                          validate20.errors = [
                                            {
                                              instancePath:
                                                instancePath +
                                                '/body/group_address',
                                              schemaPath:
                                                '#/properties/body/allOf/0/properties/group_address/allOf/0/required',
                                              keyword: 'required',
                                              params: {
                                                missingProperty: missing9,
                                              },
                                            },
                                          ];
                                          return false;
                                        } else {
                                          const _errs116 = errors;
                                          for (const key3 of Object.keys(
                                            data24,
                                          )) {
                                            if (!(
                                              key3 === 'version' ||
                                              key3 === 'realm' ||
                                              key3 === 'type' ||
                                              key3 === 'node' ||
                                              key3 === 'local'
                                            )) {
                                              validate20.errors = [
                                                {
                                                  instancePath:
                                                    instancePath +
                                                    '/body/group_address',
                                                  schemaPath:
                                                    '#/properties/body/allOf/0/properties/group_address/allOf/0/additionalProperties',
                                                  keyword:
                                                    'additionalProperties',
                                                  params: {
                                                    additionalProperty: key3,
                                                  },
                                                },
                                              ];
                                              return false;
                                              break;
                                            }
                                          }
                                          if (_errs116 === errors) {
                                            if (
                                              data24.version !== undefined &&
                                              func0.call(data24, 'version')
                                            ) {
                                              let data34 = data24.version;
                                              const _errs117 = errors;
                                              if (!(
                                                typeof data34 == 'number' &&
                                                !(data34 % 1) &&
                                                !isNaN(data34)
                                              )) {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/version',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/version/type',
                                                    keyword: 'type',
                                                    params: { type: 'integer' },
                                                  },
                                                ];
                                                return false;
                                              }
                                              if (1 !== data34) {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/group_address/version',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/version/const',
                                                    keyword: 'const',
                                                    params: { allowedValue: 1 },
                                                  },
                                                ];
                                                return false;
                                              }
                                              var valid41 = _errs117 === errors;
                                            } else {
                                              var valid41 = true;
                                            }
                                            if (valid41) {
                                              if (
                                                data24.realm !== undefined &&
                                                func0.call(data24, 'realm')
                                              ) {
                                                let data35 = data24.realm;
                                                const _errs119 = errors;
                                                if (
                                                  typeof data35 !== 'string'
                                                ) {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/group_address/realm',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/realm/type',
                                                      keyword: 'type',
                                                      params: {
                                                        type: 'string',
                                                      },
                                                    },
                                                  ];
                                                  return false;
                                                }
                                                const _errs121 = errors;
                                                if (errors === _errs121) {
                                                  if (
                                                    typeof data35 === 'string'
                                                  ) {
                                                    if (
                                                      !pattern5.test(data35)
                                                    ) {
                                                      validate20.errors = [
                                                        {
                                                          instancePath:
                                                            instancePath +
                                                            '/body/group_address/realm',
                                                          schemaPath:
                                                            '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/realm/allOf/0/pattern',
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
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/realm',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/realm/allOf/0/type',
                                                        keyword: 'type',
                                                        params: {
                                                          type: 'string',
                                                        },
                                                      },
                                                    ];
                                                    return false;
                                                  }
                                                }
                                                var valid42 =
                                                  _errs121 === errors;
                                                if (valid42) {
                                                  const _errs123 = errors;
                                                  const _errs124 = errors;
                                                  const _errs125 = errors;
                                                  if ('0' !== data35) {
                                                    const err23 = {};
                                                    if (vErrors === null) {
                                                      vErrors = [err23];
                                                    } else {
                                                      vErrors.push(err23);
                                                    }
                                                    errors++;
                                                  }
                                                  var valid43 =
                                                    _errs125 === errors;
                                                  if (valid43) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/realm',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/realm/allOf/1/not',
                                                        keyword: 'not',
                                                        params: {},
                                                      },
                                                    ];
                                                    return false;
                                                  } else {
                                                    errors = _errs124;
                                                    if (vErrors !== null) {
                                                      if (_errs124) {
                                                        vErrors.length =
                                                          _errs124;
                                                      } else {
                                                        vErrors = null;
                                                      }
                                                    }
                                                  }
                                                  var valid42 =
                                                    _errs123 === errors;
                                                }
                                                var valid41 =
                                                  _errs119 === errors;
                                              } else {
                                                var valid41 = true;
                                              }
                                              if (valid41) {
                                                if (
                                                  data24.type !== undefined &&
                                                  func0.call(data24, 'type')
                                                ) {
                                                  let data36 = data24.type;
                                                  const _errs126 = errors;
                                                  if (
                                                    typeof data36 !== 'string'
                                                  ) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/type',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/type/type',
                                                        keyword: 'type',
                                                        params: {
                                                          type: 'string',
                                                        },
                                                      },
                                                    ];
                                                    return false;
                                                  }
                                                  if (!(
                                                    data36 === 'system' ||
                                                    data36 === 'session' ||
                                                    data36 === 'subagent' ||
                                                    data36 === 'group'
                                                  )) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/group_address/type',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/type/enum',
                                                        keyword: 'enum',
                                                        params: {
                                                          allowedValues:
                                                            schema31.properties
                                                              .body.allOf[0]
                                                              .properties
                                                              .group_address
                                                              .allOf[0]
                                                              .properties.type
                                                              .enum,
                                                        },
                                                      },
                                                    ];
                                                    return false;
                                                  }
                                                  var valid41 =
                                                    _errs126 === errors;
                                                } else {
                                                  var valid41 = true;
                                                }
                                                if (valid41) {
                                                  if (
                                                    data24.node !== undefined &&
                                                    func0.call(data24, 'node')
                                                  ) {
                                                    let data37 = data24.node;
                                                    const _errs128 = errors;
                                                    if (errors === _errs128) {
                                                      if (
                                                        typeof data37 ===
                                                        'string'
                                                      ) {
                                                        if (
                                                          !pattern5.test(data37)
                                                        ) {
                                                          validate20.errors = [
                                                            {
                                                              instancePath:
                                                                instancePath +
                                                                '/body/group_address/node',
                                                              schemaPath:
                                                                '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/node/pattern',
                                                              keyword:
                                                                'pattern',
                                                              params: {
                                                                pattern:
                                                                  '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                                              },
                                                            },
                                                          ];
                                                          return false;
                                                        }
                                                      } else {
                                                        validate20.errors = [
                                                          {
                                                            instancePath:
                                                              instancePath +
                                                              '/body/group_address/node',
                                                            schemaPath:
                                                              '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/node/type',
                                                            keyword: 'type',
                                                            params: {
                                                              type: 'string',
                                                            },
                                                          },
                                                        ];
                                                        return false;
                                                      }
                                                    }
                                                    var valid41 =
                                                      _errs128 === errors;
                                                  } else {
                                                    var valid41 = true;
                                                  }
                                                  if (valid41) {
                                                    if (
                                                      data24.local !==
                                                        undefined &&
                                                      func0.call(
                                                        data24,
                                                        'local',
                                                      )
                                                    ) {
                                                      let data38 = data24.local;
                                                      const _errs130 = errors;
                                                      if (errors === _errs130) {
                                                        if (
                                                          typeof data38 ===
                                                          'string'
                                                        ) {
                                                          if (
                                                            !pattern5.test(
                                                              data38,
                                                            )
                                                          ) {
                                                            validate20.errors =
                                                              [
                                                                {
                                                                  instancePath:
                                                                    instancePath +
                                                                    '/body/group_address/local',
                                                                  schemaPath:
                                                                    '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/local/pattern',
                                                                  keyword:
                                                                    'pattern',
                                                                  params: {
                                                                    pattern:
                                                                      '^(?:0|[1-9][0-9]{0,18}|1[0-7][0-9]{18}|18[0-3][0-9]{17}|184[0-3][0-9]{16}|1844[0-5][0-9]{15}|18446[0-6][0-9]{14}|184467[0-3][0-9]{13}|1844674[0-3][0-9]{12}|184467440[0-6][0-9]{10}|1844674407[0-2][0-9]{9}|18446744073[0-6][0-9]{8}|1844674407370[0-8][0-9]{6}|18446744073709[0-4][0-9]{5}|184467440737095[0-4][0-9]{4}|18446744073709550[0-9]{3}|18446744073709551[0-5][0-9]{2}|1844674407370955160[0-9]{1}|1844674407370955161[0-4]|18446744073709551615)$',
                                                                  },
                                                                },
                                                              ];
                                                            return false;
                                                          }
                                                        } else {
                                                          validate20.errors = [
                                                            {
                                                              instancePath:
                                                                instancePath +
                                                                '/body/group_address/local',
                                                              schemaPath:
                                                                '#/properties/body/allOf/0/properties/group_address/allOf/0/properties/local/type',
                                                              keyword: 'type',
                                                              params: {
                                                                type: 'string',
                                                              },
                                                            },
                                                          ];
                                                          return false;
                                                        }
                                                      }
                                                      var valid41 =
                                                        _errs130 === errors;
                                                    } else {
                                                      var valid41 = true;
                                                    }
                                                  }
                                                }
                                              }
                                            }
                                          }
                                        }
                                      } else {
                                        validate20.errors = [
                                          {
                                            instancePath:
                                              instancePath +
                                              '/body/group_address',
                                            schemaPath:
                                              '#/properties/body/allOf/0/properties/group_address/allOf/0/type',
                                            keyword: 'type',
                                            params: { type: 'object' },
                                          },
                                        ];
                                        return false;
                                      }
                                    }
                                    var valid22 = _errs69 === errors;
                                    if (valid22) {
                                      const _errs132 = errors;
                                      if (
                                        data24 &&
                                        typeof data24 == 'object' &&
                                        !Array.isArray(data24)
                                      ) {
                                        if (
                                          data24.type !== undefined &&
                                          func0.call(data24, 'type')
                                        ) {
                                          if ('group' !== data24.type) {
                                            validate20.errors = [
                                              {
                                                instancePath:
                                                  instancePath +
                                                  '/body/group_address/type',
                                                schemaPath:
                                                  '#/properties/body/allOf/0/properties/group_address/allOf/1/properties/type/const',
                                                keyword: 'const',
                                                params: {
                                                  allowedValue: 'group',
                                                },
                                              },
                                            ];
                                            return false;
                                          }
                                        }
                                      }
                                      var valid22 = _errs132 === errors;
                                    }
                                    var valid20 = _errs67 === errors;
                                  } else {
                                    var valid20 = true;
                                  }
                                  if (valid20) {
                                    if (
                                      data1.invitation_id !== undefined &&
                                      func0.call(data1, 'invitation_id')
                                    ) {
                                      let data40 = data1.invitation_id;
                                      const _errs134 = errors;
                                      if (errors === _errs134) {
                                        if (typeof data40 === 'string') {
                                          if (!pattern7.test(data40)) {
                                            validate20.errors = [
                                              {
                                                instancePath:
                                                  instancePath +
                                                  '/body/invitation_id',
                                                schemaPath:
                                                  '#/properties/body/allOf/0/properties/invitation_id/pattern',
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
                                          validate20.errors = [
                                            {
                                              instancePath:
                                                instancePath +
                                                '/body/invitation_id',
                                              schemaPath:
                                                '#/properties/body/allOf/0/properties/invitation_id/type',
                                              keyword: 'type',
                                              params: { type: 'string' },
                                            },
                                          ];
                                          return false;
                                        }
                                      }
                                      var valid20 = _errs134 === errors;
                                    } else {
                                      var valid20 = true;
                                    }
                                    if (valid20) {
                                      if (
                                        data1.invite_expires_at !== undefined &&
                                        func0.call(data1, 'invite_expires_at')
                                      ) {
                                        let data41 = data1.invite_expires_at;
                                        const _errs136 = errors;
                                        if (errors === _errs136) {
                                          if (errors === _errs136) {
                                            if (typeof data41 === 'string') {
                                              if (!pattern18.test(data41)) {
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/invite_expires_at',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/invite_expires_at/pattern',
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
                                              validate20.errors = [
                                                {
                                                  instancePath:
                                                    instancePath +
                                                    '/body/invite_expires_at',
                                                  schemaPath:
                                                    '#/properties/body/allOf/0/properties/invite_expires_at/type',
                                                  keyword: 'type',
                                                  params: { type: 'string' },
                                                },
                                              ];
                                              return false;
                                            }
                                          }
                                        }
                                        var valid20 = _errs136 === errors;
                                      } else {
                                        var valid20 = true;
                                      }
                                      if (valid20) {
                                        if (
                                          data1.action !== undefined &&
                                          func0.call(data1, 'action')
                                        ) {
                                          let data42 = data1.action;
                                          const _errs138 = errors;
                                          if (typeof data42 !== 'string') {
                                            validate20.errors = [
                                              {
                                                instancePath:
                                                  instancePath + '/body/action',
                                                schemaPath:
                                                  '#/properties/body/allOf/0/properties/action/type',
                                                keyword: 'type',
                                                params: { type: 'string' },
                                              },
                                            ];
                                            return false;
                                          }
                                          if (!(
                                            data42 === 'wake' ||
                                            data42 === 'cancel' ||
                                            data42 === 'queue' ||
                                            data42 === 'steer' ||
                                            data42 === 'start'
                                          )) {
                                            validate20.errors = [
                                              {
                                                instancePath:
                                                  instancePath + '/body/action',
                                                schemaPath:
                                                  '#/properties/body/allOf/0/properties/action/enum',
                                                keyword: 'enum',
                                                params: {
                                                  allowedValues:
                                                    schema31.properties.body
                                                      .allOf[0].properties
                                                      .action.enum,
                                                },
                                              },
                                            ];
                                            return false;
                                          }
                                          var valid20 = _errs138 === errors;
                                        } else {
                                          var valid20 = true;
                                        }
                                        if (valid20) {
                                          if (
                                            data1.target_operation_id !==
                                              undefined &&
                                            func0.call(
                                              data1,
                                              'target_operation_id',
                                            )
                                          ) {
                                            let data43 =
                                              data1.target_operation_id;
                                            const _errs140 = errors;
                                            if (errors === _errs140) {
                                              if (typeof data43 === 'string') {
                                                if (!pattern7.test(data43)) {
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/target_operation_id',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/target_operation_id/pattern',
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
                                                validate20.errors = [
                                                  {
                                                    instancePath:
                                                      instancePath +
                                                      '/body/target_operation_id',
                                                    schemaPath:
                                                      '#/properties/body/allOf/0/properties/target_operation_id/type',
                                                    keyword: 'type',
                                                    params: { type: 'string' },
                                                  },
                                                ];
                                                return false;
                                              }
                                            }
                                            var valid20 = _errs140 === errors;
                                          } else {
                                            var valid20 = true;
                                          }
                                          if (valid20) {
                                            if (
                                              data1.expected_state_version !==
                                                undefined &&
                                              func0.call(
                                                data1,
                                                'expected_state_version',
                                              )
                                            ) {
                                              let data44 =
                                                data1.expected_state_version;
                                              const _errs142 = errors;
                                              if (errors === _errs142) {
                                                if (
                                                  typeof data44 === 'string'
                                                ) {
                                                  if (!pattern5.test(data44)) {
                                                    validate20.errors = [
                                                      {
                                                        instancePath:
                                                          instancePath +
                                                          '/body/expected_state_version',
                                                        schemaPath:
                                                          '#/properties/body/allOf/0/properties/expected_state_version/pattern',
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
                                                  validate20.errors = [
                                                    {
                                                      instancePath:
                                                        instancePath +
                                                        '/body/expected_state_version',
                                                      schemaPath:
                                                        '#/properties/body/allOf/0/properties/expected_state_version/type',
                                                      keyword: 'type',
                                                      params: {
                                                        type: 'string',
                                                      },
                                                    },
                                                  ];
                                                  return false;
                                                }
                                              }
                                              var valid20 = _errs142 === errors;
                                            } else {
                                              var valid20 = true;
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
                    }
                  }
                } else {
                  validate20.errors = [
                    {
                      instancePath: instancePath + '/body',
                      schemaPath: '#/properties/body/allOf/0/type',
                      keyword: 'type',
                      params: { type: 'object' },
                    },
                  ];
                  return false;
                }
              }
              var valid1 = _errs6 === errors;
              if (valid1) {
                const _errs144 = errors;
                if (
                  data1 &&
                  typeof data1 == 'object' &&
                  !Array.isArray(data1)
                ) {
                  if (
                    data1.subtype !== undefined &&
                    func0.call(data1, 'subtype')
                  ) {
                    let data45 = data1.subtype;
                    const _errs145 = errors;
                    if (!(
                      data45 === 'text' ||
                      data45 === 'progress' ||
                      data45 === 'status' ||
                      data45 === 'artifact_ref'
                    )) {
                      validate20.errors = [
                        {
                          instancePath: instancePath + '/body/subtype',
                          schemaPath:
                            '#/properties/body/allOf/1/properties/subtype/enum',
                          keyword: 'enum',
                          params: {
                            allowedValues:
                              schema31.properties.body.allOf[1].properties
                                .subtype.enum,
                          },
                        },
                      ];
                      return false;
                    }
                    var valid45 = _errs145 === errors;
                  } else {
                    var valid45 = true;
                  }
                  if (valid45) {
                    if (
                      data1.provenance !== undefined &&
                      func0.call(data1, 'provenance')
                    ) {
                      var valid45 = false;
                      validate20.errors = [
                        {
                          instancePath: instancePath + '/body/provenance',
                          schemaPath:
                            '#/properties/body/allOf/1/properties/provenance/false schema',
                          keyword: 'false schema',
                          params: {},
                        },
                      ];
                      return false;
                    } else {
                      var valid45 = true;
                    }
                  }
                }
                var valid1 = _errs144 === errors;
              }
              var valid0 = _errs4 === errors;
            } else {
              var valid0 = true;
            }
            if (valid0) {
              if (data.thread !== undefined && func0.call(data, 'thread')) {
                let data47 = data.thread;
                const _errs146 = errors;
                if (errors === _errs146) {
                  if (typeof data47 === 'string') {
                    if (!pattern7.test(data47)) {
                      validate20.errors = [
                        {
                          instancePath: instancePath + '/thread',
                          schemaPath: '#/properties/thread/pattern',
                          keyword: 'pattern',
                          params: {
                            pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$',
                          },
                        },
                      ];
                      return false;
                    }
                  } else {
                    validate20.errors = [
                      {
                        instancePath: instancePath + '/thread',
                        schemaPath: '#/properties/thread/type',
                        keyword: 'type',
                        params: { type: 'string' },
                      },
                    ];
                    return false;
                  }
                }
                var valid0 = _errs146 === errors;
              } else {
                var valid0 = true;
              }
            }
          }
        }
      }
    } else {
      validate20.errors = [
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
  validate20.errors = vErrors;
  return errors === 0;
}
validate20.evaluated = {
  props: true,
  dynamicProps: false,
  dynamicItems: false,
};
