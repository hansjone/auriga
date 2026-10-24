# 本机插件管理

Auriga 原生“资源市场 / 我的资源”两页签中，“我的资源”读取当前 DSH 的 `pluginManager` 服务。市场不可达不会触发市场缓存或把包元数据推断为本机安装；本机服务不可用则显示实际错误。

## 当前闭环

- 列表显示实际宿主条目、可取得的包版本、当前启用状态与 fiber phase，支持搜索、详情、刷新。按宿主 profile/installation 根只读解析已登记的合法 npm 模块名、核入口普通文件及包版本，不执行模块；缺少解析能力或仅有未安装 profile 声明时显示“安装状态待核”并禁止修改，不能一律标成已安装。
- 普通本机环境对宿主可寻址且允许管理的第三方插件提供启停，保留宿主热重载、持久 profile 和失败语义；未热重载时显示需重启，同时仍呈现真实运行态。
- 配置读取 `settings.describe({redactSecrets:true})`，仅编辑声明为安全非秘密的顶层标量。提交 field mutations 与宿主 revision，保留未编辑、秘密、复杂字段；未知 schema 只读并明确范围。宿主负责最终 schema 验证与 profile 持久化。
- 修改前确认、可取消确认；发出后不承诺撤回、不自动重试。页面刷新失败清除旧快照；切页、撤销 context、卸载后的晚到结果不能恢复旧界面或操作状态。
- managed 仅显示受原身份门锁保护的本机库存，不允许修改。Auriga 自身、DSH 内置与安全边界模块及原宿主只读条目均不能经此接口操作。

接口见 `contracts/plugin/local-resources.schema.json` 及其 README。独立本机 transport 不使用平台成员代理，不放开 CORS，不改变 Host/Origin/native browser authentication，不证明 Auriga 用户已登录。

## 宿主与并发边界

服务经 Cordis 注入，缺少 pluginManager 返回 unavailable；缺少 settings 时配置不可用。只读宿主根限定的已登记包入口/manifest，不直接写 profile，不运行 pnpm、不下载。写入前重新获取目标快照并检查 opaque ID、已安装来源、受保护模块/宿主权限、快照 revision、服务身份与请求认证。

本接口串行化自己的修改；启停底层只接收条目 ID 和期望布尔值，**没有跨宿主其它入口的原子 CAS**。外部入口的并发修改或条目替换仍受原宿主自身检查/锁约束，本接口不能承诺封住所有竞态，必须以实际读回为准。配置另将 native revision 交给 `settings.mutate`，由原生编辑器锁内复核，不能将其保证推广到启停。宿主扩展代码与同权限本机操作者仍是既有信任边界，不以名称过滤宣称完整沙箱。

## 聚焦验证

插件执行 `node --test test/local-resources*.test.js`。组件执行 panel 的 `vitest run test/local-resources.test.ts`；schema 使用现有 contracts 工具的严格 2020-12 编译和专属契约用例；生成类型：

```sh
node node_modules/json-schema-to-typescript/dist/src/cli.js -i contracts/plugin/local-resources.schema.json -o clients/dsh-plugin/src/local-resources/generated.d.ts --unreachableDefinitions
```

可选真实宿主测试设置 `AURIGA_DSH_PACKAGE_JSON` 为已安装固定 DSH 0.2.0-rc.2 的 package.json，执行 `clients/dsh-plugin/test/local-resources-host.test.js`。测试只在全新临时 profile、凭据文件及随机 loopback 端口中加载实际 Include/Loader/pluginManager/settings/connection，交换实际签名 cookie、验证写入与 Origin/认证拒绝，结束关闭 server/context 并删除本次临时目录。不指定该制品时具名 skip，不能冒作集成通过；该模块级证据也不等于 Desktop 真页面/完整运行实例验收。

## 尚待继承

市场安装/下载/签名发布、更新、卸载、个人上传审核、Skill 库存与工具部署仍需单独打通，不因本页面尚缺接口而删除目标。当前不接受任意 spec、URL、本地包路径或安装脚本，不直接搬旧 Hub 路由。managed 新资产仍走原签名/SRI 发布，未发布到平台不表示现场已升级；插件独立包需包含更新后的同一原生 UI。没有操作真实 DSH_HOME、旧 Team 服务、NAS 或部署环境。
