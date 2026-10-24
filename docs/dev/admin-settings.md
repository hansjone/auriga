# 管理台团队资料与工作空间策略

`/settings` 使用现有 shared ApiClient、内存 CSRF 与通用确认框。团队资料和 workspace 策略独立读取、编辑、提示错误及重试。

- 团队资料读取 `GET /api/org/v1/tenant`，不要求虚构的 tenant.read 权限；写入需要 `org.tenant.write`。仅以 `application/merge-patch+json` PATCH `/api/org/v1/admin/tenant` 的实际变化 displayName、locale，不发送未编辑字段。id、slug、hosts、createdAt 只读。现有后端将省略/null 作为保持旧值，空字符串原样写入；页面支持空字符串，不发送 null。此接口没有版本字段/ETag，不能保证多管理员不互相覆盖。
- 策略读取需要 `org.policies.read`，写入独立需要 `org.policies.write`，只访问 `/api/org/v1/admin/policies/workspace`。沿用 generated Tenant、Policy、WorkspacePolicy 与请求类型；不开放 login/egress。workspace GET 的 org.not_found 404 显示尚无策略且禁止创建，其他错误不会伪装为空策略。
- 六个字段均明确区分“本层设置”和省略；读取未给出的字段不填客户端默认值。generated 将带 default 的字段生成为必填，编辑器以 generated WorkspacePolicy 的 Partial 投影表示服务实际支持的稀疏值，仅在 API 请求边界断言 generated oneOf 请求，不手写另一套协议 DTO。idleMinutes 至少 5、maxConcurrentStarts 至少 1，均要求安全整数；布尔字段严格布尔值，版本为字符串。未知字段连同嵌套内容保留，并随整层 `{value}` PUT。已读回非法类型、非法已知数值或任意层级无法安全往返的数值（保守地包含非整数）会禁止保存，不截断/丢弃后写回。
- 保存原始 GET 强 ETag，且按当前服务可解析的非负 Java long 引号格式验证；缺失、弱标签或非法值均禁止保存。PUT 带原始 If-Match。确认前只重读 `/me` 复核权限，不以新 ETag 替换旧草稿的版本。412 保留草稿，阻止直接重试；需点击明确的“重新读取（丢弃本区草稿）”，检查新值并再次确认。
- 前端 If-Match 接线须与 POLICYSAFE1 的服务端原子版本更新配套发布；详见 [策略完整性](org-policy-integrity.md)。前端自身不提供数据库原子保障，也不承诺新策略已改变现有实例或资源安装功能已接通。团队资料 PATCH 无 ETag，不受策略版本保障。
- 最终确认展示字段原值、实际新值/省略和整团队生效范围。取消不写入；待确认/提交期间锁定操作并防重复。保存成功后正式 GET 读回；失败单独提示，按钮只重试读取，不重放 PATCH/PUT。
- 策略读权限撤销或策略 403 清掉其对象、草稿和 ETag，不屏蔽团队资料；401 清理两块。卸载及被清理代次的晚到响应均不能恢复数据或发起写入。服务端仍是最终权限判断方，客户端检查不替代服务端事务边界。

验证使用 admin Vitest 组件/API 边界测试、lint、vue-tsc 和 Vite build，不启动服务。组件测试不替代真实域名/IP 部署及后端并发验收。
