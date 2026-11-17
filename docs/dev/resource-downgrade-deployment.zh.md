# 开发环境的独立降级根与云端更新

统一资源的自动回落使用独立 `purpose=downgrade` 根。发行根、撤销根和 runtime
镜像发行根用途不同，不能互相替代。缺少降级根时回落失败关闭；正常撤销仍必须停用。
本节只描述已有开发环境的增量配置，不授权轮换现有根或清理历史高水位。

## 准备新根

在可信机器或无网络的一次性密钥准备容器中，对全新、受保护目录运行：

```sh
node scripts/resource-keys/generate.mjs \
  --out /srv/auriga-secrets/new-development-roots \
  --kid-prefix development-resource-unique-batch
```

生成器创建三把彼此独立的 Ed25519 根。已有环境只引入本次生成的
`offline/downgrade.pk8` 和 `resource-roots.json` 的 `downgrade` 公钥，
保留原 release/revocation 配置与字节；不要把新生成的另外两把根配置到现有服务。
私钥不得出现在终端输出、传输日志、报告、公开包或仓库中。

## 先部署节点公钥

节点读取 `<AURIGA_AGENT_DATA_ROOT>/agent/resource-trust.json`。
保留原 `schemaVersion=1`、受信 `origin` 与 `keys`，追加一项：

```json
{
  "kid": "development-resource-unique-batch-downgrade",
  "purpose": "downgrade",
  "publicKey": "<Ed25519 raw32 的标准 base64>",
  "notBefore": "2026-11-18T00:00:00Z",
  "notAfter": "2027-11-18T00:00:00Z"
}
```

期限由部署操作者明确批准，示例时间不是默认值。生成器提供的是 SPKI DER，
不能直接填入 `publicKey`。使用 Node 标准密码 API 转换：

```js
const spki = Buffer.from(roots.downgrade.publicKeySpkiBase64, 'base64');
const x = createPublicKey({ key: spki, format: 'der', type: 'spki' })
  .export({ format: 'jwk' }).x;
const publicKey = Buffer.from(x, 'base64url').toString('base64');
```

其中 `createPublicKey` 来自 `node:crypto`。记录 kid、raw32 公钥 SHA-256、
SPKI SHA-256 和最终信任文件 SHA-256，不记录私钥。通过已有配置部署脚本备份并
原子替换受保护信任文件，保留属主与权限，然后重启节点代理以加载新根。
若节点原来没有该文件，须由操作者独立核对并补齐原发行/撤销公钥及节点可达的
mTLS `origin`；不能只写 downgrade 一项或从资源包自带信息建立信任。

## 再启用平台签发

确认所有目标节点已加载公钥后，将新私钥安装到平台专用秘密目录，文件仅供平台
运行 UID 读取，挂载为只读，并通过已有受保护配置入口设置：

```properties
auriga.resource.downgrade-kid=development-resource-unique-batch-downgrade
auriga.resource.downgrade-key-file=/run/secrets/auriga-resource/downgrade.pk8
```

平台重启后，核对实际挂载为只读、配置 kid 与节点一致。保留本次修改前的配置备份，
不要改写 release/revocation 私钥、runtime 根、节点身份、已消费授权 ID 或序号高水位。

## 完整 runtime 与验收

云端更新使用 `deploy/compose/scripts/build-real-dsh.mjs` 和完整 real-dsh 配方，
保留锁定版本及基础镜像摘要。执行脚本的隔离制品检查，再按
[runtime 发行流程](runtime-release.md)取得真实 registry manifest/config、沿原受信
runtime 根签发递增序号，并用 `deploy/nas/scripts/runtime-upgrade.sh` 激活。
运输用的 docker-save 配置 ID 不能代替正式 manifest 摘要或签名。

模板激活后，还须经公开 Workspace 更新/重启入口替换已有实例。核对实际镜像、
generation/observedGeneration 和正常实例页面；仅更新平台镜像或模板不代表实例已升级。
从正常页面验证：受管插件在原生入口不能关闭/卸载，default 可从资源市场卸载，
撤销会停用并回报，高版本撤销后可用一次性降级授权回落到规则版本；禁止个人插件时，
在 DSH 启动前清理个人依赖。不能以 healthy、202 或仅 API 成功替代这些结果。
