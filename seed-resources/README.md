# 统一目录插件种子

正式插件种子通过统一目录发布。先按[种子资源准备说明](../scripts/resource-seed/README.md)构建，取同一次构建生成的 npm 格式 tgz、原始清单和 `checksums.json`。确认 `build-source.json` 记录的构建来源及能力说明已审核。本目录的归档和运行回执默认忽略。

在仓库根目录运行准备和离线预览命令。以下用 dsh-memwiki 0.4.0 举例，tgz 文件名以准备输出的 `checksums.json` 为准。

```powershell
node scripts/resource-seed/prepare.mjs 'D:\path\to\dsh-memwiki'
node seed-resources/publish-unified-plugin.mjs ./seed-resources/dsh-memwiki-0.4.0.tgz ./seed-resources/dsh-memwiki.auriga-resource.json
```

预览校验原始清单 schema、tgz 的大小与 SHA-256，以及归档内 `package/package.json` 的 npm 名称和版本，直接输出原始清单字节。目录资源名、能力、DSH 兼容范围和版本均来自清单。预览无需网络、平台地址或令牌。

实际发布由主控在授权环境中给同一条预览命令追加 `--publish`。从环境提供 `AURIGA_SEED_ORIGIN`、`AURIGA_SEED_AUTHOR_TOKEN`、`AURIGA_SEED_GENERAL_TOKEN`、`AURIGA_SEED_SECURITY_TOKEN` 和 `AURIGA_SEED_PUBLISHER_TOKEN`。确认两名不同的非作者已分别审核并批准这组字节，安全审核者具备当前资格，有权发布者为非作者。各角色满足接口的权限及近期验证要求，令牌只从环境读取。

脚本通过统一目录创建、multipart 上传原始清单及 tgz、送审并以对应角色记录两个审核决定，最后由平台签发 v2 并发布。执行完成后回执包含 `packageId`、`versionId`、`manifestDigest` 和 `artifactDigest`，与准备产物摘要核对后从正常市场入口验证安装。该流程用于目标目录初次上架。已有同名资源时请求会失败，按管理台当前状态处理。部分失败可能留下草稿，先核对服务端进度再续办。
