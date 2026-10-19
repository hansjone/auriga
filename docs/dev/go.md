# Go 开发

使用 Go 1.27，命令从仓库根进入 `infra/` 后执行。
`go.work` 包含 `gen`、`edge`、`ai-gateway`、`egress`、`agent`、`ctl`，
模块前缀为 `github.com/hansjone/auriga/infra/`。服务骨架仅支持 `--version`
和标准库 slog JSON 日志，agent 的 internal 子目录仅预留位置。

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

```powershell
Set-Location infra
go run ./ctl dev
# 可选：
go run ./ctl dev --pg-port 15434 --nats-port 14223 --data-dir ../.dev
```

前台启动 PostgreSQL 17（`127.0.0.1:15433`）和内嵌 NATS JetStream
（`127.0.0.1:14222`），按 Ctrl+C 一起停。仅适合本机开发，NATS 无认证。
首次下载 PostgreSQL 二进制需要联网。数据库名 `auriga`、开发超级用户 `auriga_dev`，
使用随机 256 位口令，保存在仓库根 `.dev/credentials.env`（`PGPASSWORD=...`）。
启动完成后打印 PostgreSQL / NATS 连接串，终端内容含开发口令。
重启复用口令和数据，不执行任何业务迁移。

从当前目录向上找 `contracts/buf.yaml` 确定仓库根。
`--data-dir` / `AURIGA_DEV_DIR` 可覆盖数据根，此时编译后的 ctl 可在仓库外运行。

- `.dev/pg`：PostgreSQL 数据；`.dev/nats`：JetStream 数据。
- `.dev/cache`：下载缓存，用 `--cache-dir` / `AURIGA_PG_CACHE_DIR` 覆盖。
- `.dev/runtime/pg`：可重建的解压运行目录，用 `--runtime-dir` / `AURIGA_PG_RUNTIME_DIR` 覆盖。
  embedded-postgres 每次会删除重建此目录，勿指定含需保留文件的目录；代码拒绝与缓存/数据重叠。
- 参数优先于环境变量，两个端口必须不同且在 1–65535 内。
- `dev.lock` 阻止同目录同时起两套服务。强制终止后，先确认 PostgreSQL 已停止，再清理遗留锁。
  正常 Ctrl+C 会移除锁。已有数据库缺失/损坏口令文件时拒绝覆盖。

集成测试用随机 loopback 端口和临时目录，验证 PostgreSQL 主版本/超级用户、真实 SQL、
JetStream 持久化、重启、目录锁及 NATS 启动失败的清理；结束时检查端口释放。
Windows 受限沙箱不能访问用户临时目录时，可在命令进程内把 TMP 指向工作区临时目录。
不要求 Docker，不启动常驻进程。
