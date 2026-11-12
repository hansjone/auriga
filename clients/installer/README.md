# Auriga Desktop installer (Windows test build)

设计见 `docs/design/dsh-installer.zh.md`。包一层不 fork：上游 `@deepseek-ai/dsh`、自带 Node 与 pnpm 是可替换组件，我们的插件、首启配置与安装核心单独存放。

```powershell
cd clients/installer
node scripts/build.mjs --exe                 # 默认上游版本见 upstream.json；--exe 同时出单文件安装器
node scripts/build.mjs --upstream 0.2.1-rc.4 # 换上游版本重新出包
node scripts/build.mjs --platform-origin <https 地址> --without-libreoffice   # 首启默认平台地址；精简版
pnpm test                                    # 单测；设置 INSTALLER_STAGE=<包目录> 额外校验成品包
node scripts/e2e.mjs --old <包目录|setup.exe> [--new <包>] [--broken <包>] --sandbox <目录>
```

本包是 pnpm 工作区成员（`@auriga/installer`），无第三方依赖，脚本只用 Node 内置模块。输出默认在 `target/`（已被 `.gitignore`）。

## 发布新版本

发行清单使用独立 Ed25519 离线钥。不得使用消息网络、DSH runtime 或插件市场的私钥。
首次由运营者在离线机器上生成并安全保存私钥，限制目录 ACL 为运营者本人；公钥可公开。
例如使用 OpenSSL（私钥输出直接写文件，不打印）：

```powershell
openssl genpkey -algorithm ED25519 -out <离线目录>/desktop-private.pem
openssl pkey -in <离线目录>/desktop-private.pem -pubout -out <发行目录>/desktop-public.pem
```

1. 递增插件/发行版本，构建时固定首启平台 origin 和发行公钥：

   ```powershell
   node scripts/build.mjs --platform-origin https://platform.example.com --release-public-key <发行目录>/desktop-public.pem
   ```

   公钥写进包内 `payload/auriga/runtime/release-public-key.txt` 并参与包 ID 摘要。
   客户端只读这个固定位置，不从清单、环境变量或平台接口导入信任钥。
   不传公钥的开发包会拒绝更新。已有旧安装器需先手动安装一次带此能力的包。
   同一发行链后续构建保持同一公钥；换钥需要由旧钥授权的安装包带入新公钥，或人工重新安装。

2. 将完整 ZIP、对应 payload 和离线签名脚本交给离线签名机。序号是正整数，严格递增；
   即使下架也不能重用。设置明确的最低可升级版本、过期时间和发布说明：

   ```powershell
   node scripts/sign-release.mjs payload=<包目录>/payload archive=<完整安装包.zip> keyFile=<离线目录>/desktop-private.pem sequence=2 minimumVersion=0.1.15 notAfter=2026-12-12T00:00:00Z notes=修复与改进 output=<发行目录>/desktop-release.json
   ```

   参数使用 `name=value`；带空格的参数整体加引号。输出文件必须尚不存在。
   私钥只由该脚本读取，不进入包、服务端、仓库和脚本输出。测试钥只在测试沙箱临时生成。
   私钥离线加密备份、双人保管；丢失时使用人工安装更换信任根，不接受服务器覆盖客户端公钥。

3. 平台 resource 服务配置：

   - `AURIGA_RESOURCE_DESKTOP_DIRECTORY=/var/lib/auriga/desktop`：独立持久卷，服务账号独占写入。
   - `AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE=/run/auriga/desktop-public.pem`：与构建时相同的公钥，只读挂载。
   - 反向代理允许该上传路由的请求体达到实际包大小（产品上限 4 GiB）、足够的上传超时和至少 44 KiB 请求头。
     清单在 `X-Desktop-Manifest` 中采用 base64url UTF-8；通常仅数 KiB。
   - 单个本地持久卷实例；跨实例共享存储必须支持 OS 文件锁、原子 rename。禁止使用不支持这些语义的对象挂载。

   不配置上述参数时，桌面发行接口不启用。平台不需要发行私钥。
   插件市场对象存储限制为 64 MiB，故本功能单独存储；按租户目录隔离。
   发布、下架和审计记录在同一原子提交的 `state.json` 中持久化，当前未汇入平台统一审计检索页。
   卷应连同安装平台的备份策略备份；首次发行前确认磁盘容量。异常退出留下的 `.upload-*.partial` 或孤立 ZIP 由停服维护清理。

4. 管理台进入“运维 → 客户端版本”（`/ops/desktop`），验证身份后选择已签清单和 ZIP，点击发布。
   需要 `resource.versions.publish`、有效管理会话及近期 step-up。服务端验证签名、有效期、完整 SHA、
   大小、版本和序号后才发布。下架永久隐藏该版本，保留序号和审计；重新发行须新版本、新序号。
   清单过期后不再对外分发，可在到期前发布下一个版本。

5. 门户登录后的首页显示下载区。下载与清单接口要求当前成员/设备认证及 `resource.packages.read`；
   不开放匿名下载，安装平台成员边界与现有插件市场一致。
   客户端使用首启绑定 origin 的 HTTPS 接口、现有平台登录令牌，禁止重定向。

6. 客户端启动检查一次，之后每 6 小时检查；未登录时检查可能失败，登录后可从原生设置的 Auriga 页手动检查。
   设置页可关闭自动检查。点击“更新”后只下载并校验暂存；**关闭整个 Auriga Desktop 进程后安装**，
   不仅是关闭浏览器页。启动器在 DSH 退出后运行原有安装事务，下次打开使用新版。
   冒烟失败自动还原旧版和 profile，数据目录保持原位；显式回滚仍使用 `rollback.cmd`。
   启动器用进程锁限制同一安装目录只运行一个 DSH，避免更新时改动另一活跃会话。
   子进程仍活跃时重复启动会被拒绝；异常退出后会检查旧进程是否存活。

隔离验证：`node --test test/update-e2e.test.mjs`。它使用随机端口发布替身、真实 ZIP、签名与安装事务，
验证检测、坏包拒绝、退出安装、数据保留；冒烟使用测试替身，不能替代真实 DSH 宿主验收。
