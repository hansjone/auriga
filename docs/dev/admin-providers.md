# 身份源管理（IDPUI1）

`/providers` 对应管理台八组导航中的“安全与接入 → 身份源”，采用现有紧凑列表、连接器页签与右侧详情抽屉。依据管理台 HTML、需求复核 F11/F12，以及 Hub 整体功能继承基线的身份源管理部分。本批交付配置管理闭环，不表示其余 Hub 身份与恢复能力被删除或已经全部完成。

## 真实流程与服务语义

身份源/已登记连接器列表 → 身份源详情 → 新建、编辑、启用、停用 → 正式读回与状态反馈。端点为现有 `GET /api/iam/v1/admin/connectors`、`GET/POST /api/iam/v1/admin/providers`、`GET/PATCH/DELETE /api/iam/v1/admin/providers/{id}`。类型引用既有生成的 IAM DTO，不增加权限或错误码。

创建使用 JSON；修改使用 `application/merge-patch+json`。当前 ProviderManager 对 display 及每个顶层 config 成员分别替换，不递归合并子对象。本页只发送明确改变的顶层配置成员；修改安全可见的嵌套对象时，从原对象保留其余兄弟字段。显示信息修改保留其他语言、按钮文字及未知显示字段；准入策略未修改不发送。

DELETE 当前仅把 enabled 置 false，保留配置、账号与身份绑定，因此按钮明确标为“停用（保留配置）”，不承诺物理删除或现有会话撤销。重新启用使用 PATCH enabled=true。builtin 实例完全只读，应急身份源不能新增、编辑、停用或删除。连接器来源 builtin 不等于某个实例 builtin：普通 OIDC 实例仍可编辑。multipleInstances=false 且已有该类型实例时不再提供重复新建。

## 配置表单与机密

按实际 ConnectorDescriptor.configSchema 生成字符串、布尔值、有限安全数字/整数、字符串枚举、字符串数组及可安全修改的嵌套对象字段。OIDC 的 issuer/clientId/clientSecret/scopes/claimMapping 和团队账号的 rpId/origins/primaryStep 有对应控件，不以只读 JSON 文本代替配置管理。数组每行一个字符串；已有数组元素含换行时保持只读，避免重新分割造成无声修改。

未勾选字段不发送，也不自动写入 schema default。密钥控件只接受明确新输入，已有值仅按服务端实际 `x-secret-set` 标记显示“已设置”；不填入星号/掩码、不在确认内容或错误 detail 中回显、不进 URL/路由/存储/全局状态/日志。取消、写后读回、撤权、失败及卸载清除新输入。错误响应可能代表写已送达，页面提示先核对当前状态，不自动重放。

未知配置、复杂 schema、引用/组合、数组对象、非对象根配置及含隐藏子字段的嵌套对象，列明具体字段只读并保留。已有身份源仍可修改不依赖这些配置的展示信息或启停；新建所必需配置无法安全表示时才禁用创建，不停掉整页。当前没有通用密钥清空语义：空字符串是新值而非删除，按实际 schema/服务校验；nullable/复杂密钥需后续专用表单，不擅自发送 null 或假定 JSON Merge Patch 会删除它。

生成物将自由对象表示为 `Record<string, never>`。本页在单一配置适配边界使用原生成 DTO 的 config 类型断言，内部按未知 JSON 检查与生成；未复制协议 DTO 或修改 shared/契约。

## 权限、取消与恢复

读为 iam.providers.read；写还需 iam.providers.write。确认前复用 useOrgActions 重读 `/api/org/v1/me`；服务端仍为权限、内置限制和配置语义的最终判断者。本接口当前未标注近期 MFA 要求，界面不自行增加或绕过验证；若返回既有 iam.step_up_required，提供通过既有管理员登录验证后返回重试的提示。

本页所有 POST/PATCH/DELETE 贯通页面拥有的 AbortSignal，卸载/撤写权取消该 signal，保留既有共享 CSRF 登记机制，不新建 CSRF 管理器、不 resetSession。纯 IP 下共享 CSRF 登记可能仍完成；标准 fetch 对已取消业务 signal 拒绝发送，不能把假 fetch 被调用等同于服务器收到请求。已经送达或提交的写不能通过 abort 撤回。

401/403 或读权撤销清除列表、详情、表单及机密；晚到读取和卸载后响应不能恢复旧结果。写成功后列表/详情读回失败，仅显示 GET 重读，抽屉内可直接重试，不重放 POST/PATCH/DELETE。当前身份源接口没有 ETag，不宣称防止多管理员覆盖。

## 保留的后续能力

连接测试、目录搜索虽有契约描述，当前 IamAdminController 没有对应生产路由，故本页不发模拟请求或展示假成功。连接器注册/安装/卸载、复杂配置专用表单、完整准入策略编辑、账号恢复/解除绑定/迁移及会话撤销仍需各自真实流程。本页保留现有 admission，不覆盖自动开通规则、MFA、组映射或 externalSessionCheckSeconds；新建未发送 admission 时沿当前服务的 invite_only/new_device 默认。总部 Agent 与管理员个人身份源管理分开，不恢复旧平台任务流程。
