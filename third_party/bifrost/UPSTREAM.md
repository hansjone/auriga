# Bifrost 上游与本地维护

来源：https://github.com/maximhq/bifrost ，Apache-2.0。固定 release `transports/v2.4.0`，tag 解引用提交 `e869c3f9adc96ea53c38694a21bb04a8e1085487`。
原始发布归档 SHA256：`86efbf7f703aeb8ed115eadc440c6af812f9bf83c1420cc6f598e2b4d1cb63a5`。
保留上游 LICENSE、THIRD_PARTY_NOTICES.md。SOURCE.sha256 每行记录本地文件 SHA256、相对路径及对应上游文件 SHA256，便于复核裁剪和升级。

保留范围：core 的 chat completion、chat stream、list models、worker、key selector、fallback、请求取消及其可达依赖；providers 仅 openai（兼容接口）、minimax、deepseek、anthropic、gemini、utils；schemas、network、internal 等保留这些入口需要的定义。Responses 的内部转换用于 Anthropic/Gemini 适配及可选的 OpenAI 协议偏好，不公开 Responses HTTP 入口。

删除 transports 全部（含 UI/HTTP 服务）、其余 provider、core/mcp、agent、toolsearch 与 realtime 执行代码、上游 examples/e2e/测试大资源。没有编译或发布上游前端。
删除所有 plugins/framework：governance 经 configstore 引入 vectorstore/objectstore，logging 又关联 MCP/jobaccounting 等；无法满足最小引入目标。因此按任务备选方案复原 Auriga PR #265 删除前的 budget/store（`6938b4d^`），复用预算、预占、结算和用量记录，没有另造计费引擎。

本地修改：
- ProviderConfig新增默认false的PreferResponses，仅OpenAI且请求可表达时在两Chat dispatch局部选择既有Responses转换/流hook；不写跨fallback的协议偏好context。Chat独有参数及ToResponsesRequest未映射的显式类型扩展保Chat，默认配置与既有reasoning.mode行为不改。启用该选项后，保Chat时仅序列化副本使用OpenAI基类，防止custom provider alias误入通用过滤而丢prediction/web_search_options；原请求/worker/key/fallback身份保留。Auriga按原生openai启用，compatible关闭；protocol_test.go为本地新增纯测试，无对应上游文件（清单标upstream=none）。
- Chat→Responses历史同时保留assistant正文和全部函数调用；同次Responses普通输出的多文本、reasoning与多工具合并为一个Chat choice，不将输出项伪作备选答案。流式转换保留原output_index供每次stream attempt独有的映射压紧工具索引，不假设必有一个reasoning前缀；并行参数增量、任意前缀与工具结果第二轮有纯fake覆盖。
- Chat Completions 不再把调用方小于16的最大输出值上调到16；缺省值仍留给上游，参数名按协议适配。真实Run回归覆盖缺省、1及65536，Responses路径未改。
- 所有保留 Go 源文件与 go.mod 都有显著的 Auriga 修改头声明；go.sum 的修改声明在相邻 go.sum.NOTICE。哈希清单在修改后重新生成。
- MiniMax/DeepSeek 原生适配补上 custom provider alias，保证供应商 UUID 与选 Key 的回调一致；默认禁止回环目的地址，仅显式私网测试配置允许。
- core Provider 接口仅保留配置和 chat/list 所需方法；provider factory 仅保留上述五类。
- 删除 MCP 初始化、工具注入/执行、A2A/relay hooks 和不用的公开请求入口。
- 对不可达顶层声明/类型字段作静态裁剪；保留所有 init，避免破坏网络安全网段初始化。
- schemas 中少量 MCP 错误/联合协议数据类型仍由通用 schema 使用，因此 mcp-go 模块仍是实际编译依赖；没有 MCP server/client runtime。后续升级不能把这些残留数据类型误称为已启用 MCP。
- go.mod/go.sum 经 GOWORK=off go mod tidy 清理，Go 工具链统一为 Auriga 的 1.27.3；版本沿用该 release 的必需依赖，不为裁剪引入框架替代包。

体积口径（原始源码不含仓库外围文档；裁剪统计包含许可证与修改声明，不含本文件及 SOURCE.sha256）：原 core/framework/plugins/transports 为 2256 文件、51,665,393 字节；裁剪源码和许可证共 200 文件、3,102,786 字节（约原来的 6.01%）。
依赖口径：原 transports `go list -deps ./bifrost-http` 1687 包（非标准库1431），裁剪 core `GOWORK=off go list -deps ./...` 364 包（非标准库143）；`go list -m all` 模块图 662 → 98 项。模块图包含依赖模块自身的声明，不能当成实际链接的模块数。

升级流程：取相同 tag 归档核 SHA256，按 SOURCE.sha256 找到原文件，再做三方比对；重新测普通/流式、fallback、取消、SSRF、身份和真实 PG 预算/用量。不得直接覆盖这份裁剪树或仅更新 go.mod。
