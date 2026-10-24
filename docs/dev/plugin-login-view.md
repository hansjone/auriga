# 原生登录步骤模块

`clients/dsh-plugin/src/login/index.js` 提供无框架、无构建要求的 ESM 模块及同名类型声明。宿主可直接加载模块，不依赖已授权 RPC 的 main 插槽；由宿主提供专用登录容器、DSH boot 样式和主题变量。布局参考既有 gate/desk 的品牌、居中卡片与标准表单，品牌为 Auriga，字体继承，颜色沿 `--dsw-alias-*`，没有独立主题调色板或系统主题监听。

## 接口

`createLoginView(root, options)` 返回 `setStep(step)`、`setLocale(locale)`、`dispose()`。`options.locale` 默认 `zh-CN`；宿主应传入当前语言（`zh-CN` / `en`）。步骤的 LocalizedText 优先匹配该语言标签，缺失回退 `en`。统一界面文案支持中英文。

- `onSubmit(input, { signal }) => Promise<ClientStep>`：提交生成契约的 `StepInput`，表单输入为 `{ type: 'form', formId, values }`。
- `onRestart({ signal }) => Promise<ClientStep>`：用户显式重试可重试失败步骤。
- `onComplete()`：当前有效 `complete` 步骤通知宿主；模块不使用 `redirectTo` 导航、不创建会话、不自行认定真实登录成功。

ClientStep / StepInput / 字段定义均来自插件 `src/generated/steps.d.ts`。宿主负责步骤数据的协议验证、真实 transport、登录事务绑定、会话确认和最终导航。宿主首次取到步骤后调用 `setStep`，更换事务时再次调用；语言改变调用 `setLocale`，退出登录容器调用 `dispose`。容器中的其他兄弟节点不被替换。

## 当前支持与安全边界

支持 form、fail、complete。表单处理 text、password、email、tel、otp、select、checkbox、hidden、info，以及 label、placeholder、help、必填、长度、pattern、autocomplete、字段错误与整表错误。info 不提交，checkbox 提交布尔值，hidden 按契约保留原始 string 或 boolean 值（未提供值时提交空字符串），不通过 DOM 字符串转换。浏览器原生约束校验通过后才提交。password/otp 不使用上游默认值，提交后立即清空；取消、切步骤、切语言、卸载也清空现存控件。模块不写 storage、URL、日志。

fail 与表单错误仅按已知契约 code 显示本地受控文案，未知错误回退通用失败；不回显服务端 message 或异常 reason/stack。只有 retryable 的 fail 显示重启按钮。qr、redirect、webauthn、device、done 及未知步骤均显示暂不可用，不产生完成通知或导航。

提交中禁用现有控件，显示处理状态与取消按钮。取消、替换步骤、语言变化、卸载会 abort 请求并使旧响应失效；宿主即使不响应 abort，晚到结果也不会替换当前步骤。dispose 幂等并移除本模块 DOM 和事件监听。完成通知在微任务中检查有效性；切语言保留当前有效 complete 的待通知状态，已通知后不重复通知。替换步骤、取消或卸载仍使旧完成通知失效。

## 验证与未接范围

DOM 行为测试位于 `web/panel/test/login-view.test.ts`，使用已有 Vitest/jsdom；插件本身不增加 DOM 测试依赖。测试覆盖实际输入、多步、所有字段类别、原生验证、密码与 OTP 清理、受控错误、未知步骤关闭、取消/重启/卸载与晚到响应、主题继承及宿主区域保护。

真实 HTTP、匿名页 DSH boot 样式加载、路由/入口安装接线、cookie/会话门锁和真实 DSH 登录均未接入，本批产物不能作为真实 DSH 登录完成的证据。没有新依赖、网络调用或凭据处理。后续装配由主控统一安排。
