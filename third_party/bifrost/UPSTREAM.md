# Bifrost 上游与本地维护

来源：https://github.com/maximhq/bifrost ，Apache-2.0。固定 release `transports/v2.4.0`，tag 解引用提交 `e869c3f9adc96ea53c38694a21bb04a8e1085487`。
原始发布归档 SHA256：`86efbf7f703aeb8ed115eadc440c6af812f9bf83c1420cc6f598e2b4d1cb63a5`。
保留上游 LICENSE、THIRD_PARTY_NOTICES.md。SOURCE.sha256 每行记录本地文件 SHA256、相对路径及对应上游文件 SHA256，便于复核裁剪和升级。

保留范围：core 的 chat completion、chat stream、list models、worker、key selector、fallback、请求取消及其可达依赖；providers 仅 openai（兼容接口）、minimax、deepseek、anthropic、gemini、utils；schemas、network、internal 等保留这些入口需要的定义。Responses 的内部转换仍用于 Anthropic/Gemini 适配，不公开 Responses HTTP 入口。

删除 transports 全部（含 UI/HTTP 服务）、其余 provider、core/mcp、agent、toolsearch 与 realtime 执行代码、上游 examples/e2e/测试大资源。没有编译或发布上游前端。
删除所有 plugins/framework：governance 经 configstore 引入 vectorstore/objectstore，logging 又关联 MCP/jobaccounting 等；无法满足最小引入目标。因此按任务备选方案复原 Auriga PR #265 删除前的 budget/store（`6938b4d^`），复用预算、预占、结算和用量记录，没有另造计费引擎。

本地修改：
- MiniMax/DeepSeek 原生适配补上 custom provider alias，保证供应商 UUID 与选 Key 的回调一致；默认禁止回环目的地址，仅显式私网测试配置允许。
- core Provider 接口仅保留配置和 chat/list 所需方法；provider factory 仅保留上述五类。
- 删除 MCP 初始化、工具注入/执行、A2A/relay hooks 和不用的公开请求入口。
- 对不可达顶层声明/类型字段作静态裁剪；保留所有 init，避免破坏网络安全网段初始化。
- schemas 中少量 MCP 错误/联合协议数据类型仍由通用 schema 使用，因此 mcp-go 模块仍是实际编译依赖；没有 MCP server/client runtime。后续升级不能把这些残留数据类型误称为已启用 MCP。
- go.mod/go.sum 经 GOWORK=off go mod tidy 清理，Go 工具链统一为 Auriga 的 1.27.3；版本沿用该 release 的必需依赖，不为裁剪引入框架替代包。

体积口径（不含文档和许可证）：原 core/framework/plugins/transports 为 2256 文件、51,665,393 字节；裁剪源码和许可证共 198 文件、3,073,230 字节（约原来的 5.95%）。
依赖口径：原 transports `go list -deps ./bifrost-http` 1687 包（非标准库1431），裁剪 core `GOWORK=off go list -deps ./...` 364 包（非标准库143）；`go list -m all` 模块图 662 → 98 项。模块图包含依赖模块自身的声明，不能当成实际链接的模块数。

升级流程：取相同 tag 归档核 SHA256，按 SOURCE.sha256 找到原文件，再做三方比对；重新测普通/流式、fallback、取消、SSRF、身份和真实 PG 预算/用量。不得直接覆盖这份裁剪树或仅更新 go.mod。
