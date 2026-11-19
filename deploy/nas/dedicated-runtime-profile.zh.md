# 显式 dedicated 的完整原生启动配方

[dedicated-runtime-profile.example.yml](./dedicated-runtime-profile.example.yml) 是完整 Linux entry-list：82 个锁定 rc.3 SDK 模块和唯一 `file:///opt/auriga-dsh/src/dedicated/plugin.js`。它提供正常问答、PluginManager、私有 Skill、真实 Jobs/controller、Linux Shell/subprocess/sandbox/approval 及对应原生界面。它不叠 stock `dsh-base`/`dsh-web-app`，不加载 shared 或旧 Auriga owner 主入口。身份及本人 owner 派生仍由原 dedicated 模块的 fresh E/W/K 围栏负责。

公开解析代码位于 [profile-resolution.js](../../clients/dsh-plugin/src/dedicated/profile-resolution.js)。Node C 消费批准包内这个固定文件，无需新增 package export，也不能从 YAML 传 resolver。

## 固定文件和持久目录

- `/opt/dsh/package.json`：只读 SDK 安装锚，对应正式套件 `payload/upstream/app`。
- `/opt/auriga-dsh`：只读 Auriga 插件；唯一 dedicated file URL 同公开 `./dedicated` export 目标，ClientModuleRegistry 能由此发现同包 `dsh.client`。裸 `auriga-dsh/dedicated` 不具备这个元数据发现效果。
- `/opt/auriga-tools/pnpm`：只读工具目录，对应正式套件 `payload/tools/pnpm`；真实 CLI 为 `node_modules/pnpm/bin/pnpm.cjs`，不在 upstream/app/node_modules。
- `/home/dsh/.host`：现有硬配额 home。工作目录 `/home/dsh/.host/workspace`，DSH home `/home/dsh/.host/.dsh`，私有 agents 根 `/home/dsh/.host/.agents`，可写持久 profile `/home/dsh/.host/.dsh/profiles/auriga-dedicated`。
- session、storage、profile、pnpm store/cache 与既有资源状态均写入该 quota home。SDK、Auriga、工具、Node 已验资源缓存保持只读。

profileContext 固定 name=`auriga-dedicated`、dir=上述 profile、installAnchor=`/opt/dsh/package.json`、patchPath=`<profile>/cordis.patch.yml`、home=`/home/dsh/.host/.dsh`、cwd=`/home/dsh/.host/workspace`、overlays=`[]`。Node 控制启动环境和这些路径，清除可变 `DSH_BUNDLED_SKILL_DIR`；SDK 锚和 owner 不从环境或用户配置选择。

`profileContext.packageManager` 使用安装侧已校验的 `process.execPath`，参数固定为：

```js
{
  command: process.execPath,
  args: [
    '/opt/auriga-tools/pnpm/node_modules/pnpm/bin/pnpm.cjs',
    '--offline', '--ignore-scripts',
    '--store-dir', '/home/dsh/.host/.dsh/pnpm-store',
  ],
  env: {
    COREPACK_ENABLE_NETWORK: '0',
    PNPM_HOME: '/home/dsh/.host/.dsh/pnpm-home',
    XDG_CACHE_HOME: '/home/dsh/.host/.cache',
  },
}
```

该对象由 Node 安装代码构造，不能来自 browser/YAML 命令字段；Node 必须先校验运行中 Node executable 和 pnpm CLI 的只读安装映射。它沿现有资源归档契约：所有依赖须已完整 bundled，所有根及捆绑 package.json 禁止 lifecycle scripts，peer 仅批准 SDK。`--offline/--ignore-scripts` 不新增用户操作；缺依赖、工具或脚本要求明确失败，不临时联网下载。真实 pnpm 安装进程仍须由主控/Node 集成验收，本配方纯测试不代替它。

## 启动、公开叠层与精确入口

Node 先创建 quota 目录并验证文件归属，调用公开 `initProfile(dir, [])`，仅初始化不存在的文件，不覆盖既有 profile。然后 `loadProfileDirectory('auriga-dedicated', dir, '/opt/dsh/package.json')`，显式拒绝非空 `skippedBundles`。已选 bundle、profile依赖及用户层必须来自当前成员/当前意图的受信 Node 校验，读取它们本身不代表完成签名或 ACL 校验。

调用步骤为：

```js
const baseline = resolveDedicatedRuntimeProfile(renderedLiteralRows);
const patches = readProfilePatches('auriga-dedicated', profileContext, loaded);
const composed = composeEntries([[{ insert: baseline }], patches]);
const resolved = resolveDedicatedResourceProfile(
  baseline,
  composed,
  approvedRows,
);
const resolution = await createRuntimeResolution({
  installAnchor: '/opt/dsh/package.json',
  profile: loaded,
  home: profileContext.home,
});
// Node 串行停旧实例后，将 resolved 原子写入该进程只读的生成配置文件。
await boot(
  'auriga-dedicated',
  generatedConfigPath,
  [],
  async (ctx) => {
    ctx.provide('profileContext', profileContext);
    await ctx.plugin(PluginPackages, { resolution });
    // 保留既有已验 appReady/launchEnvironment 等真实宿主 provider。
  },
  'file:///opt/dsh/package.json',
);
```

公开 `readProfilePatches` 的顺序为 selected bundle list → profile user patch → 固定 DSH home patch → Node 固定 overlays。最终已组合且已解析的配置传给 `boot`，patches 参数为 `[]`，避免再次插入同一 bundle。运行期间不改这个只读生成文件触发 Include 热重载；默认无 HMR，变更由 Node 受控排空重启。

`PluginPackages` 来自同 SDK 的公开 app-boot。它负责已验证 profile 依赖及其内部 SDK/携带依赖的正常解析；它不会让 SDK root 能解析另一个 profile 下新装的裸根包。因此资源新增行也必须精确解析成它自己的公开模块 file URL，不能仅设置 boot 第五 SDK 锚。

`approvedRows` 精确形状为 `[{id, packageName, specifier, version}]`，只能由 Node 从当前签名资源 manifest、已验证包/patch 字节及当前已选 bundle 行导出，不能把最终用户 YAML 自报的路径变成 approval。`resolveDedicatedResourceProfile` 会：

1. 核基线行全部仍在且逐项未改，拒第二个 SDK/Auriga provider。
2. 仅从固定可写 profile 的 node_modules 通过公开 `createRequire.resolve` 解析批准包名/子路径，核实际最近 manifest 的 name/version，realpath 必须留在该 profile 安装树内。
3. 拒未知行、版本/别包失配、SDK 替换、路径越界、任意 file URL、可执行 YAML 和未支持 group。已有 file URL 也必须精确等于当前批准 public module 的解析结果。

当前支持 flat literal 资源行；嵌套 group 或要覆盖基线的 bundle 明确拒绝。本函数不负责补齐 Node 的签名、租约、高水位、实际下载/安装字节或 W 归属权威。正常资源生命周期仍按原 Host receipt：PluginManager install/enable/remove 与实际 active entryId 观察；无 HMR 时报告 restart-required，新进程再读已启用 bundle、实际 active 后才 ready。

## 摘要与基线边界

`DEDICATED_BASELINE_DIGEST` 封存模板顺序、82模块、固定配置与唯一路径。仅 Node 受信 renderer 可替换 listener host/port、平台默认 model、pool UUID 对应的 dedicated-host.json。代码把这几项规范化为模板值后比较摘要；此模板摘要不能代替实际 binding 或启动配置摘要。Node 还须为实际 resolved 配置、已验证资源 view/intent 和本次完整 binding 分别保精确摘要及前后围栏。

`assertDedicatedBaselinePreserved` 只保护基线，不授权额外模块；Node 必须继续执行 current resource approval。用户层可配置已批准资源自己的行，不能改 DED入口、SDK/工具链/配额路径、私有Skill或native tools等固定行。在线 ConfigEditor/Settings 不在本批；PluginManager 的必需注入只有 Loader/profileContext，不用装任意宿主配置编辑通道。

SDK 的 ui-jobs 引用 `dsh-client-ui-primitives`，它是已验 web-frontend 的静态模块，不是一个可加载的 Host 包；配方没有造这个不存在的包。纯测试从真实 dist 入口的 staticModules 映射验证此项，并检查其余全部 client inject 和资产闭包。

本批以真实公开 Config/inject/ClientModuleRegistry、私有 Skill、惰性 Shell/Jobs provider 组合、单一无副作用资源 service 的 profile/bundle 重启读取证明这些接口。未启动完整 DSH、真实 Shell、NAS、PG 或模型供应商；Linux bash/containment、工具镜像映射、真正包进程、正常资源市场页面和最终四cap/receipt 均由主控后续实测。
