# releaseproof

读取可信安装根下 `<manifest 的 64 位小写 hex>/manifest.json` 与 `config.json` 的原始公开字节。
两文件分别最多 4 MiB；根与子目录不得是链接，文件必须是普通非空文件；目录句柄和文件身份检查防止读取期间替换路径。
不接受远端规格自选路径，不读取 manifest descriptor 的外部 URL/data，不缓存“已验”结果。

目录只负责传输，不能建立信任。每次启用、启动和恢复都重新读取，由 RT2V 的
`Verify`/`VerifyOCI` 核签名、原始摘要、单平台格式、目标和当前策略。
公开布局与安装说明见 `docs/dev/agent.md`。
