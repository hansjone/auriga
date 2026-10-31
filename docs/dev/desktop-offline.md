# 桌面离线准入：实现与验收限制

本批次为待审查的部分实现，不能作为 DSH 0.2.1-rc.3 桌面离线支持已交付的依据。

## 已实现的边界

- 原连接服务验证成功的独立 owner API Peer 不依赖 Auriga 平台会话；自称原生的请求头没有授权效果。
- 保存的平台地址在离线时仍能加载。后台恢复只创建平台运行时内部状态，不给浏览器签发 Cookie。
- 刷新前先做只读可达性检查。明确的连接未建立错误可重试；响应丢失等不确定结果仍保留旋转令牌防重放栅栏，要求重新登录。
- 登录页发现平台提供的表单登录方式，包含 emergency；选择必须来自该次服务器返回的列表。目前仍只支持既有 form/fail/complete 事务步骤。
- 本地应急账号复用 standalone emergency core；使用 Argon2、口令和 TOTP，凭据及已接受 counter 保存在加密 vault。重置产生随机口令，只在该次 owner 调用返回，旧会话失效。
- 本地应急 Cookie 不含平台令牌，网络、消息、授权接口拒绝该会话。平台退出不撤销已有本地应急会话。

## rc.3 现场阻塞

已安装的 rc.3 Electron 主进程使用 `authenticateWebHost()` 从启动 URL 获取浏览器 Cookie，再用 `forwardWebRequest()` 转发应用请求。转发会删除 Origin、Cookie、Host、Sec-Fetch-Site，并注入同一个宿主浏览器 Cookie；WebSocket 也使用这个 Cookie。它不是独立 owner API Peer。

因此，插件只根据原生准入结果和公开连接 API，无法可靠区分这个桌面窗口与持有合法宿主 Cookie 的浏览器。按独立 API Peer 放行，真实窗口的离线业务请求仍失败；按共享 owner 放行则会扩大浏览器授权。不能用可伪造的来源头解决。

当前保留原有平台登录通道及四个脱敏欢迎页读取接口，避免新增启动崩溃；应急凭据重置只允许独立 owner API Peer。因此 rc.3 现有窗口还不能完成首次应急账号生成。

主控需协调宿主提供原生窗口专属、可验证且浏览器不可取得的凭据或公开载体。还需核验本地应急会话与 DSH 全局模型/工具注册的隔离：当前网络路由的拒绝不能证明所有会话工具、模型调用均已隔离。

## 隔离窗口验收工具

`e2e/phase1/desktop-offline.mjs` 使用已安装 Electron、全新 profile 和本地模型 HTTP fixture。它只结束自己启动的进程，不修改系统网络。平台凭据仅读取私有 fixture，不写入公开报告。

参数依次是私有 fixture 目录、插件 tgz、新建的 `e2e/target/` 子目录。私有目录需含运行时描述、平台连接配置、团队及平台应急 enrollment。脚本依赖同目录名为 `auriga-offline-isolation-0.0.0.tgz` 的测试插件包：将 `desktop-offline-isolation.mjs` 作为包入口，使用标准 DSH bundle patch 启用同名插件。生产插件不包含断网开关。

断网插件只影响测试进程中指定平台目标的 fetch/TCP。验收必须观察网络断开和恢复，不能仅凭切换文件宣称隔离有效。当前验收在首个离线本地请求失败，后续团队 READY、恢复 READY、平台应急登录均未执行；未更新日常插件。
