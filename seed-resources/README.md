# 统一目录插件种子

正式插件种子通过统一目录发布。先按[种子资源准备说明](../scripts/resource-seed/README.md)构建，取同一次构建生成的 npm 格式 tgz、原始清单和 `checksums.json`。确认 `build-source.json` 记录的构建来源及能力说明已审核。本目录的归档和运行回执默认忽略。

在仓库根目录运行准备和离线预览命令。以下用 dsh-memwiki 0.4.0 举例，tgz 文件名以准备输出的 `checksums.json` 为准。

```powershell
node scripts/resource-seed/prepare.mjs 'D:\path\to\dsh-memwiki'
node seed-resources/publish-unified-plugin.mjs ./seed-resources/dsh-memwiki-0.4.0.tgz ./seed-resources/dsh-memwiki.auriga-resource.json
```

预览使用与发布相同的本地校验，核对原始清单 schema、权限域名与版本范围、tgz 大小与 SHA-256、归档格式及 npm 元数据。包内 `engines.dsh` 如果存在，须与清单的 DSH 范围逐字一致。成功时直接输出原始清单字节，预览无需网络、平台地址或令牌。

## IP 模式使用浏览器管理台

1. 用作者账号登录 IP 模式的管理入口，打开 `/resources/plugins`，选择本次准备生成的 .tgz。准备脚本把资源名、能力和运行时声明放在包内的 `package/auriga.manifest.json`，管理台读取这些字段并按实际文件计算制品摘要。早期产物需重新运行准备命令。
2. 对照同批清单核对介绍、权限和 DSH 兼容范围，确认共享范围，选择「上传并送审」。管理台处理浏览器会话、CSRF 登记及身份再验证。
3. 两名不同的非作者分别登录同一管理入口，在 `/resources/plugins/review` 核对版本、原始清单和制品摘要，分别记录普通与安全审核。安全审核者须具备当前资格。
4. 有发布权限的非作者从 `/resources/plugins/published` 打开审核通过的版本，完成身份验证及发布确认。用同批 `checksums.json` 对照管理台详情的清单和制品摘要，从正常市场入口验证安装。

## 域名模式使用发布 CLI

只有已配置域名模式并开放统一目录的 HTTPS 管理入口支持 `--publish`。`AURIGA_SEED_ORIGIN` 填管理台的 HTTPS 源。写入前，CLI 只读确认域名模式、管理路由与发布者权限。门户和 IP 模式管理入口会在创建包前被拒绝。

准备具备相应权限和近期身份验证的 IAM 管理员用户 access token，通过已注册的 OAuth 客户端，在域名管理入口按授权码流程取得。令牌是发给平台 API 的短期 Bearer，属于 `admin` 上下文的用户身份。浏览器会话 Cookie、门户或设备令牌、服务账号凭据都无法履行此流程。仅从环境提供 `AURIGA_SEED_AUTHOR_TOKEN`、`AURIGA_SEED_GENERAL_TOKEN`、`AURIGA_SEED_SECURITY_TOKEN` 和 `AURIGA_SEED_PUBLISHER_TOKEN`。

确认两名不同的非作者已分别审核并批准这组字节，安全审核者具备当前资格，有权发布者为非作者。主控在授权环境中给同一条预览命令追加 `--publish`。

脚本通过统一目录创建、multipart 上传原始清单及 tgz、送审并以对应角色记录两个审核决定，最后由平台签发 v2 并发布。执行完成后回执包含 `packageId`、`versionId`、`manifestDigest` 和 `artifactDigest`，与准备产物摘要核对后从正常市场入口验证安装。该流程用于目标目录初次上架。服务端仍会在每步核对当前权限、版本和资源状态。遇到已有同名资源或写入后出现权限、网络等问题时，先到管理台核对服务端进度，再接续处理已有草稿或审核记录。
