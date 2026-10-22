# Go 开发

构建和测试固定使用 Go 1.27.3，命令从仓库根进入 `infra/` 后执行。
该版本包含 [Go 官方发布记录](https://go.dev/doc/devel/release#go1.27.3)所列的
`crypto/x509` 安全修复，并包含 1.27.2 的安全修复。CI 与容器构建同步固定补丁版本；
`go.mod` / `go.work` 中的 `go 1.27.0` 仍表示最低语言版本，不能代替实际工具链要求。
已有 Go 安装可在当前进程设置 `GOTOOLCHAIN=go1.27.3`，由 Go 下载并校验官方工具链，
再以 `go version` 确认实际版本。此工具链补丁不代表所有第三方依赖均无漏洞。
`go.work` 包含 `gen`、`edge`、`ai-gateway`、`egress`、`agent`、`ctl`，
模块前缀为 `github.com/hansjone/auriga/infra/`。

`edge` 已实现接入网关骨架（Host 路由、会话→Bearer、CSRF、限流、静态、
TLS 模式、身份头签发、`/open` 票据）；配置与运维说明见 [edge.md](edge.md)。
其余 Go 服务骨架仍以 `--version` + slog JSON 为主；agent 的 internal 子目录仅预留位置。

## 生成契约

`go run generate.go` 从只读的 `../contracts/proto` 生成 `gen/auriga/**`。
生成物及 go.mod/go.sum 入库。无需全局 buf、protoc 或 Go 插件：脚本通过
`go run github.com/bufbuild/buf/cmd/buf@v1.59.0` 调用 buf，
`buf.gen.yaml` 使用本地命令数组运行 protoc-gen-go v1.36.10、protoc-gen-go-grpc v1.5.1，
均为 `paths=source_relative`。删除契约后，旧的对应生成文件也会清除。

Go workspace 根目录不是模块，不能直接用 `go test ./...` 覆盖全部模块。PowerShell：

```powershell
Set-Location infra
foreach ($module in 'gen','edge','ai-gateway','egress','agent','ctl') {
    Push-Location $module
    try {
        go vet ./...
        go test -count=1 -timeout=10m ./...
        go build ./...
    } finally { Pop-Location }
}
gofmt -l .
```

CI 在 Ubuntu / Windows 运行生成同步检查、gofmt、vet、test、build，
以官方 golangci-lint action 在每个模块运行 `infra/.golangci.yml`。

## 本地开发数据服务

完整命令、数据目录、角色清单与安全注意见 [ctl.md](ctl.md)。

```powershell
Set-Location infra
go run ./ctl dev
go run ./ctl start
go run ./ctl status
go run ./ctl stop
# 可选：
go run ./ctl dev --pg-port 15434 --nats-port 14223 --data-dir ../.dev
```

- `dev`：前台启动 PostgreSQL 17（`127.0.0.1:15433`）和内嵌 NATS JetStream（`127.0.0.1:14222`），Ctrl+C 停。
- `start` / `stop` / `status`：同一套组件的后台管理；`status` 退出码 `0`=运行中、`3`=已停止。
- 仅适合本机开发：NATS 无认证，只听 loopback。首次下载 PostgreSQL 二进制需要联网。
- 库名 `auriga`、开发超级用户 `auriga_dev`；各业务 schema/角色见 ctl.md。口令分别在
  `.dev/credentials.env` 与 `.dev/service-roles.json`（权限收紧）。`dev` 终端会打印带口令的 URL；
  `start`/`status` 不打印明文口令。不执行业务迁移（Flyway 由服务自己跑）。

`--data-dir` / `AURIGA_DEV_DIR` 可覆盖数据根。`dev.lock`（含持有者 pid）阻止同目录双开；
强制结束后先确认进程已停再清锁，或对该目录执行 `ctl stop`。
