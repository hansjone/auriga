# 历史会话枚举组合回归

本 fixture 串接当前源码的 `installSharedHost`、`createSharedRuntime`、
`createSharedNodeTransport`、`sharednode.Relay`，以及合成的 bufconn gRPC W。
不需要 npm 安装，不启动 DSH、PG 或任何外部服务。

在仓库根目录执行（Go 缓存及临时目录使用本工位 `target`，最多 2 CPU、512 MiB）：

```powershell
New-Item -ItemType Directory -Force target/go-build,target/go-tmp | Out-Null
$env:GOTOOLCHAIN='go1.27.3'
$env:GOMAXPROCS='2'
$env:GOMEMLIMIT='512MiB'
$env:GOCACHE=Join-Path (Get-Location) 'target/go-build'
$env:GOTMPDIR=Join-Path (Get-Location) 'target/go-tmp'
Push-Location infra/agent
go build -p 1 -o ../../target/history-relay-fixture.exe ./internal/sharednode/testdata/history-relay
Pop-Location
node --max-old-space-size=512 infra/agent/internal/sharednode/testdata/lookup-host.mjs target/history-relay-fixture.exe
```

Linux 对应命令（仍在仓库根目录）：

```sh
mkdir -p target/go-build target/go-tmp
export GOTOOLCHAIN=go1.27.3 GOMAXPROCS=2 GOMEMLIMIT=512MiB
export GOCACHE="$PWD/target/go-build" GOTMPDIR="$PWD/target/go-tmp"
(cd infra/agent && go build -p 1 -o ../../target/history-relay-fixture ./internal/sharednode/testdata/history-relay)
node --max-old-space-size=512 infra/agent/internal/sharednode/testdata/lookup-host.mjs target/history-relay-fixture
```

三行 PASS 分别验证：

- 本人当前会话保留；持久化的过期/退出会话、其他成员会话、旧 owner ID 不进入本人列表及消息会话列表。
- W 真正 unavailable 或 Unknown 错误文本为 `common.not_found` 时，两种生产枚举均拒绝；不回显私密异常。
- 权威恢复后重新查询，枚举恢复正常。

Node 只用临时 Ed25519 密钥签合成入口票据，真实签名校验仍由生产 runtime 执行。
测试专用的本地 socket 桥接 Go loopback HTTP server，Windows 用 named pipe、Linux 用 Unix socket。
测试结束关闭桥接及子进程、清理合成 token 和 socket 目录，不保留常驻服务。
`assertProfile` 与 Host 注册接口是组件 fixture，**此结果不是完整 rc.3 loader、真实 DSH、Linux 内核配额或最终 READY 的证明**。

Go 的默认回归不依赖 Node；可以单独运行：

```sh
cd infra/agent
go test -p 1 ./internal/sharednode -run '^TestRelayLookupPreservesHistoricalNotFound$' -count=1
```

内部 socket RPC 继续沿用 HTTP 503 + 白名单业务码：只把 W 的 typed gRPC
`NotFound` 安全译为 `common.not_found`，不按异常文本猜测，也不透传异常详情。
