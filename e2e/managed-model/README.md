# 托管模型链路验收

设计与部署前提见 [托管模型凭据](../../docs/design/managed-model-credential.zh.md)。
使用共享栈时遵守工作区的锁和真实 DSH 唯一执行槽规则；本目录不自动启动或重建栈。

1. 经公开部署流程更新 platform、ai-relay 和 agent，确认 workspace V7 迁移成功。
2. 在 LiteLLM 配好受限团队服务 Key、两个模型及测试成员 customer，确认成员有模型调用权限。
3. 确认可信安装材料的 `modelBaseUrl`（缺省为 `platformOrigin + /ai/v1`）同时被 workspace 与实例正常解析、访问和验证 TLS；不使用伪造 Host 或跳过证书检查。
4. 从成员门户正常启动/重启本人实例，在原生模型选择器选 `Auriga` 提供方的池模型发起对话。
5. 按真实请求 id 查询 LiteLLM spend logs，确认 `end_user = tenantId:accountId`、非零 token 用量及对应服务 Key，与桌面成员的维度一致。
6. 停止/重启实例、移出成员或吊销其托管设备后复验旧 Key 被拒；不得用管理员/外部 Key 代替托管模型 Key。

已有第一阶段验收脚本的第⑥项及 `modelNative` 配置可以继续用于云端、桌面及成员用量核对；它们必须指向真实原生会话，不以本任务的组件测试代替。
本工位本地检查与首失日志保存在未入库的 `.evidence/`，具体结果及未完成的真实验收以工位 `.agent-report.md` 为准。
