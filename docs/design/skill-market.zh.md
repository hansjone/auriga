# Skill 类型专项设计

用户于2026-11-12确认D1至D11全部按推荐执行。公共资源模型、统一清单、存储、签名、接口迁移、P1派工清单与已决定要求由主文档维护。D8已选定Desktop自带Node、python-build-standalone及Eclipse Temurin 21 LTS，后两者由平台审核签名后按需下载；Skill清单的runtimes为空。[统一资源包规范](resource-package.zh.md)

## 原生格式与宿主能力

安装输入clients/installer/upstream.json固定DSH 0.2.1-rc.3。公开filesystem provider扫描根下一层的 `<name>/SKILL.md` 或 `<name>.md`。frontmatter必填name/description，可选whenToUse、metadata、disable-model-invocation、user-invocable。Auriga共享采用目录包，name为1至64字符小写kebab-case，名称与description和统一清单精确相等，开关只收YAML布尔值。[公开格式与配置](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/skill/skill-filesystem/README.md)

第1轮已核对同版本公开npm包README，integrity为 `sha512-IMGONDTeLvQanAAfI/2cLm1/Ppl/msriFxjOON/TOJAEnulou4inRAJ1DaJhLwDbhuX7zMUbapLQE7kwHN+k8g==`。实现以公开能力探测与真实宿主验收为准。[公开npm版本](https://www.npmjs.com/package/@deepseek-ai/dsh-skill-filesystem/v/0.2.1-rc.3)

上游顺序包含项目.dsh/skills、项目.agents/skills、customSkillDirs、用户DSH根和用户agents根，作用域也影响同名选择。公开catalog用于观察当前cwd生效来源。旧oclaw H3的目录、版本、送审和驳回交互保留为页面参考。[技能子系统契约](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/skills.md)

## 制品约束

统一清单kind=skill，skill段含entry=SKILL.md、contentProfile=text-only、dshVersionRange。一个payload，os/arch为any，runtimes为空。制品为严格ustar+gzip，根含SKILL.md，可附references/与assets/文本。外置auriga-resource.json替代第1轮manifest.json，版本摘要与制品摘要依主文档分别计算。

上限为压缩5 MiB、解压20 MiB、512条目含目录、单文件2 MiB、SKILL.md 256 KiB、路径240 UTF-8字节、深度8、压缩比100。whenToUse最多2048字符，metadata的JSON等价表达最多8 KiB。拒绝重复YAML键、标签、别名与过深结构。

允许UTF-8 .md/.txt/.json/.yaml/.yml，assets首期也限文本。依D1禁止scripts、可执行附件、嵌套压缩包、链接、设备和执行位。正文命令示例需审核工具调用、外连和敏感文件访问。纯资料为R1，至少一名非作者完整审核；命令型技能为R2，至少两名非作者审核且含安全审核。路径安全沿公共规则。

## 安装位置与同名处理

私有技能在 `<DSH_HOME>/skills/<name>/SKILL.md`。Desktop默认 `%LOCALAPPDATA%\AurigaDesktop\home\skills`，普通DSH跟随实际DSH_HOME，默认home时为~/.dsh/skills。云端写本人实例DSH_HOME对应的持久卷。

共享缓存和ledger在 `<DSH_HOME>/auriga/resources/<tenant>/<owner>/<client或instance>/skills/`。活跃根为 `<DSH_HOME>/auriga/skills-active/<tenant>/<owner>/<client或instance>/`，下一层为 `<name>/SKILL.md`，用公开customSkillDirs挂接。切换身份关闭旧视图，缓存版本留在扫描根外。宿主catalog确认名称和来源后回报应用，需要重启时显示待重启。

安装前检查同frontmatter.name的私有与已知根占用，有冲突先提示。项目来源可覆盖共享技能，UI显示实际来源和被覆盖状态。改名须重新打包送审，安装器遵循已签名称。依D3首期共享为L3，用户可关闭/卸载，接受项目覆盖边界。

更新原子切换验证后的版本，Windows占用时保留旧状态并提示待重启。关闭移出活跃视图，卸载只清理ledger标记且摘要匹配的文件，用户手改保留冲突副本。事务恢复与高水位沿主文档。

## 私有创建与共享

我的资源提供新建私有技能，预览、本地校验后写原生目录。手工技能按公开根列举和打开，用DSH原生文件能力编辑。接管手工目录前展示影响。私有内容仅在当前DSH，云端进入本人实例备份，默认不跨实例同步。

提交共享前展示SKILL.md、全部附件、工具/域名声明，用户确认后冻结为通用L3草稿，走总部审核发布和分发。私有副本继续独立编辑，管理员默认看到用户提交的共享字节，沿F01和已确认范围2、3。[需求依据](auriga-admin/requirements-review.md)

发现后调用受模型/用户调用开关与工具授权约束。更新/关闭提示新会话生效，已经载入的正文保留在历史，磁盘变化影响后续加载。[工具消费契约](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/skill/tool-skill/README.md)

## 到期、撤销和安全

共享租约取notAfter与撤销expiresAt较早值，各最多24小时。到期移出受管视图并停止受影响DSH，联网重新授权和验签后恢复。私有原生技能依DSH规则离线使用。

依D4，恶意撤销时云端由workspace/agent停止受影响DSH，本地由可信启动器/guardian停止对应进程，重验并禁用被撤销内容后恢复新会话，保留原历史。提示该动作可能中断DSH中的其他任务。依D5，共享Skill首期仅在具备可信guardian的云端和Auriga Desktop开放，外部DSH提供私有技能。guardian独立于可卸载的Auriga插件，在启动前和运行期间核验租约与撤销，DSH重启不能绕过。

description与正文可能注入指令、诱导终端、读取凭据和外传。审核检查实际进入模型的内容，工具权限由宿主与远程网关控制。文本技能会持续影响模型并残留历史，撤销上下文处置需单独验证。

## 页面与专项验收

管理中心skill面板展示纯文本正文、资料、差异和工具/域名声明。成员市场按kind筛选，详情显示适配与当前位置。我的资源支持私有创建/编辑/提交共享、共享更新/关闭/卸载，显示同名来源、待重启、新会话生效、撤销和过期。

P1专项测目录层级、非法frontmatter、篡改、同名项目覆盖、身份切换、更新占用、撤销已载入会话和离线超期。对应主文档P1-C成员/安装、P1-D云端规格/事件、P1-E Desktop guardian工作线。D的规格回报先于云端联调；共享功能开放前，云端须通过D的可信停用验收，Desktop须通过E的可信停用验收。原生发现和调用由正常DSH页面验收，部署交主控串行执行。
