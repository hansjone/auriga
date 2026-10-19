# 0015 契约优先

- 状态：已采纳
- 日期：2026-10-19

## 背景

多语言、多人（多个 AI 编码助手）并行开发，必须有一份大家都遵守的接口定义。

## 决定

`contracts/` 是接口的唯一来源：OpenAPI 3.1（对外 REST）、protobuf（内部 gRPC，buf 管理）、JSON Schema（事件、身份源步骤协议、插件协议）。Java、Go、TypeScript 类型都从契约生成，不手写。契约在一个大版本内只做兼容修改，CI 跑 `buf breaking` 和 OpenAPI diff；不兼容的修改要升大版本。

## 后果

改接口先改契约；实现和契约不一致时以契约为准。
