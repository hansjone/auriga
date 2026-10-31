# 共用 collab 安装输入

`Read` 读取显式批准的安装 JSON，校验租户、realm、版本、有效期、独立 Ed25519 SPKI
指纹、三个用途的 PKCS#8 私钥对应关系以及 Linux 属主/链接/权限。`CheckState` 比对批准
keyset 与安装状态；`Clear` 清除输入材料的内存快照。公开 JSON 字段见 NAS README。

compose 的 HTTPS 门禁仍在其配方入口，NAS 的 HTTP 基线和 HTTPS 呈现来源由 NAS
配方验证。地址、数据库角色、mTLS、收据和运行副本各由自己的配方负责。
此包不签名、不生成网络根、不请求外网、不宣称 READY。最终根签/schema、发布与数据库
防回滚仍由生产 Java `RootKeyPublication`、`SigningKeyLoader` 和 `NetworkKeyOperations` 执行。

## 一次性测试根

只有独立测试环境得到明确授权时，才使用旁边的离线开发工具：

```sh
node deploy/bootstrap/collab/generate-test-approval.mjs --test-only /private/request.json /private/new-approval
node --test deploy/bootstrap/collab/generate-test-approval.test.mjs
```

`request.json` 仅含 `tenantId`、`realm`、`approval` 三个字符串；tenant/realm 必须来自
实际 IAM native 设备登记或只读权威记录，`approval` 写明批准人、时间、一次性测试用途及
“非正式发行根”。先限制父目录权限：Linux 仅当前用户可访问，Windows 仅当前用户 ACL。
输出目录必须不存在，工具拒绝覆盖及祖先符号链接，文件以 0600、目录以 0700 创建。
Windows 继承受限父目录的 ACL，不把 POSIX mode 当 Windows ACL 验证。

工具生成独立 Ed25519 根和三个用途钥、最长 24 小时的完整根签发布/公告和一致的公钥状态；
stdout 仅输出根指纹、kid、截止时间。安装的严格 `input.json` 字段保持不变，审批原文在
相邻 `approval.json`，以 `inputSha256` 绑定准确输入字节。这是人工审计记录，不是额外的
密码学审批权威；正式部署仍须独立指纹确认及正式离线根运维。

`offline-root-private.der` 只留离线开发机，绝不上传 NAS。安装时只交付 `input.json`、
`approval.json`、`root.der`、`signed.json`、`announcement.json`、`state.json` 和
`business.der`/`protocol.der`/`management.der`；后三者是服务必需的在线私钥。
测试工具不登录、不注册目标设备、不修改安装根/收据、不续期轮换、不宣称 READY。
