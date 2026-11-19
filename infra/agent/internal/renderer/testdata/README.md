# 完整 shared launcher 纯验证

本夹具消费生产 `RenderPool` 的实际只读产物（包括其嵌入的 `pool-launcher.mjs`），
并使用锁定 rc.3 的真实公开 SDK exports、normalizer、`composeEntries` 和
`ClientModuleRegistry`。不启动 DSH、容器、PG、浏览器或任何供应商服务。

在仓库根运行，Go 缓存和临时目录均在本工位 target：

```powershell
New-Item -ItemType Directory -Force target/go-build,target/go-tmp | Out-Null
$env:GOTOOLCHAIN='go1.27.3'
$env:GOMAXPROCS='2'
$env:GOMEMLIMIT='512MiB'
$env:GOCACHE=Join-Path (Get-Location) 'target/go-build'
$env:GOTMPDIR=Join-Path (Get-Location) 'target/go-tmp'
Push-Location infra/agent
go run -p 1 ./internal/renderer/testdata/render-shared-profile ../../deploy/nas/shared-runtime-profile.example.yml ../../target/launcher-fixture/meta
Pop-Location
$env:AURIGA_DSH_PUBLIC_PACKAGE_ROOT='<批准的rc.3套件>/payload/upstream/app'
$env:AURIGA_RENDERED_SHARED_PROFILE=(Resolve-Path target/launcher-fixture/meta).Path
node --expose-internals --max-old-space-size=512 --test infra/agent/internal/renderer/pool_launcher.test.mjs
```

Linux 使用相同 Go 子命令与 Node 命令；环境变量以 shell export 设置，并使用绝对路径。
产物不可覆盖；源码变更后应选一个新的夹具输出目录，保留原首失或旧产物。
SDK root 环境变量只供测试定位已批准包，生产 launcher 无此配置入口。

实际 rc.3 `boot(binName, absoluteConfigPath, patches, prepare, bareModuleBaseUrl)`
只接受配置路径。Node renderer 写入固定只读 `pool-entry.yml` 空数组，launcher
在真实 boot 前把经过完整 Go 允许表的逻辑 profile 用固定 SDK normalizer 转换，
经 `[{insert: normalizedRows}]` 交给公开 boot，继续传第五参数
`file:///opt/dsh/package.json`。测试对真实公开 `composeEntries` 核对最终完整 60 行，
避免误把数组塞给路径参数，或只检查转换却实际启动裸包。

测试的 boot 是参数观察边界，仅提供 launcher 原有的 `profileContext`/`appReady`
元数据；它不是 DSH Loader 的实际启动或原生页面验收。SDK 模块和 client metadata
解析使用真实公开实现。完整服务构造、Linux 进程和正常浏览器仍由主控串行验证。

Dedicated 的独立准备夹具同样不 boot 完整 DSH。它运行生产嵌入的 helper，真实读取
rc.3 持久 profile/bundle/user/home 层，比较 TGZ 与安装树，执行公开 normalizer 与
resolution；launcher 的 boot 仅观察参数，但实际接入公开 PluginPackages。
Linux 地址只由测试 builtin seam 映射到本地合成目录，不进入生产环境或配置。

```powershell
$env:AURIGA_DEDICATED_PREPARATION_FIXTURE=Join-Path (Get-Location) 'target/dedicated-prepare-fixture'
$env:AURIGA_DEDICATED_FIXTURE_OUTPUT=Join-Path (Get-Location) 'target/dedicated-public-resources'
Push-Location infra/agent
go test -p 1 ./internal/renderer -run '^TestDedicatedRendererExportsClosedPreparation$' -count=1
go test -p 1 ./internal/runtime -run '^TestDedicatedMaterializerAcceptsSignedOptionalSkillAndPlugin$' -count=1
Pop-Location
node --max-old-space-size=512 infra/agent/internal/renderer/dedicated_prepare.test.mjs '<批准rc.3套件>/payload/upstream/app' target/dedicated-prepare-fixture target/dedicated-public-resources/plugin.json
$env:AURIGA_DEDICATED_PUBLIC_PREPARED=Join-Path (Get-Location) 'target/dedicated-prepare-fixture'
Push-Location infra/agent
go test -p 1 ./internal/runtime -run '^TestDedicatedPreparedOutputHasClosedAuthority$' -count=1 -v
Pop-Location
```

首个 Go 命令使用与前文相同的 Go1.27.3、2 CPU、512MiB 和 target 缓存设置。
三个 SDK 子测试包括空 profile 的保持、当前选装包与 user/home 叠层、实际可写
profileContext、第五锚与空 patches，以及包/基线/工具漂移反例。最终 Go 命令再核
SDK 真产物的完整身份、config SHA、精确封存基线与封闭文件集合。没有启动真实
pnpm 安装、容器或供应商进程；实际 Linux 安装/挂载与正常市场页面仍需主控门禁。
