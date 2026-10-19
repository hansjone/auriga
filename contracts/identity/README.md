# 身份源契约 v1

设计见 `docs/architecture/identity.zh.md`。

| 文件 | 内容 |
|---|---|
| `steps.schema.json` | 步骤：连接器返回的 `ConnectorStep`、发给登录页的 `ClientStep`、登录页提交的 `StepInput`、外部身份 `ExternalProfile` |
| `connector.schema.json` | 连接器 SPI 的数据结构：描述、上下文、begin / advance、配置校验、可选能力 |
| `remote-connector.yaml` | 远程连接器的 HTTP 接口（任意语言实现） |

Java SPI（`io.github.hansjone.auriga.iam.spi`，由 B2 实现）和插件离线版的 JS 连接器接口，都用这里的数据结构，语义相同。

## 一次登录

```
登录页                     iam 核心                          连接器
  │ POST /login/transactions  │                                  │
  │──────────────────────────▶│ begin(ctx{state:null}, config)   │
  │                           │─────────────────────────────────▶│
  │                           │◀──── {step: redirect, state} ────│
  │◀──── ClientStep redirect ─│ （state 加密存进 login_tx）       │
  │ …外部系统… → GET /login/callback/{providerId}                 │
  │                           │ advance(ctx{state}, input:callback)
  │                           │─────────────────────────────────▶│
  │                           │◀──── {step: done(profile)} ──────│
  │                           │ 找身份 → 准入 → 多因素 → 同步属性 → 发会话
  │◀── ClientStep complete ───│ （可能先插入 form / webauthn 步骤）│
```

## 规则

1. **连接器无状态**：需要跨步骤保存的东西（state、nonce、PKCE verifier、二维码挑战）放进返回的 `state`，平台加密保存后在下次调用时原样放回 `ctx.state`（最大 16 KB）。
2. **跳转类**：一律用 `state`、`nonce` 和 PKCE；回调地址用 `ctx.callbackUrl`（按身份源实例固定），不接受参数指定。
3. **扫码类**：`qr` 步骤必须带两位比对码；平台把二维码和发起端 cookie 绑定，防止二维码被转发。状态变化时连接器返回新的 `qr` 步骤（`status: scanned`），平台经 SSE 推给登录页。
4. **登录事务** 5 分钟过期、一次性使用；按 IP 和账号限速。过期后再调用返回 `iam.login_tx_expired`。
5. **错误**：连接器用 `fail` 步骤报错，`code` 用 `iam.` 前缀的通用码（例如 `iam.invalid_credentials`、`iam.external_denied`、`iam.connector_unavailable`），或连接器自己的 `<连接器类型>.<码>`。
6. **机密**：`configSchema` 里标 `"x-secret": true` 的字段加密保存、管理台不回显；传给连接器时已解密，连接器不得记录到日志。连接器的每次外部调用由平台记录耗时和结果（不记凭据）。
7. **兼容**：`spiVersion` 不兼容的连接器拒绝加载，并在管理台提示。

## 内置连接器类型

| 类型 | 说明 |
|---|---|
| `team-account` | 团队账号：邀请注册、口令 + TOTP、Passkey、手机扫码确认 |
| `emergency` | 应急账号：不能关、不能删，每次登录强制 TOTP 并通知所有超管 |
| `oidc` | 通用 OIDC / OAuth 2.0 |

企业定制连接器不在公开仓库里；它们和第三方连接器一样，通过 Java 插件或远程连接器接入，并要通过连接器测试套件（TCK）。
