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

- `web/shared`（`@auriga/shared`）：主题 CSS、i18n 工厂、Placeholder 组件与契约生成类型。
  workspace 消费者使用源码，Vite 另生成可验证的 ES library。
- `web/portal`、`web/admin`：Vue 3 + TypeScript + Vite，注册 Pinia、Vue Router、Element Plus；
  仅可切换中英文/深浅主题的占位首页，不实现登录或业务。
- `web/panel`：独立 ES module `dist/auriga-team-panel.js`，导入后注册 `auriga-team-panel`，
  Vue 运行时打入单文件，Shadow DOM 内置主题和组件样式。
- `clients/dsh-plugin`：独立 MIT npm 包 `auriga-dsh`，纯 ESM JavaScript + JSDoc，Node 原生测试，
  三种模式仅选择空实现，运行期无需前端依赖。

各 web 包都有 ESLint flat config + Prettier、vue-tsc、Vitest / Vue Test Utils、vite build。
插件使用 tsc checkJs，其 build 脚本为无输出类型校验，因为 Node 22 可直接执行源码。

## 主题与语言

先导入 Element Plus 样式，再导入 `@auriga/shared/theme.css`。
`--auriga-*` CSS 变量默认采用色相 200–215° 的黑蓝液态玻璃，不使用紫色或发光；
设置文档根 `data-theme="light"` 切浅色。Element Plus 基础颜色映射到同一组变量。
每个 app 调用 `createAurigaI18n()` 创建自己的实例，支持 `zh-CN`、`en`，fallback 为 en。

面板不依赖宿主 Vue/i18n 或全局 CSS，通过元素 `context` 属性读取 locale/theme：

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

目前只读 locale/theme，其余协议字段留待业务接入。context 是 JS 属性，不是 HTML 字符串属性。
`test:bundle` 在空白 JSDOM 文档执行产物，检查元素、Shadow DOM 和内置样式，不监听端口。

## 类型生成

`pnpm gen:api` 使用 openapi-typescript，从
`contracts/openapi/{iam,org,workspace}.yaml` 及引用生成 `web/shared/src/generated/*.ts`，
shared 导出 IAMPaths/OrgPaths/WorkspacePaths 及对应 Components 类型。

`pnpm gen:steps` 使用 json-schema-to-typescript，显式带 `--unreachableDefinitions`，
输出 shared 与插件各自的 `src/generated/steps.d.ts` 后统一格式化。
源 Schema 根没有 properties，必须保留全部 `$defs`，不要手写 ClientStep 等类型。
生成物提交，CI 重生成并检查 git diff 及新增文件。
