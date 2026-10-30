# AI 管理 API runtime

通过 `auriga-ai-gateway --admin-config /protected/admin.json` 显式启用；每进程一个 runtime。
未配置时不启动管理 API，不影响 network/team。管理 listener 只供 edge 转发；本任务不修改 edge 路由。
不配置 CORS，不接受浏览器 cookie；edge 负责管理源隔离、会话与 CSRF，转发 IAM 的访问令牌。

正式部署配置示例（路径由部署填写）：

```json
{
  "listen": "0.0.0.0:8081",
  "tlsCertFile": "/protected/ai-admin-server.pem",
  "tlsKeyFile": "/protected/ai-admin-server.key",
  "tlsClientCAFile": "/protected/edge-client-ca.pem",
  "tlsEdgeDNSNames": ["edge.example.com"],
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
可覆盖主密钥路径。org RPC 非回环必须 mTLS，字段沿 `team.RPCConfig`
（CertFile / KeyFile / CAFile / ServerName）；它与下述管理 HTTP listener 的材料分别配置。

管理 listener 的 TLS 四项必须一起提供：

- `tlsCertFile` / `tlsKeyFile`：服务端 PEM 证书链和匹配私钥；edge 必须校验该服务端的链、用途、期限和身份。
- `tlsClientCAFile`：专用于信任 edge 客户端证书签发者的 PEM CA 集合；仅接受带 CA 约束和签证书用途的证书，
  不使用操作系统根库，不接受空集合、非 CA 证书或夹杂损坏内容的集合。
- `tlsEdgeDNSNames`：非空的显式 edge 客户端 **DNS SAN** 名单，例如 `edge.example.com`。
  配置项使用小写 ASCII DNS 名，不带通配符、端口、协议、尾点或 IP；与客户端叶证书 DNS SAN 逐字精确匹配。
  证书须通过受信链、clientAuth EKU 和有效期校验，CA 相同但 SAN 不在名单的客户端仍会被拒绝。
  CN、URI SAN、IP SAN、请求 Host 及任何身份请求头都不能代替该 DNS SAN；节点入口使用的 URI 身份不自动获准。

任何非数值回环监听（包括 `localhost`、私网 IP、空主机和通配监听地址）都要求完整 mTLS。
仅在四项 TLS 配置均未设置时，允许数值回环（如 `127.0.0.1:8081`、`[::1]:8081`）使用原有明文开发模式；
默认仍为 `127.0.0.1:0`。回环一旦配置任意 TLS 项，也必须提供完整四项；不会降级到明文或单向 TLS。
材料不可读、非普通文件、链接、证书/私钥不匹配、CA 解析失败或名单非法均在连接数据库/监听端口前拒绝启动。
每个材料文件最大 1 MiB；由部署方保护文件权限，runtime 不修改 OS ACL，换证/更新名单后重启服务。

DNS SAN 是证书身份，管理服务不会解析名单中的 DNS；内部可连接数值 IP，由 edge 独立核验服务端证书身份。
前台域名 TLS、外置反代终止 TLS 或纯 IP HTTP 部署都不免除这条内部 mTLS 边界。
TLS 仅证明受信 edge 通道；原有管理 JWT 的签名、issuer、audience、admin ctx、权限和 stepup 校验仍独立执行。
正确 edge 证书不能替代用户 JWT，正确 JWT 也不能替代 edge 证书。
这里仅实现管理服务受信入口；edge 转发路由、会话/CSRF 与 UI 的完整接线由其他任务验证，不据此声称全链完成。

主密钥是部署生成的 **32 个原始随机字节**，不接受口令或文本编码。POSIX 文件仅服务账号可读，
不能是符号链接或硬链接；Windows 同样验证 ACL 仅授权服务身份、SYSTEM 和管理员，不能只依赖 chmod。
缺失/长度错误时启动失败。主密钥和加密数据库必须分别备份；本版本不提供主密钥轮换 API，
直接替换主密钥会导致既有密文不可用、24 小时幂等指纹不匹配。
JWT 使用 Team 同一 jwt/v5 库，校验 at+jwt / ES256 / issuer / auriga audience / 有效期 / admin ctx；
IAM 公共 JWKS 文件由部署可信同步并原子替换，每次请求重新读取，旧 key 保留至旧 token 全部到期。
文件不可用则失败关闭，不回退旧缓存。所有写操作要求近期二次验证。

`GET /api/ai/v1/admin/usage` 已接入同一管理 handler，仅接受显式 `ai.usage.admin` 权限；
仍校验管理用户 JWT、单一 `aud=auriga`、`ctx=admin` 和期限，沿上述受信管理入口策略。
此只读 GET 不要求 stepup，不修改账本、revision 或 outbox，也不返回 ETag。
仅支持 `limit`（默认 50，1–200）和 `cursor`（最长 512 字符）；未知、重复或非法参数返回
`common.validation_failed`。使用上一页的 `nextCursor` 获取后续页，按落账时间和 requestId 稳定升序；
游标带完整性校验并绑定当前租户和管理查询范围，不能使用本人用量游标或跨租户复用。
同租户同范围重读游标允许重复读取同页。结果包含当前租户全部主体的已落账 `Usage`，
空页 `items=[]`，没有下一页时省略 `nextCursor`；不包含 reserved/dispatching 记录。
unknown 的 input/output/cost 保持 null，保留原冻结值、价格版本和 UTC 周期，不按当前模型价格重算。

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
提供商/模型单项 GET 与列表项均返回强 `etag`，列表中的值可原样用作该资源的 If-Match。
凭据单项 GET 仅返回 id/state/createdAt/revision，并在 ETag 响应头提供当前版本；
模型 ACL 单项 GET 返回授权列表及授权集合专属的强 opaque ETag（`"grants-..."`），
绑定租户、模型 id 与版本，不能用模型 GET/列表或其他授权集合的 ETag 代替。
PUT 授权集合只接受该集合当前 GET 返回的 ETag；成功后使用返回的新 ETag。
底层仍共用模型行 revision，因此模型 PATCH 也会使先前取得的授权集合 ETag 失效，需重新 GET。
常规轮换激活 staged 凭据，原 active 转 retiring；读取旧凭据的单项 GET 获取新 ETag 后即可撤销。
显式 revoke 将 active/retiring 紧急转 revoked。
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

`go test -count=1 -run 'TestAdmin(TLS|Plain|Runtime)' .` 不依赖 PostgreSQL：生成独立临时 CA，
通过生产实际使用的 server 构造和随机回环 TLS listener 验证 TLS 1.2/1.3 的允许/拒绝握手、禁止明文降级，
并在真实管理 handler/JWT 验签路径验证无 JWT、伪造 JWT、错误 audience/ctx、缺权限/stepup 被拒绝。
这些测试不依赖真实 DNS、hosts、外部服务或 DSH，不证明数据库 CRUD 已通过。

使用独立测试数据库，将管理员 PostgreSQL URL 放入 `AURIGA_AI_TEST_PG_DSN` 后运行 `go test -count=1 ./...`。
fixture 创建随机低权限运行角色，验证 RLS、事务回滚、迁移幂等及 API；结束清理测试租户/角色。
fixture 的角色创建/授权和撤权/删除分别在同一事务内取得数据库级咨询锁（718245010），
避免跨测试包进程同时改写同一 ai schema/table ACL 目录行；cleanup 失败使测试失败。
此锁只覆盖 fixture 目录写入，运行角色、业务事务与生产迁移锁（718245009）保持原约束。
`go test -count=5 ./...` 会保留默认跨包并行；TestPostgresConcurrentFixtures 另启三个独立测试进程，
各进程内三个并行 fixture 同时初始化/使用/回收，并持续核验存活 fixture 的读写与 RLS。
无变量时具名跳过 PG 集成测试；`CI=true` 缺变量直接失败。CI Linux 提供 postgres:17 service，
Windows 使用 runner 自带 PGBIN，在随机端口启动临时库，结束时停止。不得指向真实部署。
