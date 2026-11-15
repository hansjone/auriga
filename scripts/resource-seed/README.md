# 首批资源准备（未上架、未签名）

执行 `node scripts/resource-seed/prepare.mjs <dsh-memwiki源码目录> [输出目录]`。默认产物位于工位 `seed-resources/`，入口从任意当前目录均可运行。需要 Node 22、npm 和锁定工作区依赖。

每次在输出目录下创建独立 `build-*`，复制通用发行代码、包元数据及固定清单中的三个模板/技能文件，以 `npm ci --omit=dev --ignore-scripts --no-audit --no-fund` 安装锁定生产依赖。源码提交号、工作区改动和实际输入字节摘要一并记录。归档从显式文件映射构造，只收录四个 JS 入口、package.json、Cordis patch、许可证及固定资料文件。旧输出与隐藏附件不会进入映射。脚本只读源码，不访问用户记忆库、凭据或个人配置。

产物为两个 tgz、两份 `*.auriga-resource.json`、`checksums.json`、`build-source.json`。制品和现场证据留工位且被忽略。memwiki 将发行 JS 和生产依赖打成 Node ESM，去掉 lifecycle scripts 和外网安装依赖，保留 host/client/MCP CLI、通用模板和 skills；修正 bundle 后模板相对路径，并收集依赖许可证。示例 skill 仅根据用户提供的文字总结任务，无命令、外连和敏感内容。

现有校验器验证清单 schema、插件 validateArchive、skill packSkill/unpackSkill。构建时执行 bundle 后的 CLI 空库初始化，核对模板路径。`build-source.json` 记录源码提交号、锁文件摘要、输入文件摘要、esbuild 版本、所有实际打入包中的依赖名和版本，以及包文件清单。

可运行 `RESOURCE_SEED_SOURCE=<源码目录> node --test scripts/resource-seed/prepare.test.mjs` 验证两次干净安装的归档一致，旧输出中的额外文件被排除。在 PowerShell 中先设置 `$env:RESOURCE_SEED_SOURCE` 再运行该 Node 命令。

## 插件能力与审核材料

当前 dsh-memwiki 0.4.0 提供 18 个 MCP 工具，包括记忆库查询、增删改和 `memwiki_secret_list/get/set/delete`。工具名从源码提取后写入 `permissions.tools`，`dataAccess` 声明 workspace_read、workspace_write 和 user_selected_files。插件会执行 git，包括向用户所配置远端推送。主机由用户远端配置决定，`outboundHosts` 以 `*` 保守声明可访问任意主机，实际远端需在发布审核时核对。

Windows 凭据使用 DPAPI，非 Windows 实现采用明文凭据存储。该包的 os=any 包含这一差异，发布审核与使用说明必须披露。能力说明同步写入清单 description 和构建来源文件。清单字段沿用现有契约，权限授权仍由宿主和平台执行。

## 主控发布步骤

1. 核实 build-source.json 的源码提交、锁文件/输入摘要、依赖版本及许可证，在隔离测试库验收 memwiki 设置、MCP 读写，核对凭据存储差异和 Git 远端。按供应链 A 级做完整安全审核。
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

真实 rc.3 页面的一次性验证脚本、截图及 DOM 尺寸证据保存在工位 `seed-resources/`，由工位报告记录命令和结果。
