# 前端与插件开发

Node 22（至少 22.12），pnpm 固定 11.28.6。从仓库根运行：

```powershell
pnpm install --store-dir $env:AURIGA_PNPM_STORE
pnpm gen:api
pnpm gen:steps
pnpm -r lint
pnpm -r typecheck
pnpm -r test
pnpm -r build
pnpm --filter @auriga/panel test:bundle
```

未设置共享 store 的普通环境，安装用 `pnpm install --store-dir "$(pnpm store path)"`。
锁文件在 `web/pnpm-lock.yaml`（根 .npmrc 的 lockfile-dir 配置），CI 使用 frozen-lockfile。
仅允许 esbuild 的依赖构建脚本，其他包审查后加入 pnpm-workspace.yaml 的 allowBuilds。

## 包结构

- `web/shared`（`@auriga/shared`）：主题 CSS、i18n 工厂、API 客户端（problem+json / CSRF / 401）、
  登录步骤渲染器（按 `ClientStep` 类型）、开发用 fetch mock、Placeholder 与契约生成类型。
  workspace 消费者使用源码，Vite 另生成可验证的 ES library。
- `web/portal`：门户。登录（步骤驱动）、邀请注册、手机扫码确认、登录后顶栏和本人实例状态；
  首页只读取身份与实例，不自动创建或启动。用户点击“启动并进入”才调用 workspace 的
  `POST /me/instance/open`，每两秒查询单实例，最多等待两分钟；只有 `running` 或 `pending_restart` 且 `ready` 才进入。
  已就绪实例点击“进入”也先显式 open 刷新或唤醒，再检查最新就绪状态；待重启状态仍显示提醒。
  取消、卸载、失败、401、超时均停止等待，忽略旧响应，支持手动重试，失败写请求不自动重放。
  入口固定使用 `/open/<编码后的实例 id>` 交由 edge 校验会话、主人并换票，不使用响应中的任意 URL。
- `web/admin`：管理台。独立登录、侧边栏（成员 / 组 / 角色 / 身份源 / 系统设置）、成员列表（搜索 + 游标分页）。
- `web/panel`：独立 ES module `dist/auriga-team-panel.js`，导入后注册 `auriga-team-panel`，
  原生 Custom Element/DOM，无运行时框架依赖，Shadow DOM 内置 DSH 主题样式。
- `clients/dsh-plugin`：独立 MIT npm 包 `auriga-dsh`，纯 ESM JavaScript + JSDoc，Node 原生测试，
  托管运行时与默认本地原生页面均走宿主接线，运行期无需前端框架依赖。

管理台与门户使用 ESLint flat config + Prettier、vue-tsc、Vitest / Vue Test Utils、vite build。
原生面板使用 tsc、Vitest + JSDOM 直接挂载 Custom Element 与 vite build；不引入 Vue。
插件使用 tsc checkJs，其 build 脚本为无输出类型校验，因为 Node 22 可直接执行源码。

## 本地 Mock

后端未就绪时，给 Vite 设 `VITE_AURIGA_MOCK=1`（portal / admin 的 `main.ts` 会调用 `enableAurigaMock()`）。
Mock 拦截 `/api/*`：多步登录（选身份源 → 口令 `correct-horse` → TOTP `123456` → 完成）、
口令错误 / `fail-hard` 失败路径、邀请预览码 `invite-demo`、扫码挑战 `qr-demo-challenge`（比对码 `42`）、
管理台成员列表。切真实后端时去掉该环境变量即可，业务代码只依赖 `@auriga/shared` 的 API 封装。
当前开发 mock 不提供 workspace 实例接口；门户实例功能需接真实 workspace，组件测试使用隔离的 fetch 响应，不能作为真实实例已启动的证据。

```powershell
# 示例：只给 portal 开 mock 做联调（不要占用任务要求外的端口以外的服务；本地预览自行决定）
$env:VITE_AURIGA_MOCK = '1'
pnpm --filter @auriga/portal exec vite --port 5173
```

## 管理台角色与邀请

管理台邀请默认选择后端返回的内置 member 角色。创建邀请同时需要邀请读写与角色查看权限
（`org.roles.read`）；缺少角色查看权限时明确提示所需权限，不能提交。此方案保留显式初始角色，
不将空角色数组当作默认 member。授予角色只允许 active 成员；成员详情与角色页均在写前重读
成员状态，服务端在同一结构锁内最终校验。授予或撤销失败后重新读取角色及绑定，并保留原错误提示。
角色相关界面与后端均按权限字符串精确匹配，不将 `*` 或 `org.*` 等字面值扩展为通配授权。
上述角色查看要求适用于管理台确认默认角色的界面；直接调用邀请 API 不额外要求 `org.roles.read`，
仍须具有 `org.invitations.write`，所选角色和分组须通过服务端授予上限校验；空角色集合不会隐式授予 member。
停用、离职及重新启用成员同样要求操作者覆盖目标成员的有效权限（含继承角色及停用期间保留的绑定），
否则返回 `403 org.grant_exceeds_own`；最后 owner 保护继续生效。

## 主题与语言

先导入 Element Plus 样式，再导入 `@auriga/shared/theme.css`。
`--auriga-*` CSS 变量默认采用色相 200–215° 的黑蓝液态玻璃，不使用紫色或发光；
设置文档根 `data-theme="light"` 切浅色。Element Plus 基础颜色映射到同一组变量。
每个 app 调用 `createAurigaI18n()` 创建自己的实例，支持 `zh-CN`、`en`，fallback 为 en。

面板沿 `--dsw-alias-*` 宿主主题，通过元素 `context` 属性读取 locale/theme；原生模块本身不含 Vue/i18n 运行时：

```html
<script type="module">
  import './auriga-team-panel.js';
  const panel = document.createElement('auriga-team-panel');
  panel.context = {
    mode: 'connect',
    protocol: 1,
    locale: 'en',
    theme: 'light',
  };
  document.body.append(panel);
</script>
```

context 是 JS 属性，不是 HTML 字符串属性。资源读取只在已提供真实 `context.fetch` 的 connect 宿主中启用；managed 和未配置安装均保持封闭，不能回退全局 fetch。
`test:bundle` 在空白 JSDOM 文档执行产物，检查元素、Shadow DOM、DSH 样式、无 Vue 运行时及体积预算，不监听端口。
插件的独立安装、设置与登录接线边界见 [plugin-surfaces.md](plugin-surfaces.md)。

## 类型生成

`pnpm gen:api` 使用 openapi-typescript，从
`contracts/openapi/{iam,org,workspace}.yaml` 及引用生成 `web/shared/src/generated/*.ts`，
shared 导出 IAMPaths/OrgPaths/WorkspacePaths 及对应 Components 类型。

`pnpm gen:steps` 使用 json-schema-to-typescript，显式带 `--unreachableDefinitions`，
输出 shared 与插件各自的 `src/generated/steps.d.ts` 后统一格式化。
源 Schema 根没有 properties，必须保留全部 `$defs`，不要手写 ClientStep 等类型。
生成物提交，CI 重生成并检查 git diff 及新增文件。

## 在 worktree 里开发

worktree 的 `node_modules` 是指向主仓库的联接，`pnpm -r` 会拒绝运行。在 worktree 里改了 `web/shared` 想让 portal / admin 立刻用上时，在 worktree 根目录单独 `pnpm install --store-dir $env:AURIGA_PNPM_STORE`；**不要**为此在 vite.config / tsconfig 里加 alias 或 paths。实在跑不了就交给 CI（PR 会跑全部 web 检查）。

## 管理台入口模式与 CSRF

edge 仅在 SPA index 注入唯一 `meta[name=auriga-access-mode]`，值为原部署 Schema 的 `mode`。
`gen:api` 使用固定 json-schema-to-typescript 16 从同一 Schema 生成类型；浏览器仅使用 mode 的 Pick 投影。
管理台缺失、重复或非法 mode 时写请求失败关闭，中英文提示；不从 hostname 或网络错误猜模式。
开发 mock 装配显式 domain 标签，生产不使用默认 mode。门户两模式和 domain 管理台继续使用原 CSRF cookie。

IP 管理台每个 ApiClient/document 以安全随机 32 字节生成无填充 base64url token，只留闭包内存。
首次管理写按正式 POST csrf-tokens 登记；匿名 purpose=login 的创建、advance、cancel 由 IAM 操作状态明确豁免，link/stepup 仍登记。
并发只登记一次，到期按需重新登记；401、登录会话变化及登出使旧在途结果失效。
管理请求拒绝异源目标和 Authorization，固定 same-origin credentials 与 redirect:error，浏览器自行设置 Origin。
业务 403/429/503 不自动重放。登记 429/503 保存同一个候选 token，按 Retry-After 等待后由下一次操作重试；
`ApiError.retryAfter` 暴露原响应头，UI 可据此提示等待。没有 Retry-After 时最少等一秒，不自动循环。
真实双浏览器实例、cookie 端口及完整纯 IP 登录属于 IP3，组件测试不能替代该验收。

## 成员角色与邀请

成员详情通过正式成员 role-bindings 接口读取直接和组继承的绑定；继承项只读，显示来源组名，无组查看权限时显示来源组 ID。
角色读取需要 `org.roles.read`，增删直接绑定需要 `org.roles.grant`；`org.roles.write` 只管理角色定义。
每次确认重新读取操作者权限，提交成功后重新读取成员列表、详情及绑定；最后 owner 和防越权授予由 Org 服务已有约束校验。

邀请可选择契约中的 `member` / `external` 类型和多个初始角色，默认按角色目录发现并选中内置 `member` 角色，类型与角色分别设置。
目录加载失败、缺少角色查看权限或默认角色不可授予时禁止创建并显示原因，避免静默生成无角色邀请；选项恢复后可重试。
角色选项显示内置与自定义角色，超出本人权限的角色不可选。邀请确认展示类型和初始角色，仍不附加分组、不自动发邮件。
