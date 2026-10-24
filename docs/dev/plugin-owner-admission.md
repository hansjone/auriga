# 插件离线 owner 持久准入前置

本模块只提供同进程 Node 能力，尚未装配 HTTP 登录入口、原生 DSH 请求放行或真实宿主登录生命周期。managed、连接总部、会话 cookie 协议均不变。启用离线登录入口仍须单独实现并接受独立安全审查。

## 固定 owner 与初始化

`clients/dsh-plugin/src/identity/owner-admission.js` 导出异步 `createOwnerAdmission({ credentials, owner, initialize = false })`。调用者必须明确提供非空的 `{ providerId, subject }`，两项逐字匹配；不得从第一个浏览器登录自动选 owner。

只访问固定 record key `auriga-dsh/standalone-owner`，kind 为 `grant`，payload 严格为：

```js
{
  version: 1,
  owner: { providerId: 'emergency', subject: 'alice' },
  lastCounter: -1,
}
```

`lastCounter` 只允许 `-1` 或非负安全整数。记录不存口令摘要、TOTP secret、token 或连接器 state，不枚举或改动其他 record。

默认打开要求记录已存在。只有明确的本机预配置／安装操作可传 `initialize: true`，此时缺记录才在同一原子操作中建立固定 owner／`-1`。既有记录的 owner 冲突、kind／version 不符、结构损坏均拒绝，不覆盖、不重置、不迁移。模块打开后记录丢失时，`commitAdmission` 和 `readState` 都失败，不自动重建。

安装阶段的初始化与运行阶段的打开应分别调用；不得在每次运行时自动传 `initialize: true`，否则无法区分首次安装与记录被删除。

## 原子 seam 与存储边界

仅依赖传入的 `credentials.modifyRecord(key, async current => nextOrUndefined)`。初始化、读取和提交都通过此接口；只读回调返回 `undefined`，由 provider 保持记录不变。`readState()` 返回锁内当前 payload 的防御性副本。模块不读取 `readRecord` 的缓存、不修改 provider 返回的对象、不自造文件锁或储密库。

已在本机安装的 `@deepseek-ai/dsh-credentials-local@0.2.0-rc.2` 制品独立核对：`lib/types/index.d.ts` 导出 `LocalCredentialProvider` 及 `modifyRecord`；`lib/index.js` 的操作顺序是进入队列、获取跨进程文件锁、`reconcileFromDisk`、执行回调、`writeFileAtomic`、更新快照、通知监听器、返回。回调返回 `undefined` 时不写文件。原子承诺来自该实际 provider；其他 adapter 必须具有同等语义，不能用普通 get/set 替换。

该 local provider 使用私有凭据文件。它不构成 DPAPI／Keychain 或操作系统加密证明；同用户进程的读取能力仍属于宿主存储边界。本模块保存的 owner／counter 也不是对同用户恶意改盘的防篡改保证。

## 会话前提交

返回的 `commitAdmission(input)` 接受：

```js
{
  providerId: 'emergency',
  profile: { subject: 'alice', mfaPerformed: true },
  emergencyCounter: 123,
}
```

profile 还可带 `displayName`、`email`、`authTime`。只有固定 owner 精确匹配、`mfaPerformed === true`、counter 为非负安全整数且严格大于锁内 `lastCounter` 时，才写新 counter。并发同 counter 恰一成功，较旧 counter 不回退，跨 provider 实例和重启保持。只有整个 `modifyRecord` 成功返回后才返回 `true`；策略 veto 返回 `false`，存储错误抛出固定内部异常 `owner admission unavailable`，不附带原始异常、路径、输入或公开新错误码。

`createStandaloneCore`／`createStandalone` 新增可选异步 `commitAdmission` 选项。装配示意如下，`credentials` 和 `emergencyConfig` 均由已受信的本机调用方提供：

```js
const owner = { providerId: 'emergency', subject: 'alice' };
const admission = await createOwnerAdmission({ credentials, owner });
const runtime = await createStandalone({
  commitAdmission: admission.commitAdmission,
  config: {
    owner: { providers: [owner.providerId], subject: owner.subject },
    connectors: [
      { type: 'emergency', id: owner.providerId, config: emergencyConfig },
    ],
  },
});
```

core 先验证连接器结果和既有 owner policy，并在完成点原子消费内存 counter，随后 await 持久提交。传给 hook 的只有白名单身份副本与该事务验证结果自身的 counter；非 emergency 连接器不传 counter。core 不拿全局 counter 冒充本次 OTP，也不传连接器 state 或配置。

提交返回 `false` 映射现有 `iam.admission_denied`，抛错映射现有 `iam.connector_unavailable`。此时事务终态、state 清空，不写首次 owner／identity，不签发 session／CSRF／Set-Cookie；已消费 counter 不回滚。存储已提交后再抛错同样拒绝签发，下一次使用更新的 OTP。提交成功后 core 再检查内存 owner 并完成认领，处理其他事务在 await 期间的竞争。未提供 hook 时保持现有内存模式，仍修复不同事务并发复用同 OTP 的窗口。

## 验证

常规测试包含 `test/standalone-core.test.js` 和 `test/owner-admission.test.js`。真实制品探针为显式 opt-in，不新增依赖、不改 CI。在插件目录设置 `AURIGA_TEST_DSH_PACKAGE` 为已安装 DSH 包目录后运行：

```powershell
node --test test/owner-admission.test.js
```

探针仅在当前工位目录创建随机 `.owner-admission-fixture-*` 私有目录，明确将其传给 provider；子进程的 `DSH_HOME`／cwd 也指向该目录。它验证实际 rc.2 的两个 provider 对象、两个子进程同 counter 竞争、第三个重启进程的重放拒绝、core 正向会话前持久提交、记录删除后的登录拒绝、损坏文档、真实 I/O 失败和固定 owner 冲突。结束时关闭 provider／子进程并移除自己的临时目录，不读取真实 DSH_HOME、已有凭据或 NAS。未设置变量时只跳过真实制品用例，常规逻辑测试照常运行。
