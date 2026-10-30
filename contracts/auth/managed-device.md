# 托管网络设备

内部接口见 `iam/v1/managed_device.proto` 与 `workspace/v1/managed_device.proto`。
IAM 只信任 workspace 提供的当前实例绑定，并且每次核验 org 的有效成员和联网权限。
一个实例一个有效版本，重启无重叠；已吊销的同版本不能重新签发。签发响应只供 workspace HPKE 下发，
IAM 保存散列和按 tenant/instance/version 作 AAD 的加密副本，以支持响应丢失后的幂等重试。

换取使用既有公开 `POST /oauth2/token`（form-urlencoded，无 Cookie、无 instance Bearer）：

- `grant_type=urn:auriga:params:oauth:grant-type:managed-device`
- `client_id=auriga-managed-network`
- `instance_id`、`owner_id`：规范 UUID，必须等于受信文件和当前绑定。
- `credential`：43 字符 base64url 随机机密。

租户由原可信 edge hop/部署入口选择，不能用表单声明。未知字段拒绝。
响应沿 OAuth token 结构：access_token、token_type=Bearer、scope=auriga.connect、expires_in（最大300秒）。
只授予 `auriga.connect.collab.use`，不含模型、管理员、普通用户或实例权限。
无效绑定/成员/凭据、已吊销或已到期返回 `common.forbidden`；依赖不可用失败关闭。
所有响应 no-store。每租户/实例限制换取频率，不在日志输出表单或令牌。

受信文件结构见 `plugin/managed-device.schema.json`；环境仅传 `AURIGA_DEVICE_CREDENTIAL_FILE` 路径。
托管设备复用 device + session 网络权威关联；其后端会话从不签发浏览器 Cookie、用户 JWT 或 OAuth refresh grant。
短期设备 JWT、票据、连接与来源在线核权均继续检查该关联和当前实例代次。
