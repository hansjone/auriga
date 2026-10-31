# 在 DSH 中使用团队中转站模型

先向管理员取得平台地址、允许的模型或池名称、你自己的 LiteLLM 虚拟 Key，
以及这些模型的上下文和输出上限。管理员在 Auriga 管理台打开“模型”，
为成员签发独立 Key：`user_id` 绑定 `<tenantId>:<accountId>`，设置模型范围、
预算、周期、到期时间和 RPM/TPM，再通过安全渠道交付成员。当前管理入口需要
管理员权限；成员通过管理员取得凭据。每人、每台设备可使用不同 Key，便于吊销和计费。

在 DSH 的原生模型设置中添加 OpenAI 兼容提供方，Base URL 使用
`https://portal.example.com/ai/v1`，替换为管理员提供的平台地址。
在 DSH 原生凭据设置保存 Key，取引用名 `AURIGA_MEMBER_RELAY_KEY`；
模型配置只保存该引用名。不要将 Key 填进 URL、模型名、对话、工单或截图。
Auriga 登录凭据、安装签名材料和 LiteLLM 管理 Key 不能用于此处。

DSH 的 `llm-pi-ai` 设置节支持下面的提供方配置。示例模型名和容量均须替换为
管理员核准的值；保留已有的其他提供方，在 `providers` 下增加一个新名称即可。

```json
{
  "providers": {
    "team-relay": {
      "displayName": "团队中转站",
      "api": "openai-completions",
      "compat": { "supportsStore": false },
      "apiKeyEnv": "AURIGA_MEMBER_RELAY_KEY",
      "baseURL": "https://portal.example.com/ai/v1",
      "models": [
        {
          "id": "team-pool",
          "contextWindow": 32768,
          "maxTokens": 2048
        }
      ]
    }
  }
}
```

保存后新建对话，在模型选择器选择“团队中转站 / team-pool”，发送一句简短消息。
兼容项 `supportsStore:false` 表示不向中转站发送响应存储控制字段；中转站当前不开放此项。
确认收到新回复，再让管理员在 LiteLLM 按该成员的 Key 查看新增请求和非零 token 用量。
使用池名称时，上游选择和故障切换由 LiteLLM 的池配置负责；成员不填供应商 Key 或上游 URL。
管理员应先验证实际计价和预算拒绝，再开放长期额度。

Auriga 托管的云端 DSH 默认带 `Auriga` 模型提供方，成员有权限时由平台下发实例绑定的模型 Key；正常使用无需手工保存这份 Key。
云端原生凭据仍由用户管理，已有个人 Key、网页搜索 Key 和 OAuth 记录继续可用，也可通过原生凭据设置另存个人 Key；平台不会接管这份用户存储。
手工配置个人中转站提供方时使用上面的原生模型设置和独立成员 Key。托管提供方使用平台专用 profile，不将托管补丁叠到已有个人 profile。
已知并接受：仅实例主人经 `settings/describe` 可见托管提供方求值后的 Authorization，属 MANAGED-N1 同机边界，跟踪 backlog **B-MM-DESCRIBE**；详见[托管模型设计](../design/managed-model-credential.zh.md)。
部署方须先确认托管容器能访问
所提供的中转站地址，包括 DNS、TLS 和隔离网络路由；桌面可以访问该地址不代表云端也可达。
纯 IP/HTTP 部署只在管理员已接受该部署的明文传输边界时使用
`http://192.0.2.10:8080/ai/v1`，不要关闭 HTTPS 证书校验来排除连接错误。

如果返回 401/403，核对 Key、模型授权和到期时间；429 可能是限速或预算拒绝，
按明确错误类型请管理员处理。换 Key 时只更新对应原生凭据引用；设备丢失或不再使用时
由管理员吊销这台设备的 Key。团队插件登录与原生模型 Key 分别管理，退出插件不会自动吊销 Key。

自动验收可在独立 HOME/profile 中通过 DSH 公开的 `credentials/set`、
`settings/describe` 和 `settings/mutate` 设置唯一临时提供方，再由真实
`session/create`、`session/selectModel`、`session/prompt` 生成回复。
验收后撤销测试 Key、删除临时提供方和凭据，保留已有用户配置。
