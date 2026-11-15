# 首批资源准备（未上架、未签名）

在工位根执行 `node seed-resources/prepare.mjs <dsh-memwiki源码目录>`。需要锁定工作区依赖、Python。仅读源码的 lib、templates、skills、LICENSE、cordis.patch.yml 和包元数据，不访问用户记忆库、凭据或本机配置，不执行上游构建及生命周期脚本。

产物为两个 tgz、两份 `*.auriga-resource.json`、`checksums.json`、`build-source.json`。制品和现场证据留工位且被忽略。memwiki 将发行 JS 和生产依赖打成 Node ESM，去掉 lifecycle scripts 和外网安装依赖，保留 host/client/MCP CLI、通用模板和 skills；修正 bundle 后模板相对路径，并收集依赖许可证。示例 skill 仅根据用户提供的文字总结任务，无命令、外连和敏感内容。

现有校验器验证清单 schema、插件 validateArchive、skill packSkill/unpackSkill。签名声明不等于已签名。CLI 的 help 和空库初始化通过，也不能替代真实宿主安装及 MCP 完整读写验收。

## 主控发布步骤

1. 核实 build-source.json 的源码/锁文件摘要及许可证，在隔离测试库验收 memwiki 设置、MCP 读写。插件具备访问记忆库及凭据存储能力，按供应链 A 级做完整安全审核。
2. 管理台资源中心创建共享 L3 草稿。plugin 使用原插件接口；skill 使用通用 catalog，不将 plugin 上传到 catalog。
3. plugin：`POST /api/resource/v1/packages` 创建包，`POST /api/resource/v1/packages/{packageId}/versions` 上传整个 gzip。媒体类型、版本参数以 OpenAPI 为准。旧 Version.digest 是 tgz 原字节 SHA-256；外置通用清单仅作准备资料，当前不强行迁移 plugin 到 v2。
4. skill：`POST /api/resource/v1/catalog/packages` 创建包，`POST /api/resource/v1/catalog/packages/{packageId}/versions` 提交原始清单，按 `.../versions/{versionId}/artifacts/{artifactId}` 上传制品。清单及制品摘要分别核对 checksums.json；具体 body、If-Match、Idempotency-Key 和近期验证依 contracts/openapi/resource.yaml。
5. 分别 `POST .../versions/{versionId}/submissions` 送审。纯文本 skill 为 R1，至少一名非作者审核；memwiki 按插件既有审核流程和供应链 A 级独审处理。
6. 有权非作者发布：plugin 走 `/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/publications`，skill 走 `/api/resource/v1/admin/catalog/packages/{packageId}/versions/{versionId}/publications`。核对默认版本、启用与可见范围，从普通插件市场验证可见及安装。

## 需要主控提供

- 目标租户、作者、包/版本 ID、共享范围；创建、上传、送审、非作者审核与发布权限及近期验证会话。
- 平台现有可信签名服务：plugin v1 ResourceEnvelope 绑定归档摘要；skill v2 ResourceEnvelope 绑定原始清单和全部制品摘要。签发时确定 releaseSequence、issuedAt、notAfter、kid，并准备有效撤销清单及部署根公钥。
- 无需将私钥交给工人。没有读取私钥或伪造签名，主控走既有签名服务签发；安装端继续鉴权、验签、租约与撤销核验。
- 用户明确选择的记忆库目录，包不附带用户库、`.memwiki`、凭据文件或个人配置；清单权限声明不能代替宿主权限控制。

`real-ui.mjs` 是隔离 rc.3 页面验证脚本，资源 API 使用浏览器替身，不代表 NAS 或真实平台权限验收。按已有 public-host/e2e 锁文件将依赖分别安装到本目录 host/browser；浏览器使用系统 Chrome。临时宿主结束时清理。
