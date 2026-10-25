# AI 管理 API runtime

通过 `auriga-ai-gateway --admin-config /protected/admin.json` 显式启用；每进程一个 runtime。
未配置时不启动管理 API，不影响 network/team。管理 listener 只供 edge 转发；本任务不修改 edge 路由。
不配置 CORS，不接受浏览器 cookie；edge 负责管理源隔离、会话与 CSRF，转发 IAM 的访问令牌。

配置示例（路径由部署填写）：

```json
{
  "listen": "127.0.0.1:8081",
  "issuer": "https://iam.example.com",
  "jwksFile": "/protected/iam-public-jwks.json",
  "masterKeyFile": "/protected/ai-master.key",
  "store": {"maxConnections": 8},
  "org": {
    "Target": "127.0.0.1:9091",
    "ServiceTokenFile": "/protected/ai-org-service.jwt"
  }
}
```

`AURIGA_AI_PG_DSN`、`AURIGA_AI_MIGRATION_PG_DSN` 分别提供应用和迁移连接串，也可填
`store.dsn` / `store.migrationDsn`（该配置文件同样必须保护）。`AURIGA_AI_MASTER_KEY_FILE`
可覆盖主密钥路径。非回环监听必须同时配置 `tlsCertFile` / `tlsKeyFile`；org RPC 非回环必须 mTLS，
字段沿 `team.RPCConfig`（CertFile / KeyFile / CAFile / ServerName）。

主密钥是部署生成的 **32 个原始随机字节**，不接受口令或文本编码。POSIX 文件仅服务账号可读，
不能是符号链接或硬链接；Windows 同样验证 ACL 仅授权服务身份、SYSTEM 和管理员，不能只依赖 chmod。
缺失/长度错误时启动失败。主密钥和加密数据库必须分别备份；本版本不提供主密钥轮换 API，
直接替换主密钥会导致既有密文不可用、24 小时幂等指纹不匹配。
JWT 使用 Team 同一 jwt/v5 库，校验 at+jwt / ES256 / issuer / auriga audience / 有效期 / admin ctx；
IAM 公共 JWKS 文件由部署可信同步并原子替换，每次请求重新读取，旧 key 保留至旧 token 全部到期。
文件不可用则失败关闭，不回退旧缓存。所有写操作要求近期二次验证。

迁移连接只用于启动迁移；运行连接必须为无 SUPERUSER/BYPASSRLS/CREATEROLE/CREATEDB 的独立 DML 角色，
不可拥有 schema/表或继承迁移角色。部署用迁移角色授予运行角色：

```sql
GRANT USAGE ON SCHEMA ai TO ai_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ai TO ai_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA ai GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ai_app;
```

首次部署须先以迁移角色创建 ai schema 并配置默认授权；或先执行迁移后授权再重启。
迁移有事务咨询锁、历史校验和；编号 0001–0009 属于管理底座，0010–0019 保留预算账本。
每个业务事务使用 SET LOCAL 租户与 SQL 显式租户条件，业务表 FORCE RLS。模型的公开 id 是契约定义的逻辑别名。

创建 POST 与 activate/revoke 的幂等作用域为租户、账号、方法、规范路径、key，有效 24 小时；
HMAC 指纹与资源写入、脱敏响应在同一事务保存。过期记录在相同 key 下次使用时覆盖；
无 key 每次独立执行。PATCH/PUT 不支持幂等 key，使用 If-Match 防覆盖。
常规轮换激活 staged 凭据，原 active 转 retiring；显式 revoke 将 active/retiring 紧急转 revoked。
staged 直接 revoke、再次激活/撤销等非法迁移返回 common.conflict。secret 没有读取接口。
后续转发必须固定 credentialId、检查 revoked 并取消该凭据在途流；本任务没有任何上游请求或转发流。

供应商登记会拒绝私网/本地字面地址及当前 DNS 中的非公网地址；不请求供应商 HTTP 端点。
后续转发仍必须重新校验 DNS、绑定实际连接 IP 并拒绝重定向，以防 DNS 重绑定。

## 授权契约缺口（需主控接线）

当前可真实写入成员 ACL：org 在线核当前授予者状态/有效权限、目标成员租户/状态与授予上限。
组/角色 ACL **返回 common.unavailable，不落库**：现有 org CheckGrant 对缺失或外租户目标也可能 allowed=true，
无法证明引用存在于当前租户。需要 org 提供权威的组/角色存在与归属校验契约后再启用；
不能用“空权限集合”推断归属，也不直接读 org schema。实现边界在报告中同样列出。

## 测试

使用独立测试数据库，将管理员 PostgreSQL URL 放入 `AURIGA_AI_TEST_PG_DSN` 后运行 `go test -count=1 ./...`。
fixture 创建随机低权限运行角色，验证 RLS、事务回滚、迁移幂等及 API；结束清理测试租户/角色。
无变量时具名跳过 PG 集成测试；`CI=true` 缺变量直接失败。CI Linux 提供 postgres:17 service，
Windows 使用 runner 自带 PGBIN，在随机端口启动临时库，结束时停止。不得指向真实部署。
